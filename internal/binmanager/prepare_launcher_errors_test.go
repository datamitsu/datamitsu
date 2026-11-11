package binmanager

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestPreparedKeyRejectsUnavailableDependencyLauncher(t *testing.T) {
	manager := prepareTestManager(t)
	dependency := manager.mapOfApps["dep"]
	dependency.Binary = nil
	dependency.Node = &AppConfigNode{PackageName: "frontend", Version: "1", BinPath: "entrypoint.mjs"}
	manager.mapOfApps["dep"] = dependency
	unavailable := errors.New("dependency runtime unavailable")
	manager.runtimeManager = &mockRuntimeAppManager{
		computeAppPathFunc: func(string, App) (string, error) {
			return filepath.Join(env.GetStorePath(), ".apps", "node", "dep"), nil
		},
		resolveCommandInfoFunc: func(string, App) (*CommandInfo, error) { return nil, unavailable },
	}
	key, err := manager.ComputePreparedKey("root")
	if !errors.Is(err, unavailable) || key != "" {
		t.Fatalf("unresolved runtime produced a reusable preparation key: key=%q err=%v", key, err)
	}
}

func TestPreparedKeyChangesWhenDependencyAliasesSwapTargets(t *testing.T) {
	manager := prepareTestManager(t)
	binary := manager.mapOfApps["dep"]
	manager.mapOfApps["left"] = binary
	manager.mapOfApps["right"] = binary
	dependency := App{
		Node:       &AppConfigNode{PackageName: "frontend", Version: "1", BinPath: "entrypoint.mjs"},
		DependsOn:  []AppDependency{{App: "left", Alias: "validator"}, {App: "right", Alias: "other"}},
		RuntimeEnv: map[string]string{"VALIDATOR_BIN": "${APP_BIN:validator}"},
	}
	manager.mapOfApps["dep"] = dependency
	manager.runtimeManager = &mockRuntimeAppManager{
		computeAppPathFunc: func(string, App) (string, error) {
			return filepath.Join(env.GetStorePath(), ".apps", "node", "dep"), nil
		},
	}
	before, err := manager.ComputePreparedKey("root")
	if err != nil {
		t.Fatal(err)
	}
	dependency.DependsOn = []AppDependency{{App: "right", Alias: "validator"}, {App: "left", Alias: "other"}}
	manager.mapOfApps["dep"] = dependency
	after, err := manager.ComputePreparedKey("root")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("prepared state was reused after the validator binding changed targets")
	}
}
