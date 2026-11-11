package binmanager

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func bindingErrorFixture(t *testing.T) MapOfApps {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return MapOfApps{
		"root":     {Shell: &AppConfigShell{Name: executable}, DependsOn: edges("frontend"), RuntimeEnv: map[string]string{"FRONTEND_BIN": "${APP_BIN:frontend}"}},
		"frontend": {Node: &AppConfigNode{PackageName: "frontend", Version: "1", BinPath: "entrypoint.mjs"}},
	}
}

func TestNodeDependencyBindingWithoutRuntimeManagerFails(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	manager := New(bindingErrorFixture(t), nil, nil)
	if _, _, err := manager.ResolveCommandInfo("root"); err == nil || !strings.Contains(err.Error(), "frontend") {
		t.Fatalf("missing runtime was not attributed to its dependency: %v", err)
	}
}

func TestNodeDependencyBindingPreservesRuntimeResolutionError(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	unavailable := errors.New("runtime executable unavailable")
	manager := New(bindingErrorFixture(t), nil, &mockRuntimeAppManager{
		resolveCommandInfoFunc: func(string, App) (*CommandInfo, error) { return nil, unavailable },
	})
	if _, _, err := manager.ResolveCommandInfo("root"); !errors.Is(err, unavailable) {
		t.Fatalf("dependency resolution lost its original failure: %v", err)
	}
}

func TestNodeDependencyBindingRejectsUndeclaredNestedAlias(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	apps := bindingErrorFixture(t)
	frontend := apps["frontend"]
	frontend.RuntimeEnv = map[string]string{"NESTED_BIN": "${APP_BIN:unlisted}"}
	apps["frontend"] = frontend
	manager := New(apps, nil, &mockRuntimeAppManager{
		resolveCommandInfoFunc: func(string, App) (*CommandInfo, error) { return &CommandInfo{Command: executable}, nil },
	})
	if _, _, err := manager.ResolveCommandInfo("root"); err == nil || !strings.Contains(err.Error(), "unlisted") {
		t.Fatalf("accepted an undeclared nested executable binding: %v", err)
	}
}

func TestNodeInstallPreservesPostInstallResolutionFailure(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	unavailable := errors.New("installed runtime could not be resolved")
	manager := New(bindingErrorFixture(t), nil, &mockRuntimeAppManager{
		getCommandInfoFunc: func(context.Context, string, App) (*CommandInfo, error) {
			return &CommandInfo{Command: executable}, nil
		},
		resolveCommandInfoFunc: func(string, App) (*CommandInfo, error) { return nil, unavailable },
	})
	if _, err := manager.GetCommandInfo(context.Background(), "frontend"); !errors.Is(err, unavailable) {
		t.Fatalf("post-install failure was replaced by a usable launcher: %v", err)
	}
}
