package env

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/datamitsu/datamitsu/internal/hashutil"
)

func GetRuntimesPath() string {
	return filepath.Join(GetStorePath(), ".runtimes")
}

func GetRuntimeBinaryPath(runtimeName string, configHash string) string {
	return filepath.Join(GetRuntimesPath(), runtimeName, configHash)
}

func GetAppsPath() string {
	return filepath.Join(GetStorePath(), ".apps")
}

func GetAppEnvPath(runtimeKind string, appName string, configHash string) string {
	return filepath.Join(GetAppsPath(), runtimeKind, appName, configHash)
}

func GetPNPMStorePath() string {
	return filepath.Join(GetStorePath(), ".pnpm-store")
}

func GetNodeBinaryPath(storeRoot string, nodeVersion string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(storeRoot, ".runtimes", "fnm-nodes", "v"+nodeVersion, "installation", "node.exe")
	}
	return filepath.Join(storeRoot, ".runtimes", "fnm-nodes", "v"+nodeVersion, "installation", "bin", "node")
}

func GetPNPMPath(storeRoot string, pnpmVersion string, pnpmHash string) string {
	return filepath.Join(storeRoot, ".runtimes", "fnm-pnpm", pnpmVersion, pnpmHash, "package", "bin", "pnpm.cjs")
}

// HashProjectPath computes the XXH3-128 hash of a project path.
// Used for cache directory naming. Shared between env and cache packages.
func HashProjectPath(projectPath string) string {
	return hashutil.XXH3Hex([]byte(projectPath))
}

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
