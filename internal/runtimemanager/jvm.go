package runtimemanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/ui"

	"go.uber.org/zap"
)

// jvmHTTPClient has no end-to-end deadline (JARs reach hundreds of MiB on
// arbitrarily slow links); stalls are caught by the progress guard below.
var jvmHTTPClient = httpx.NewHardenedClient(0)

const maxJARDownloadSize = 200 * 1024 * 1024 // 200 MiB

func getJVMBinaryPath(appEnvPath string, appName string) string {
	return filepath.Join(appEnvPath, appName+".jar")
}

// InstallJVMApp downloads a JAR file and installs it in the app environment.
// Safe for concurrent use from multiple goroutines.
func (rm *RuntimeManager) InstallJVMApp(ctx context.Context, appName string, appConfig *binmanager.AppConfigJVM, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	ctx, cancel, timeoutSec := newInstallContext(ctx)
	defer cancel()
	key := "jvm/" + appName
	_, err, _ := rm.appInstall.Do(key, func() (any, error) {
		return nil, rm.installJVMAppOnce(ctx, appName, appConfig, files, archives)
	})
	return wrapInstallTimeout(err, timeoutSec)
}

func (rm *RuntimeManager) installJVMAppOnce(ctx context.Context, appName string, appConfig *binmanager.AppConfigJVM, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	runtimeName, _, err := rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindJVM)
	if err != nil {
		return fmt.Errorf("failed to resolve runtime for %q: %w", appName, err)
	}

	appEnvPath, err := rm.GetJVMAppPath(appName, appConfig, files, archives, runtimeName)
	if err != nil {
		return fmt.Errorf("failed to get app path: %w", err)
	}

	jarPath := getJVMBinaryPath(appEnvPath, appName)

	if _, err := os.Stat(jarPath); err == nil {
		log.Debug("JVM app already installed",
			zap.String("app", appName),
			zap.String("path", jarPath),
		)
		return nil
	}

	// Ensure the JVM runtime binary is available
	if _, err := rm.getRuntimePath(ctx, runtimeName); err != nil {
		return fmt.Errorf("failed to get JVM runtime path: %w", err)
	}

	if err := os.MkdirAll(appEnvPath, 0o755); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}

	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(appEnvPath)
		}
	}()

	if len(files) > 0 || len(archives) > 0 {
		if err := binmanager.WriteAppFiles(ctx, appEnvPath, files, archives); err != nil {
			return fmt.Errorf("failed to write app files/archives for %q: %w", appName, err)
		}
	}

	ui.Current().Statusf(ui.SymStep, "Downloading %s JAR…", appName)

	if err := downloadAndVerifyJAR(ctx, appName, appConfig.JarURL, appConfig.JarHash, jarPath); err != nil {
		return fmt.Errorf("failed to download JAR for %q: %w", appName, err)
	}

	ui.Current().Statusf(ui.SymOK, "Installed %s", appName)

	cleanupOnError = false
	return nil
}

func downloadAndVerifyJAR(ctx context.Context, name, url, expectedHash, destPath string) error {
	if err := httpx.GuardOffline("JAR download of " + name); err != nil {
		return err
	}
	if expectedHash == "" {
		return fmt.Errorf("JAR hash is required but not provided for %s", url)
	}

	guard, ctx := httpx.NewStallGuard(ctx, httpx.DefaultStallWindow)
	defer guard.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to build JAR download request: %w", err)
	}

	resp, err := jvmHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download JAR: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JAR download returned status %d for %s", resp.StatusCode, url)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), "jar-download-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
		_ = os.Remove(tmpPath)
	}()

	hasher := sha256.New()
	limited := guard.Reader(io.LimitReader(resp.Body, maxJARDownloadSize+1))
	// Render a progress bar (or throttled lines in CI) for the JAR transfer,
	// consistent with binary and runtime downloads.
	tracked := ui.Current().Download(name, resp.ContentLength, limited)
	defer func() { _ = tracked.Close() }()
	written, err := io.Copy(io.MultiWriter(tmpFile, hasher), tracked)
	if err != nil {
		if guard.Stalled() {
			return fmt.Errorf("JAR download of %s stalled: no data received for %s", name, guard.Window())
		}
		return fmt.Errorf("failed to download JAR: %w", err)
	}
	if written > maxJARDownloadSize {
		return fmt.Errorf("JAR download exceeds maximum size of %d bytes", maxJARDownloadSize)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	closed = true

	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != expectedHash {
		return fmt.Errorf("JAR hash mismatch: expected %s, got %s", expectedHash, actualHash)
	}

	if err := moveFile(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to move JAR to destination: %w", err)
	}

	return nil
}

// GetJVMAppPath returns the cache path for an installed JVM app environment.
func (rm *RuntimeManager) GetJVMAppPath(appName string, appConfig *binmanager.AppConfigJVM, files map[string]string, archives map[string]*binmanager.ArchiveSpec, runtimeName string) (string, error) {
	return rm.GetAppPath(appName, config.RuntimeKindJVM, appConfig.Version, nil, appConfig.JarHash, files, archives, runtimeName)
}

// GetJVMCommandInfo returns command info for running a JVM app (java -jar <jar>).
func (rm *RuntimeManager) GetJVMCommandInfo(ctx context.Context, appName string, appConfig *binmanager.AppConfigJVM, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (*binmanager.CommandInfo, error) {
	runtimeName, rc, err := rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindJVM)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve runtime for %q: %w", appName, err)
	}

	appEnvPath, err := rm.GetJVMAppPath(appName, appConfig, files, archives, runtimeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get app path: %w", err)
	}

	jarPath := getJVMBinaryPath(appEnvPath, appName)

	// Determine the java binary path
	var javaBin string
	if rc.Mode == config.RuntimeModeSystem {
		if rc.System != nil {
			javaBin = rc.System.Command
		} else {
			javaBin = "java"
		}
	} else {
		runtimePath, err := rm.getRuntimePath(ctx, runtimeName)
		if err != nil {
			return nil, fmt.Errorf("failed to get JVM runtime path: %w", err)
		}
		javaBin = runtimePath
	}

	var args []string
	if appConfig.MainClass != "" {
		args = []string{"-cp", jarPath, appConfig.MainClass}
	} else {
		args = []string{"-jar", jarPath}
	}

	return &binmanager.CommandInfo{
		Type:    "jvm",
		Command: javaBin,
		Args:    args,
	}, nil
}
