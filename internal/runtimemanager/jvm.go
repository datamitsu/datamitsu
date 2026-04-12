package runtimemanager

import (
	"crypto/sha256"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

var jvmHTTPClient = &http.Client{
	Timeout: 5 * time.Minute,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme == "http" {
			return fmt.Errorf("HTTPS to HTTP redirect rejected: %s", req.URL)
		}
		return nil
	},
}

const maxJARDownloadSize = 200 * 1024 * 1024 // 200 MiB

func getJVMBinaryPath(appEnvPath string, appName string) string {
	return filepath.Join(appEnvPath, appName+".jar")
}

// InstallJVMApp downloads a JAR file and installs it in the app environment.
// Safe for concurrent use from multiple goroutines.
func (rm *RuntimeManager) InstallJVMApp(appName string, appConfig *binmanager.AppConfigJVM, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	key := "jvm/" + appName
	entry, _ := rm.appInstall.LoadOrStore(key, &installOnce{})
	once := entry.(*installOnce)
	once.once.Do(func() {
		once.err = rm.installJVMAppOnce(appName, appConfig, files, archives)
	})
	if once.err != nil {
		rm.appInstall.CompareAndDelete(key, entry)
		return once.err
	}
	return nil
}

func (rm *RuntimeManager) installJVMAppOnce(appName string, appConfig *binmanager.AppConfigJVM, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
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
	if _, err := rm.GetRuntimePath(runtimeName); err != nil {
		return fmt.Errorf("failed to get JVM runtime path: %w", err)
	}

	if err := os.MkdirAll(appEnvPath, 0755); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}

	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(appEnvPath)
		}
	}()

	if len(files) > 0 || len(archives) > 0 {
		if err := binmanager.WriteAppFiles(appEnvPath, files, archives); err != nil {
			return fmt.Errorf("failed to write app files/archives for %q: %w", appName, err)
		}
	}

	fmt.Fprintf(os.Stderr, "Downloading %s JAR...\n", appName)

	if err := downloadAndVerifyJAR(appConfig.JarURL, appConfig.JarHash, jarPath); err != nil {
		return fmt.Errorf("failed to download JAR for %q: %w", appName, err)
	}

	fmt.Fprintf(os.Stderr, "Installed %s\n", appName)

	cleanupOnError = false
	return nil
}

func downloadAndVerifyJAR(url, expectedHash, destPath string) error {
	if expectedHash == "" {
		return fmt.Errorf("JAR hash is required but not provided for %s", url)
	}

	resp, err := jvmHTTPClient.Get(url)
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
	reader := io.LimitReader(resp.Body, maxJARDownloadSize+1)
	written, err := io.Copy(io.MultiWriter(tmpFile, hasher), reader)
	if err != nil {
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
func (rm *RuntimeManager) GetJVMCommandInfo(appName string, appConfig *binmanager.AppConfigJVM, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (*binmanager.CommandInfo, error) {
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
		runtimePath, err := rm.GetRuntimePath(runtimeName)
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
