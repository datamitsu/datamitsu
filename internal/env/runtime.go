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

// ProjectLockFileName is the file name of the per-root advisory lock taken while
// the source-mode farm is baked. It sits beside the farm directory returned by
// GetProjectBinPath. The file is advisory-locked during a bake and is
// deliberately never unlinked: unlinking it would let a second process lock a
// fresh inode for the same root and bake concurrently.
const ProjectLockFileName = "lock"

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

// configFarmsDirName is the cache namespace holding farms whose identity comes
// from an explicitly named config chain instead of a git root.
//
// It is deliberately a sibling of "projects" rather than a differently-hashed
// entry inside it. The two identities are computed from different kinds of
// input — a directory on one side, an ordered list of files on the other — and
// nothing stops a hash of one from colliding with a hash of the other if they
// ever shared a namespace. Separate directories make the collision impossible by
// construction, and leave a future store GC able to tell the two kinds apart by
// path alone.
const configFarmsDirName = "configs"

// ResolveConfigChain returns the resolved, absolute, cleaned form of each config
// path, in the order given.
//
// Order is preserved and duplicates are kept, because the chain is a merge
// order: `--config a --config b` and `--config b --config a` select different
// effective configs and must therefore be different farms.
//
// Symlinks are followed so that `--config ./cfg.ts`, `--config /abs/cfg.ts` and
// a symlink pointing at the same file all name one farm rather than three. A
// path that cannot be resolved — most commonly because it does not exist yet —
// falls back to its cleaned absolute form: identity must be computable before
// the caller has decided whether a missing config is an error, and the
// non-existent path is still a stable name for it.
func ResolveConfigChain(paths []string) ([]string, error) {
	resolved := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			return nil, errors.New("config path must not be empty")
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("resolve config path %q: %w", p, err)
		}
		abs = filepath.Clean(abs)
		if resolvedPath, err := filepath.EvalSymlinks(abs); err == nil {
			abs = filepath.Clean(resolvedPath)
		}
		resolved = append(resolved, abs)
	}
	return resolved, nil
}

// ConfigFarmIdentity returns the XXH3-128 fingerprint of a config chain.
//
// The input is run through ResolveConfigChain first, so the function is
// idempotent: passing an already-resolved chain returns the same identity as
// passing the paths the user typed. The hash is an internal fingerprint only,
// never compared against a value from any external source.
func ConfigFarmIdentity(configPaths []string) (string, error) {
	if len(configPaths) == 0 {
		return "", errors.New("config chain must not be empty")
	}
	resolved, err := ResolveConfigChain(configPaths)
	if err != nil {
		return "", err
	}
	parts := make([][]byte, 0, len(resolved))
	for _, p := range resolved {
		parts = append(parts, []byte(p))
	}
	return hashutil.XXH3Multi(parts...), nil
}

// configFarmRootDir returns the per-chain cache directory
// ({cache}/configs/{XXH3-128(resolved chain)}).
func configFarmRootDir(configPaths []string) (string, error) {
	identity, err := ConfigFarmIdentity(configPaths)
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(
		GetCachePath(),
		configFarmsDirName,
		identity,
	)), nil
}

// GetConfigFarmBinPath returns the source-mode farm directory for an explicitly
// named config chain ({cache}/configs/{XXH3-128(resolved chain)}/bin).
func GetConfigFarmBinPath(configPaths []string) (string, error) {
	dir, err := configFarmRootDir(configPaths)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bin"), nil
}

// GetConfigFarmManifestPath returns the manifest file for an explicit-config
// farm, a sibling of the directory returned by GetConfigFarmBinPath.
func GetConfigFarmManifestPath(configPaths []string) (string, error) {
	dir, err := configFarmRootDir(configPaths)
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
