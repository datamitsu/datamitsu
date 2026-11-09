package runtimemanager

import (
	"fmt"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

// ComputeRuntimeDataPath returns the identity-specific mutable data directory for a runtime.
func (rm *RuntimeManager) ComputeRuntimeDataPath(runtimeName string) (string, error) {
	rc, ok := rm.mapOfRuntimes[runtimeName]
	if !ok {
		return "", fmt.Errorf("runtime %q not found", runtimeName)
	}
	return rm.runtimeDataPath(runtimeName, rc)
}

func (rm *RuntimeManager) runtimeDataPath(runtimeName string, rc config.RuntimeConfig) (string, error) {
	osType, archType, libc, err := rm.hostRuntimePlatform()
	if err != nil {
		return "", err
	}
	identity, err := rm.runtimeIdentityHash(rc, osType, archType, libc)
	if err != nil {
		return "", fmt.Errorf("identify runtime %q data: %w", runtimeName, err)
	}
	dataRoot := env.GetRuntimeDataPath(runtimeName, identity)
	storeRoot := env.GetStorePath()
	relative, err := filepath.Rel(storeRoot, dataRoot)
	if err != nil {
		return "", fmt.Errorf("resolve runtime %q data root: %w", runtimeName, err)
	}
	validated, err := storelock.ValidateTarget(storeRoot, relative)
	if err != nil {
		return "", fmt.Errorf("runtime %q data root is outside the store: %w", runtimeName, err)
	}
	return validated, nil
}

func (rm *RuntimeManager) pnpmDataPath(runtimeName string, rc config.RuntimeConfig) (string, error) {
	osType, archType, libc, err := rm.hostRuntimePlatform()
	if err != nil {
		return "", err
	}
	identity, err := rm.runtimeIdentityHash(rc, osType, archType, libc)
	if err != nil {
		return "", fmt.Errorf("identify runtime %q data: %w", runtimeName, err)
	}
	pnpmName, pnpmIdentity, err := rm.pnpmIdentityHash(rc, osType, archType, libc)
	if err != nil {
		return "", err
	}
	runtimeRoot := env.GetRuntimeDataPath(runtimeName, identity)
	return filepath.Join(runtimeRoot, "pnpm", pnpmName, pnpmIdentity, "store"), nil
}

// RuntimeOwnedEnvironment keeps runtime-specific mutable state independent of individual app installs.
func (rm *RuntimeManager) RuntimeOwnedEnvironment(runtimeName string, rc config.RuntimeConfig, appEnvPath string) (string, map[string]string, error) {
	if rc.Kind == config.RuntimeKindJVM {
		return "", nil, nil
	}
	runtimeDataPath, err := rm.runtimeDataPath(runtimeName, rc)
	if err != nil {
		return "", nil, err
	}
	switch rc.Kind {
	case config.RuntimeKindNode:
		storePath, err := rm.pnpmDataPath(runtimeName, rc)
		if err != nil {
			return "", nil, err
		}
		return runtimeDataPath, getNodeEnvVars(appEnvPath, storePath, runtimeDataPath), nil
	case config.RuntimeKindBun:
		storePath, err := rm.pnpmDataPath(runtimeName, rc)
		if err != nil {
			return "", nil, err
		}
		return runtimeDataPath, getBunEnvVars(appEnvPath, storePath, runtimeDataPath), nil
	case config.RuntimeKindUV:
		return runtimeDataPath, getUVEnvVars(runtimeDataPath), nil
	case config.RuntimeKindGo:
		return runtimeDataPath, goBaseEnvVars(appEnvPath, runtimeDataPath), nil
	case config.RuntimeKindJVM:
		return "", nil, nil
	case config.RuntimeKindPNPM:
		return "", nil, fmt.Errorf("runtime %q has no runtime-owned environment", runtimeName)
	default:
		return "", nil, fmt.Errorf("runtime %q has no runtime-owned environment", runtimeName)
	}
}

// RuntimeExecutionEnvironment returns only cache paths owned by a direct interpreter run.
func (rm *RuntimeManager) RuntimeExecutionEnvironment(runtimeName string, rc config.RuntimeConfig) (string, map[string]string, error) {
	dataRoot, err := rm.runtimeDataPath(runtimeName, rc)
	if err != nil {
		return "", nil, err
	}
	var vars map[string]string
	switch rc.Kind {
	case config.RuntimeKindNode:
		storePath, err := rm.pnpmDataPath(runtimeName, rc)
		if err != nil {
			return "", nil, err
		}
		vars = map[string]string{
			"npm_config_cache":     filepath.Join(dataRoot, "npm-cache"),
			"npm_config_store_dir": storePath,
			"NODE_COMPILE_CACHE":   filepath.Join(dataRoot, "node-compile"),
		}
	case config.RuntimeKindBun:
		vars = map[string]string{
			"npm_config_cache":                  filepath.Join(dataRoot, "npm-cache"),
			"BUN_RUNTIME_TRANSPILER_CACHE_PATH": filepath.Join(dataRoot, "bun-cache"),
			"BUN_OPTIONS":                       "--no-install",
		}
	case config.RuntimeKindUV:
		vars = getUVEnvVars(dataRoot)
	case config.RuntimeKindGo:
		vars = goBaseEnvVars("", dataRoot)
	case config.RuntimeKindJVM:
		return "", nil, nil
	case config.RuntimeKindPNPM:
		return "", nil, fmt.Errorf("runtime %q has no runtime-owned environment", runtimeName)
	default:
		return "", nil, fmt.Errorf("runtime %q has no runtime-owned environment", runtimeName)
	}

	return dataRoot, vars, nil
}
