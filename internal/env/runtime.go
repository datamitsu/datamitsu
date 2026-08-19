package env

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/hashutil"
)

// GetRuntimesPath returns the root directory for managed runtimes ({store}/.runtimes).
func GetRuntimesPath() string {
	return filepath.Join(GetStorePath(), ".runtimes")
}

// GetRuntimeBinaryPath returns the install directory for a runtime, keyed by its config hash.
func GetRuntimeBinaryPath(runtimeName string, configHash string) string {
	return filepath.Join(GetRuntimesPath(), runtimeName, configHash)
}

// GetAppsPath returns the root directory for managed app environments ({store}/.apps).
func GetAppsPath() string {
	return filepath.Join(GetStorePath(), ".apps")
}

// GetAppEnvPath returns the per-app environment directory, keyed by runtime kind and config hash.
func GetAppEnvPath(runtimeKind string, appName string, configHash string) string {
	return filepath.Join(GetAppsPath(), runtimeKind, appName, configHash)
}

// GetPNPMStorePath returns the shared pnpm content-addressable store ({store}/.pnpm-store).
func GetPNPMStorePath() string {
	return filepath.Join(GetStorePath(), ".pnpm-store")
}

// GetPNPMPath returns the path to the pnpm CLI entrypoint within the given store root.
func GetPNPMPath(storeRoot string, pnpmVersion string, pnpmHash string) string {
	return filepath.Join(storeRoot, ".runtimes", "pnpm", pnpmVersion, pnpmHash, "package", "bin", "pnpm.cjs")
}

// HashProjectPath computes the XXH3-128 hash of a project path.
// Used for cache directory naming. Shared between env and cache packages.
func HashProjectPath(projectPath string) string {
	return hashutil.XXH3Hex([]byte(projectPath))
}

// ProjectManifestFileName is the file name of the source-mode farm manifest,
// stored alongside the farm's bin directory under the per-root cache directory.
const ProjectManifestFileName = "manifest.json"

// projectRootDir returns the per-git-root cache directory
// ({cache}/projects/{XXH3-128(gitRoot)}), rejecting roots that are not absolute.
func projectRootDir(gitRoot string) (string, error) {
	if gitRoot == "" {
		return "", errors.New("gitRoot must not be empty")
	}
	if !filepath.IsAbs(gitRoot) {
		return "", fmt.Errorf("gitRoot must be absolute: %q", gitRoot)
	}

	return filepath.Clean(filepath.Join(
		GetCachePath(),
		"projects",
		HashProjectPath(gitRoot),
	)), nil
}

// GetProjectBinPath returns the source-mode farm directory for a git root
// ({cache}/projects/{XXH3-128(gitRoot)}/bin). The hash is an internal fingerprint
// only, never compared against an external value.
func GetProjectBinPath(gitRoot string) (string, error) {
	dir, err := projectRootDir(gitRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bin"), nil
}

// GetProjectManifestPath returns the source-mode farm manifest file for a git root
// ({cache}/projects/{XXH3-128(gitRoot)}/manifest.json), a sibling of the farm
// directory returned by GetProjectBinPath.
func GetProjectManifestPath(gitRoot string) (string, error) {
	dir, err := projectRootDir(gitRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ProjectManifestFileName), nil
}

// GetProjectCachePath returns a per-project, per-tool cache directory, rejecting
// inputs that would escape the cache root.
func GetProjectCachePath(gitRoot string, relativeProjectPath string, toolName string) (string, error) {
	projectHash := HashProjectPath(gitRoot)

	if relativeProjectPath != "" {
		if filepath.IsAbs(relativeProjectPath) {
			return "", fmt.Errorf("relativeProjectPath must not be absolute: %q", relativeProjectPath)
		}
		cleaned := filepath.Clean(relativeProjectPath)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("relativeProjectPath must not escape cache directory: %q", relativeProjectPath)
		}
		relativeProjectPath = cleaned
	}

	if toolName != "" {
		if strings.Contains(toolName, "/") || strings.Contains(toolName, "\\") || strings.Contains(toolName, "..") {
			return "", fmt.Errorf("invalid tool name: %q", toolName)
		}
	}

	return filepath.Clean(filepath.Join(
		GetCachePath(),
		"projects",
		projectHash,
		"cache",
		relativeProjectPath,
		toolName,
	)), nil
}
