package runtimemanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"

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
		return "", "", errors.New("go lock file missing mod (go.mod) content")
	}
	if lf.Sum == "" {
		return "", "", errors.New("go lock file missing sum (go.sum) content")
	}
	return lf.Mod, lf.Sum, nil
}

// BuildGoLockFileJSON marshals go.mod and go.sum into the JSON wrapper. The
// result is intended to be passed to CompressLockFile before being stored in
// config. Exported so the config lockfile command can assemble the wrapper from
// a freshly generated go.mod + go.sum.
func BuildGoLockFileJSON(goMod, goSum string) (string, error) {
	data, err := json.Marshal(goLockFile{Mod: goMod, Sum: goSum})
	if err != nil {
		return "", fmt.Errorf("build go lock file JSON: %w", err)
	}
	return string(data), nil
}

// goBaseEnvVars returns the per-app isolation and supply-chain hardening shared
// by the build and generation flows. baseDir is the directory under which the
// per-app GOPATH/GOMODCACHE/GOBIN live so neither flow ever touches the user's
// Go environment.
//
// The verification controls are forced to safe values (not merely cleared) so a
// value inherited from the user's environment cannot weaken the supply chain:
//   - GOTOOLCHAIN=local pins execution to the managed, SHA-256-verified SDK and
//     prevents Go from auto-downloading an unverified toolchain over the network
//     when a module requires a newer Go version (the default GOTOOLCHAIN=auto
//     would do so, bypassing the hash policy — fail fast instead).
//   - GOSUMDB=sum.golang.org keeps checksum-database verification enabled; a
//     user GOSUMDB=off would otherwise let unverified checksums be recorded.
//   - GOPRIVATE/GONOPROXY/GONOSUMDB/GOINSECURE are cleared so no path pattern
//     inherited from the user can opt modules out of proxy + checksum
//     verification or allow insecure (http) fetches. GONOSUMDB must be cleared
//     explicitly: per `go help environment`, it disables checksum-database
//     validation on its own ("GOPRIVATE or GONOSUMDB may be used to achieve
//     that"), and an explicit GONOSUMDB overrides the GOPRIVATE-derived default,
//     so clearing GOPRIVATE alone leaves an inherited GONOSUMDB=* able to skip
//     verification while `go get` records go.sum entries during generation.
//
// These must stay identical across both flows, which is why they live here
// rather than being duplicated in the build and generation env builders.
func goBaseEnvVars(baseDir string) map[string]string {
	return map[string]string{
		"GOPATH":      filepath.Join(baseDir, "gopath"),
		"GOMODCACHE":  filepath.Join(baseDir, "gomodcache"),
		"GOBIN":       filepath.Join(baseDir, "bin"),
		"GOTOOLCHAIN": "local",
		"GOSUMDB":     "sum.golang.org",
		"GOPRIVATE":   "",
		"GONOPROXY":   "",
		"GONOSUMDB":   "",
		"GOINSECURE":  "",
	}
}

// getGoEnvVars returns the isolated build environment for a Go app. It pins
// GOFLAGS=-mod=readonly so any go.sum mismatch fails the build instead of
// silently rewriting go.sum, on top of the shared hardening in goBaseEnvVars.
func getGoEnvVars(appEnvPath string) map[string]string {
	env := goBaseEnvVars(appEnvPath)
	env["GOFLAGS"] = "-mod=readonly -trimpath"
	return env
}

// getGoGenEnvVars returns the isolated environment for generating a Go app's
// lockfile (`go mod init` + `go get`). It shares goBaseEnvVars' isolation and
// verification hardening but, unlike the build env, omits -mod=readonly:
// generation must be allowed to write go.mod and go.sum, which -mod=readonly
// would forbid.
func getGoGenEnvVars(workDir string) map[string]string {
	env := goBaseEnvVars(workDir)
	env["GOFLAGS"] = ""
	return env
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
	return rm.GetAppPath(appName, config.RuntimeKindGo, appConfig.Version, nil, goAppLockHash(appConfig.PackageName, appConfig.LockFile), files, archives, runtimeName)
}

// InstallGoApp builds a Go app from source if not already cached.
// If files/archives are non-empty, writes them to the app directory before building.
// Safe for concurrent use from multiple goroutines.
func (rm *RuntimeManager) InstallGoApp(ctx context.Context, appName string, appConfig *binmanager.AppConfigGo, customEnv map[string]string, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	ctx, cancel, timeoutSec := newInstallContext(ctx)
	defer cancel()
	key := "go/" + appName
	_, err, _ := rm.appInstall.Do(key, func() (any, error) {
		return nil, rm.installGoAppOnce(ctx, appName, appConfig, customEnv, files, archives)
	})
	return wrapInstallTimeout(err, timeoutSec)
}

func (rm *RuntimeManager) installGoAppOnce(ctx context.Context, appName string, appConfig *binmanager.AppConfigGo, customEnv map[string]string, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
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

	goPath, err := rm.getRuntimePath(ctx, runtimeName)
	if err != nil {
		return fmt.Errorf("failed to get Go runtime path: %w", err)
	}

	if err := os.MkdirAll(appEnvPath, 0o755); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}

	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			// appEnvPath holds a read-only GOMODCACHE once `go build` runs, so a
			// plain os.RemoveAll would leak it; ForceRemoveAll restores write bits.
			if err := ForceRemoveAll(appEnvPath); err != nil {
				log.Warn("failed to clean up Go app directory after error",
					zap.String("app", appName),
					zap.String("path", appEnvPath),
					zap.Error(err),
				)
			}
		}
	}()

	reservedEnv := getGoEnvVars(appEnvPath)
	envVars := mergeInstallEnv(reservedEnv, customEnv, appEnvPath)
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOBIN"} {
		if err := os.MkdirAll(reservedEnv[key], 0o755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", reservedEnv[key], err)
		}
	}

	if len(files) > 0 || len(archives) > 0 {
		if err := binmanager.WriteAppFiles(ctx, appEnvPath, files, archives); err != nil {
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
	if err := os.WriteFile(filepath.Join(appEnvPath, "go.mod"), []byte(goMod), 0o644); err != nil {
		return fmt.Errorf("failed to write go.mod for %q: %w", appName, err)
	}
	if err := os.WriteFile(filepath.Join(appEnvPath, "go.sum"), []byte(goSum), 0o644); err != nil {
		return fmt.Errorf("failed to write go.sum for %q: %w", appName, err)
	}

	args := buildGoBuildArgs(appConfig.PackageName, binPath)

	cmd := exec.CommandContext(ctx, goPath, args...) //nolint:gosec // G204: goPath comes from the trusted managed runtime store and args are built from validated config
	cmd.Dir = appEnvPath
	cmd.Env = buildEnvWithOverrides(os.Environ(), envVars)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	log.Debug("building Go app",
		zap.String("app", appName),
		zap.String("package", appConfig.PackageName),
		zap.String("go_path", goPath),
	)

	fmt.Fprintf(os.Stderr, "Installing %s...\n", appName)

	if err := runInstallCmd(ctx, cmd); err != nil {
		return fmt.Errorf("failed to build Go app %q: %w", appName, err)
	}

	fmt.Fprintf(os.Stderr, "Installed %s\n", appName)

	cleanupOnError = false
	return nil
}

// ForceRemoveAll removes root and everything under it, restoring owner rwx on
// every entry first. `go get`/`go build` and pnpm populate the store with files
// and directories marked read-only (0444/0555); a plain os.RemoveAll then fails
// with EACCES because a read-only directory's entries cannot be unlinked,
// leaking 100+MiB of module cache. WalkDir runs pre-order, so each directory is
// made traversable before RemoveAll descends into it.
//
// Every entry gets mode 0o700 unconditionally — NOT 0o600-for-files — because
// the file/dir distinction cannot be trusted here. ReadDir may return a dirent
// with type DT_UNKNOWN (filesystem-dependent), in which case
// fs.DirEntry.IsDir() falls back to an lstat that can fail and report a real
// directory as a non-dir. Giving such a directory a file mode (0o600) strips its
// execute bit, so it can no longer be entered and RemoveAll later fails with
// EACCES on openat. The execute bit on a regular file is harmless — it is about
// to be deleted. Chmod errors are best-effort; the final os.RemoveAll reports
// any failure that actually blocks removal.
func ForceRemoveAll(root string) error {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // best-effort chmod: skip on walk error, RemoveAll reports real failures
		}
		_ = os.Chmod(p, 0o700) //nolint:gosec // G122: best-effort chmod on a datamitsu-owned store path before RemoveAll; failures are ignored and the final RemoveAll reports any real blocker
		return nil
	})
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("failed to remove %q: %w", root, err)
	}
	return nil
}

// removeStaleGoModFiles deletes any leftover go.mod/go.sum in workDir so a
// fresh `go mod init` does not fail on a pre-existing module. A missing file is
// expected and ignored; any other failure (e.g. permission denied) is reported
// so generation does not silently proceed on a workdir it could not clean.
func removeStaleGoModFiles(workDir string) error {
	for _, name := range []string{"go.mod", "go.sum"} {
		if err := os.Remove(filepath.Join(workDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("failed to clean stale %s in workDir: %w", name, err)
		}
	}
	return nil
}

// GenerateGoLockFiles produces a fresh go.mod + go.sum for a Go app in
// workDir by running `go mod init` followed by `go get packageName@version`.
//
// This is the generation counterpart to installGoAppOnce: installs require a
// lockfile (it is mandatory and the build refuses without it), so the lockfile
// itself cannot be produced by a normal reinstall. The config lockfile command
// calls this to resolve transitive dependencies and write the checksums, then
// reads the resulting files back. The files are left in workDir for the caller.
func (rm *RuntimeManager) GenerateGoLockFiles(ctx context.Context, appName string, appConfig *binmanager.AppConfigGo, workDir string) error {
	if appConfig.PackageName == "" {
		return fmt.Errorf("app %q has no packageName; cannot generate lock file", appName)
	}
	if appConfig.Version == "" {
		return fmt.Errorf("app %q has no version; cannot generate lock file", appName)
	}

	runtimeName, _, err := rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindGo)
	if err != nil {
		return fmt.Errorf("failed to resolve runtime for %q: %w", appName, err)
	}

	goPath, err := rm.getRuntimePath(ctx, runtimeName)
	if err != nil {
		return fmt.Errorf("failed to get Go runtime path: %w", err)
	}

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("failed to create work directory: %w", err)
	}

	// Start from a clean module: a leftover go.mod would make `go mod init` fail.
	if err := removeStaleGoModFiles(workDir); err != nil {
		return err
	}

	envVars := getGoGenEnvVars(workDir)
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOBIN"} {
		if err := os.MkdirAll(envVars[key], 0o755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", envVars[key], err)
		}
	}
	fullEnv := buildEnvWithOverrides(os.Environ(), envVars)

	runGo := func(args ...string) error {
		cmd := exec.CommandContext(ctx, goPath, args...) //nolint:gosec // G204: goPath comes from the trusted managed runtime store and args are built from validated config
		cmd.Dir = workDir
		cmd.Env = fullEnv
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	log.Debug("generating Go lock file",
		zap.String("app", appName),
		zap.String("package", appConfig.PackageName),
		zap.String("version", appConfig.Version),
		zap.String("go_path", goPath),
	)

	if err := runGo("mod", "init", "datamitsu-"+appName); err != nil {
		return fmt.Errorf("failed to init go module for %q: %w", appName, err)
	}

	if err := runGo("get", appConfig.PackageName+"@"+appConfig.Version); err != nil {
		return fmt.Errorf("failed to resolve %s@%s for %q: %w", appConfig.PackageName, appConfig.Version, appName, err)
	}

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
