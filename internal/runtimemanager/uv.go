package runtimemanager

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"go.uber.org/zap"
)

func getUVEnvVars(appEnvPath string) map[string]string {
	return map[string]string{
		"UV_CACHE_DIR": filepath.Join(appEnvPath, "cache"),
		// Redirect uv's managed CPython into the cached store (shared per-version,
		// not per-app) so the store cache is self-contained and venv interpreter
		// symlinks resolve after a cache restore onto a fresh runner.
		"UV_PYTHON_INSTALL_DIR": filepath.Join(env.GetStorePath(), ".uv", "python"),
	}
}

func getUVBinaryPath(appEnvPath string, packageName string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(appEnvPath, ".venv", "Scripts", packageName+".exe")
	}
	return filepath.Join(appEnvPath, ".venv", "bin", packageName)
}

// InstallUVApp installs a UV app if not already cached.
// If files is non-empty, writes them to the app directory before running uv.
// Safe for concurrent use from multiple goroutines.
func (rm *RuntimeManager) InstallUVApp(appName string, appConfig *binmanager.AppConfigUV, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	key := "uv/" + appName
	_, err, _ := rm.appInstall.Do(key, func() (any, error) {
		return nil, rm.installUVAppOnce(appName, appConfig, files, archives)
	})
	return err
}

func (rm *RuntimeManager) installUVAppOnce(appName string, appConfig *binmanager.AppConfigUV, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	runtimeName, rc, err := rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindUV)
	if err != nil {
		return fmt.Errorf("failed to resolve runtime for %q: %w", appName, err)
	}

	appEnvPath, err := rm.GetAppPath(appName, config.RuntimeKindUV, uvVersionForHash(appConfig.Version, appConfig.RequiresPython), nil, lockFileHash(appConfig.LockFile), files, archives, runtimeName)
	if err != nil {
		return fmt.Errorf("failed to get app path: %w", err)
	}

	binPath := getUVBinaryPath(appEnvPath, appConfig.PackageName)

	if _, err := os.Stat(binPath); err == nil {
		log.Debug("UV app already installed",
			zap.String("app", appName),
			zap.String("path", binPath),
		)
		return nil
	}

	uvPath, err := rm.GetRuntimePath(runtimeName)
	if err != nil {
		return fmt.Errorf("failed to get runtime path: %w", err)
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

	envVars := getUVEnvVars(appEnvPath)
	for _, dir := range envVars {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", dir, err)
		}
	}

	if len(files) > 0 || len(archives) > 0 {
		if err := binmanager.WriteAppFiles(appEnvPath, files, archives); err != nil {
			return fmt.Errorf("failed to write app files/archives for %q: %w", appName, err)
		}
	}

	reqPython := resolveRequiresPython(appConfig.RequiresPython)
	pyprojectTOML := buildPyprojectTOML(appName, appConfig.PackageName, appConfig.Version, reqPython)
	if err := os.WriteFile(filepath.Join(appEnvPath, "pyproject.toml"), []byte(pyprojectTOML), 0644); err != nil {
		return fmt.Errorf("failed to write pyproject.toml for %q: %w", appName, err)
	}

	if appConfig.LockFile != "" {
		lockContent, decErr := DecompressLockFile(appConfig.LockFile)
		if decErr != nil {
			return fmt.Errorf("failed to decompress lock file for %q: %w", appName, decErr)
		}
		lockFilePath := filepath.Join(appEnvPath, "uv.lock")
		if err := os.WriteFile(lockFilePath, []byte(lockContent), 0644); err != nil {
			return fmt.Errorf("failed to write uv.lock for %q: %w", appName, err)
		}
	}

	args := buildUVInstallArgs(appConfig.LockFile, rc.UV)

	cmd := exec.Command(uvPath, args...)
	cmd.Dir = appEnvPath
	cmd.Env = buildEnvWithOverrides(os.Environ(), envVars)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	packageSpec := appConfig.PackageName
	if appConfig.Version != "" {
		packageSpec += "==" + appConfig.Version
	}

	log.Debug("installing UV app",
		zap.String("app", appName),
		zap.String("package", packageSpec),
		zap.String("uv_path", uvPath),
	)

	fmt.Fprintf(os.Stderr, "Installing %s...\n", appName)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install UV app %q: %w", appName, err)
	}

	fmt.Fprintf(os.Stderr, "Installed %s\n", appName)

	cleanupOnError = false
	return nil
}

// buildUVInstallArgs constructs the arguments for `uv sync` invocations.
//
// When a lockfile is present, --no-build is added so uv only installs
// pre-built wheels. This eliminates arbitrary code execution from setup.py
// or PEP 517 build backends during install. Trade-off: if a dependency has
// no wheel for the current platform, install fails — intentional for supply
// chain security; users must pre-resolve to wheel-available versions in
// their lockfile.
func buildUVInstallArgs(lockFile string, uvRC *config.RuntimeConfigUV) []string {
	args := []string{"sync", "--no-install-project"}

	if lockFile != "" {
		args = append(args, "--locked", "--no-build")
	}

	if uvRC != nil && uvRC.PythonVersion != "" {
		args = append(args, "--python", uvRC.PythonVersion)
	}

	return args
}

func escapeTOMLString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}

func resolveRequiresPython(appRequiresPython string) string {
	if appRequiresPython != "" {
		return appRequiresPython
	}
	return ">=3.12"
}

func uvVersionForHash(version, requiresPython string) string {
	return version + "\x00" + resolveRequiresPython(requiresPython)
}

func buildPyprojectTOML(appName string, packageName string, version string, requiresPython string) string {
	safeName := strings.NewReplacer("@", "", "/", "-").Replace(packageName)
	dep := packageName
	if version != "" {
		dep += "==" + version
	}
	return fmt.Sprintf(`[project]
name = "datamitsu-%s"
version = "0.0.0"
requires-python = "%s"
dependencies = [
    "%s",
]
`, escapeTOMLString(safeName), escapeTOMLString(requiresPython), escapeTOMLString(dep))
}

func (rm *RuntimeManager) GetUVCommandInfo(appName string, appConfig *binmanager.AppConfigUV, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (*binmanager.CommandInfo, error) {
	runtimeName, _, err := rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindUV)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve runtime for %q: %w", appName, err)
	}

	appEnvPath, err := rm.GetAppPath(appName, config.RuntimeKindUV, uvVersionForHash(appConfig.Version, appConfig.RequiresPython), nil, lockFileHash(appConfig.LockFile), files, archives, runtimeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get app path: %w", err)
	}

	binPath := getUVBinaryPath(appEnvPath, appConfig.PackageName)
	envVars := getUVEnvVars(appEnvPath)

	return &binmanager.CommandInfo{
		Type:    "uv",
		Command: binPath,
		Env:     envVars,
	}, nil
}

func buildEnvWithOverrides(base []string, overrides map[string]string) []string {
	env := make([]string, len(base))
	copy(env, base)

	keyToIdx := make(map[string]int, len(env))
	for i, e := range env {
		if j := strings.IndexByte(e, '='); j > 0 {
			keyToIdx[e[:j]] = i
		}
	}

	for key, value := range overrides {
		envVar := fmt.Sprintf("%s=%s", key, value)
		if idx, ok := keyToIdx[key]; ok {
			env[idx] = envVar
		} else {
			keyToIdx[key] = len(env)
			env = append(env, envVar)
		}
	}

	return env
}
