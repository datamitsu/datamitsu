package binmanager

import (
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestPreparedKeyIgnoresValuesOverriddenByRuntime(t *testing.T) {
	manager := prepareTestManager(t)
	dependency := App{
		Node:       &AppConfigNode{PackageName: "frontend", Version: "1", BinPath: "entrypoint.mjs"},
		Env:        map[string]string{"NODE_COMPILE_CACHE": "ignored-install-value"},
		RuntimeEnv: map[string]string{"NODE_COMPILE_CACHE": "ignored-runtime-value"},
	}
	manager.mapOfApps["dep"] = dependency
	manager.runtimeManager = &mockRuntimeAppManager{
		computeAppPathFunc: func(string, App) (string, error) {
			return filepath.Join(env.GetStorePath(), ".apps", "node", "dep"), nil
		},
		resolveCommandInfoFunc: func(string, App) (*CommandInfo, error) {
			return &CommandInfo{Env: map[string]string{"NODE_COMPILE_CACHE": "runtime-owned-cache"}}, nil
		},
	}
	before, err := manager.ComputePreparedKey("root")
	if err != nil {
		t.Fatal(err)
	}
	if dependency.Env["NODE_COMPILE_CACHE"] != "ignored-install-value" || dependency.RuntimeEnv["NODE_COMPILE_CACHE"] != "ignored-runtime-value" {
		t.Fatal("computing preparation identity mutated the app configuration")
	}
	dependency.Env = map[string]string{"NODE_COMPILE_CACHE": "different-ignored-install-value"}
	dependency.RuntimeEnv = map[string]string{"NODE_COMPILE_CACHE": "different-ignored-runtime-value"}
	manager.mapOfApps["dep"] = dependency
	after, err := manager.ComputePreparedKey("root")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("an environment value the runtime overrides invalidated prepared state")
	}
}
