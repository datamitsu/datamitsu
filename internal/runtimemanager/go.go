package runtimemanager

import (
	"encoding/json"
	"fmt"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"

	"go.uber.org/zap"
)

// goLockFile is the JSON wrapper persisted (after brotli compression) as a
// Go app's lockFile. It carries the full go.mod and go.sum so the app can be
// rebuilt deterministically with `go build -mod=readonly`, where any go.sum
// mismatch fails the build — the supply chain guarantee for Go apps.
type goLockFile struct {
	Mod string `json:"mod"`
	Sum string `json:"sum"`
}

// parseGoLockFile decodes the JSON wrapper (already decompressed) into the
// go.mod and go.sum contents. Both fields are mandatory: go.mod identifies the
// module graph and go.sum carries the cryptographic checksums that make the
// build verifiable. A missing or empty field is treated as a configuration
// error rather than silently building without verification.
func parseGoLockFile(lockFile string) (goMod, goSum string, err error) {
	var lf goLockFile
	if err := json.Unmarshal([]byte(lockFile), &lf); err != nil {
		return "", "", fmt.Errorf("parse go lock file: %w", err)
	}
	if lf.Mod == "" {
		return "", "", fmt.Errorf("go lock file missing mod (go.mod) content")
	}
	if lf.Sum == "" {
		return "", "", fmt.Errorf("go lock file missing sum (go.sum) content")
	}
	return lf.Mod, lf.Sum, nil
}

// buildGoLockFileJSON marshals go.mod and go.sum into the JSON wrapper. The
// result is intended to be passed to CompressLockFile before being stored in
// config.
func buildGoLockFileJSON(goMod, goSum string) (string, error) {
	data, err := json.Marshal(goLockFile{Mod: goMod, Sum: goSum})
	if err != nil {
		return "", fmt.Errorf("build go lock file JSON: %w", err)
	}
	return string(data), nil
}

// getGoEnvVars returns the isolated build environment for a Go app. Each app
// gets its own GOPATH/GOMODCACHE/GOBIN so installs never touch the user's Go
// environment. GONOSUMCHECK and GONOSUMDB are force-cleared so a user value
// disabling checksum verification cannot weaken the build, and GOFLAGS pins
// -mod=readonly so any go.sum mismatch fails the build.
func getGoEnvVars(appEnvPath string) map[string]string {
	return map[string]string{
		"GOPATH":       filepath.Join(appEnvPath, "gopath"),
		"GOMODCACHE":   filepath.Join(appEnvPath, "gomodcache"),
		"GOBIN":        filepath.Join(appEnvPath, "bin"),
		"GONOSUMCHECK": "",
		"GONOSUMDB":    "",
		"GOFLAGS":      "-mod=readonly -trimpath",
	}
}

// goBinaryName derives the built binary's filename from the Go package path.
// `go build` names the output after the last path element (with .exe on
// Windows), e.g. "golang.org/x/vuln/cmd/govulncheck" -> "govulncheck".
func goBinaryName(packageName string) string {
	name := path.Base(packageName)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// getGoBinaryPath returns the path of the binary produced by building
// packageName into appEnvPath's bin directory.
func getGoBinaryPath(appEnvPath string, packageName string) string {
	return filepath.Join(appEnvPath, "bin", goBinaryName(packageName))
}

// buildGoBuildArgs constructs the `go build` arguments. -trimpath strips local
// filesystem paths for reproducibility, -mod=readonly forbids any go.mod/go.sum
// mutation and fails immediately on a missing or mismatched go.sum entry — the
// supply chain guarantee. The package path carries no version: the version is
// pinned in the lockfile's go.mod.
func buildGoBuildArgs(packageName, outputPath string) []string {
	return []string{"build", "-trimpath", "-mod=readonly", "-o", outputPath, packageName}
}

// GetGoAppPath returns the cache path for an installed Go app environment.
func (rm *RuntimeManager) GetGoAppPath(appName string, appConfig *binmanager.AppConfigGo, files map[string]string, archives map[string]*binmanager.ArchiveSpec, runtimeName string) (string, error) {
	return rm.GetAppPath(appName, config.RuntimeKindGo, appConfig.Version, nil, lockFileHash(appConfig.LockFile), files, archives, runtimeName)
}

// InstallGoApp builds a Go app from source if not already cached.
// If files/archives are non-empty, writes them to the app directory before building.
// Safe for concurrent use from multiple goroutines.
func (rm *RuntimeManager) InstallGoApp(appName string, appConfig *binmanager.AppConfigGo, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	key := "go/" + appName
	entry, _ := rm.appInstall.LoadOrStore(key, &installOnce{})
	once := entry.(*installOnce)
	once.once.Do(func() {
		once.err = rm.installGoAppOnce(appName, appConfig, files, archives)
	})
	if once.err != nil {
		rm.appInstall.CompareAndDelete(key, entry)
		return once.err
	}
	return nil
}

func (rm *RuntimeManager) installGoAppOnce(appName string, appConfig *binmanager.AppConfigGo, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	runtimeName, _, err := rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindGo)
	if err != nil {
		return fmt.Errorf("failed to resolve runtime for %q: %w", appName, err)
	}

	appEnvPath, err := rm.GetGoAppPath(appName, appConfig, files, archives, runtimeName)
	if err != nil {
		return fmt.Errorf("failed to get app path: %w", err)
	}

	binPath := getGoBinaryPath(appEnvPath, appConfig.PackageName)

	if _, err := os.Stat(binPath); err == nil {
		log.Debug("Go app already installed",
			zap.String("app", appName),
			zap.String("path", binPath),
		)
		return nil
	}

	// Lockfile is mandatory: without go.mod + go.sum there is nothing to
	// verify the build against, so refuse rather than build unverified.
	if appConfig.LockFile == "" {
		return fmt.Errorf("app %q has no lockFile; a lockFile (go.mod + go.sum) is mandatory for Go apps", appName)
	}

	goPath, err := rm.GetRuntimePath(runtimeName)
	if err != nil {
		return fmt.Errorf("failed to get Go runtime path: %w", err)
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

	envVars := getGoEnvVars(appEnvPath)
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOBIN"} {
		if err := os.MkdirAll(envVars[key], 0755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", envVars[key], err)
		}
	}

	if len(files) > 0 || len(archives) > 0 {
		if err := binmanager.WriteAppFiles(appEnvPath, files, archives); err != nil {
			return fmt.Errorf("failed to write app files/archives for %q: %w", appName, err)
		}
	}

	lockContent, err := DecompressLockFile(appConfig.LockFile)
	if err != nil {
		return fmt.Errorf("failed to decompress lock file for %q: %w", appName, err)
	}
	goMod, goSum, err := parseGoLockFile(lockContent)
	if err != nil {
		return fmt.Errorf("invalid lock file for %q: %w", appName, err)
	}
	if err := os.WriteFile(filepath.Join(appEnvPath, "go.mod"), []byte(goMod), 0644); err != nil {
		return fmt.Errorf("failed to write go.mod for %q: %w", appName, err)
	}
	if err := os.WriteFile(filepath.Join(appEnvPath, "go.sum"), []byte(goSum), 0644); err != nil {
		return fmt.Errorf("failed to write go.sum for %q: %w", appName, err)
	}

	args := buildGoBuildArgs(appConfig.PackageName, binPath)

	cmd := exec.Command(goPath, args...)
	cmd.Dir = appEnvPath
	cmd.Env = buildEnvWithOverrides(os.Environ(), envVars)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	log.Debug("building Go app",
		zap.String("app", appName),
		zap.String("package", appConfig.PackageName),
		zap.String("go_path", goPath),
	)

	fmt.Fprintf(os.Stderr, "Building %s...\n", appName)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to build Go app %q: %w", appName, err)
	}

	fmt.Fprintf(os.Stderr, "Installed %s\n", appName)

	cleanupOnError = false
	return nil
}

// GetGoCommandInfo returns command info for running a Go app. The built binary
// is self-contained, so no environment overrides are required to run it.
func (rm *RuntimeManager) GetGoCommandInfo(appName string, appConfig *binmanager.AppConfigGo, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (*binmanager.CommandInfo, error) {
	runtimeName, _, err := rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindGo)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve runtime for %q: %w", appName, err)
	}

	appEnvPath, err := rm.GetGoAppPath(appName, appConfig, files, archives, runtimeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get app path: %w", err)
	}

	binPath := getGoBinaryPath(appEnvPath, appConfig.PackageName)

	return &binmanager.CommandInfo{
		Type:    "go",
		Command: binPath,
	}, nil
}
