package cmd

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

func TestScanReferencedApps(t *testing.T) {
	t.Run("extracts app names from tool operations", func(t *testing.T) {
		cfg := &config.Config{
			Tools: config.MapOfTools{
				"golangci-lint": {
					Operations: map[config.OperationType]config.ToolOperation{
						config.OpFix:  {App: "golangci-lint"},
						config.OpLint: {App: "golangci-lint"},
					},
				},
				"eslint": {
					Operations: map[config.OperationType]config.ToolOperation{
						config.OpLint: {App: "eslint-bin"},
					},
				},
			},
		}

		result := scanReferencedApps(cfg)
		sort.Strings(result)

		expected := []string{"eslint-bin", "golangci-lint"}
		if len(result) != len(expected) {
			t.Fatalf("got %v, want %v", result, expected)
		}
		for i, name := range result {
			if name != expected[i] {
				t.Errorf("result[%d] = %q, want %q", i, name, expected[i])
			}
		}
	})

	t.Run("deduplicates app names across multiple tools", func(t *testing.T) {
		cfg := &config.Config{
			Tools: config.MapOfTools{
				"tool1": {
					Operations: map[config.OperationType]config.ToolOperation{
						config.OpFix:  {App: "shared-app"},
						config.OpLint: {App: "shared-app"},
					},
				},
				"tool2": {
					Operations: map[config.OperationType]config.ToolOperation{
						config.OpLint: {App: "shared-app"},
					},
				},
			},
		}

		result := scanReferencedApps(cfg)

		if len(result) != 1 {
			t.Fatalf("expected 1 unique app, got %d: %v", len(result), result)
		}
		if result[0] != "shared-app" {
			t.Errorf("expected shared-app, got %q", result[0])
		}
	})

	t.Run("handles tools without app references", func(t *testing.T) {
		cfg := &config.Config{
			Tools: config.MapOfTools{
				"tool1": {
					Operations: map[config.OperationType]config.ToolOperation{
						config.OpLint: {App: ""},
					},
				},
			},
		}

		result := scanReferencedApps(cfg)

		if len(result) != 0 {
			t.Fatalf("expected 0 apps, got %d: %v", len(result), result)
		}
	})

	t.Run("handles empty config", func(t *testing.T) {
		cfg := &config.Config{}

		result := scanReferencedApps(cfg)

		if len(result) != 0 {
			t.Fatalf("expected 0 apps, got %d: %v", len(result), result)
		}
	})

	t.Run("handles tools with no operations", func(t *testing.T) {
		cfg := &config.Config{
			Tools: config.MapOfTools{
				"empty-tool": {
					Operations: map[config.OperationType]config.ToolOperation{},
				},
			},
		}

		result := scanReferencedApps(cfg)

		if len(result) != 0 {
			t.Fatalf("expected 0 apps, got %d: %v", len(result), result)
		}
	})

	t.Run("returns sorted results", func(t *testing.T) {
		cfg := &config.Config{
			Tools: config.MapOfTools{
				"z-tool": {
					Operations: map[config.OperationType]config.ToolOperation{
						config.OpLint: {App: "z-app"},
					},
				},
				"a-tool": {
					Operations: map[config.OperationType]config.ToolOperation{
						config.OpLint: {App: "a-app"},
					},
				},
				"m-tool": {
					Operations: map[config.OperationType]config.ToolOperation{
						config.OpLint: {App: "m-app"},
					},
				},
			},
		}

		result := scanReferencedApps(cfg)

		for i := 1; i < len(result); i++ {
			if result[i] < result[i-1] {
				t.Errorf("results not sorted: %v", result)
				break
			}
		}
	})
}

func TestFilterAppsForSmartInit(t *testing.T) {
	t.Run("installs only referenced apps with links", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"app-with-links-referenced": {
				Uv:    &binmanager.AppConfigUV{PackageName: "pkg1"},
				Links: map[string]string{"config": "dist/config.js"},
			},
			"app-with-links-not-referenced": {
				Node:  &binmanager.AppConfigNode{PackageName: "pkg2"},
				Links: map[string]string{"other": "dist/other.js"},
			},
			"app-no-links-referenced": {
				Uv: &binmanager.AppConfigUV{PackageName: "pkg3"},
			},
			"binary-app-with-links": {
				Binary: &binmanager.AppConfigBinary{},
				Links:  map[string]string{"bin-config": "config.js"},
			},
			"shell-app-with-links": {
				Shell: &binmanager.AppConfigShell{Name: "sh"},
				Links: map[string]string{"sh-config": "config.js"},
			},
		}

		referencedApps := []string{"app-with-links-referenced", "app-no-links-referenced", "binary-app-with-links", "shell-app-with-links"}

		result := filterAppsForSmartInit(apps, referencedApps)
		sort.Strings(result)

		// Should only include runtime-managed apps (not binary/shell) that are referenced AND have links
		expected := []string{"app-with-links-referenced"}
		if len(result) != len(expected) {
			t.Fatalf("got %v, want %v", result, expected)
		}
		for i, name := range result {
			if name != expected[i] {
				t.Errorf("result[%d] = %q, want %q", i, name, expected[i])
			}
		}
	})

	t.Run("returns empty when no apps match criteria", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"app1": {
				Binary: &binmanager.AppConfigBinary{},
				Links:  map[string]string{"config": "config.js"},
			},
		}

		result := filterAppsForSmartInit(apps, []string{"app1"})

		if len(result) != 0 {
			t.Fatalf("expected 0 apps, got %d: %v", len(result), result)
		}
	})

	t.Run("returns empty when referenced apps have no links", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"app1": {
				Uv: &binmanager.AppConfigUV{PackageName: "pkg1"},
			},
		}

		result := filterAppsForSmartInit(apps, []string{"app1"})

		if len(result) != 0 {
			t.Fatalf("expected 0 apps, got %d: %v", len(result), result)
		}
	})

	t.Run("handles empty inputs", func(t *testing.T) {
		result := filterAppsForSmartInit(binmanager.MapOfApps{}, nil)
		if len(result) != 0 {
			t.Fatalf("expected 0 apps, got %d: %v", len(result), result)
		}
	})
}

func TestSmartInitIntegration(t *testing.T) {
	t.Run("config with 5 apps and 3 with links but only 2 referenced by tools", func(t *testing.T) {
		cfg := &config.Config{
			Apps: binmanager.MapOfApps{
				"app-a": {
					Node:  &binmanager.AppConfigNode{PackageName: "pkg-a"},
					Links: map[string]string{"config-a": "dist/a.js"},
				},
				"app-b": {
					Uv:    &binmanager.AppConfigUV{PackageName: "pkg-b"},
					Links: map[string]string{"config-b": "dist/b.py"},
				},
				"app-c": {
					Node:  &binmanager.AppConfigNode{PackageName: "pkg-c"},
					Links: map[string]string{"config-c": "dist/c.js"},
				},
				"app-d": {
					Binary: &binmanager.AppConfigBinary{},
				},
				"app-e": {
					Uv: &binmanager.AppConfigUV{PackageName: "pkg-e"},
				},
			},
			Tools: config.MapOfTools{
				"tool-1": {
					Operations: map[config.OperationType]config.ToolOperation{
						config.OpLint: {App: "app-a"},
					},
				},
				"tool-2": {
					Operations: map[config.OperationType]config.ToolOperation{
						config.OpFix: {App: "app-c"},
					},
				},
				"tool-3": {
					Operations: map[config.OperationType]config.ToolOperation{
						config.OpLint: {App: "app-d"},
					},
				},
			},
		}

		// Step 1: scan referenced apps from tools
		referenced := scanReferencedApps(cfg)
		sort.Strings(referenced)

		expectedReferenced := []string{"app-a", "app-c", "app-d"}
		if len(referenced) != len(expectedReferenced) {
			t.Fatalf("referenced apps = %v, want %v", referenced, expectedReferenced)
		}
		for i, name := range referenced {
			if name != expectedReferenced[i] {
				t.Errorf("referenced[%d] = %q, want %q", i, name, expectedReferenced[i])
			}
		}

		// Step 2: filter to runtime apps with links
		toInstall := filterAppsForSmartInit(cfg.Apps, referenced)
		sort.Strings(toInstall)

		// Only app-a (node with links, referenced) and app-c (node with links, referenced) should be installed
		// app-b has links but not referenced, app-d is binary, app-e has no links
		expectedInstall := []string{"app-a", "app-c"}
		if len(toInstall) != len(expectedInstall) {
			t.Fatalf("apps to install = %v, want %v", toInstall, expectedInstall)
		}
		for i, name := range toInstall {
			if name != expectedInstall[i] {
				t.Errorf("toInstall[%d] = %q, want %q", i, name, expectedInstall[i])
			}
		}
	})
}

type mockCommandInfoGetter struct {
	calls []string
	err   error
}

func (m *mockCommandInfoGetter) GetCommandInfo(_ context.Context, appName string) (*binmanager.CommandInfo, error) {
	m.calls = append(m.calls, appName)
	if m.err != nil {
		return nil, m.err
	}
	return &binmanager.CommandInfo{Type: "uv", Command: "/fake/bin"}, nil
}

func TestInstallSmartInitApps(t *testing.T) {
	t.Run("installs only filtered apps", func(t *testing.T) {
		mock := &mockCommandInfoGetter{}
		appsToInstall := []string{"app-b", "app-a"}

		err := installSmartInitApps(context.Background(), mock, appsToInstall)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		sort.Strings(mock.calls)
		expected := []string{"app-a", "app-b"}
		if len(mock.calls) != len(expected) {
			t.Fatalf("calls = %v, want %v", mock.calls, expected)
		}
		for i, name := range mock.calls {
			if name != expected[i] {
				t.Errorf("calls[%d] = %q, want %q", i, name, expected[i])
			}
		}
	})

	t.Run("returns error on install failure", func(t *testing.T) {
		mock := &mockCommandInfoGetter{err: errors.New("install failed")}

		err := installSmartInitApps(context.Background(), mock, []string{"broken-app"})
		if err == nil {
			t.Fatal("expected error")
		}
		if got := err.Error(); got != "failed to install broken-app: install failed" {
			t.Errorf("error = %q, want 'failed to install broken-app: install failed'", got)
		}
	})

	t.Run("no-op when list is empty", func(t *testing.T) {
		mock := &mockCommandInfoGetter{}

		err := installSmartInitApps(context.Background(), mock, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(mock.calls) != 0 {
			t.Errorf("expected no calls, got %v", mock.calls)
		}
	})
}

func TestFilterAppsForSmartInit_ExcludesJVMApps(t *testing.T) {
	apps := binmanager.MapOfApps{
		"jvm-app": {
			Jvm:   &binmanager.AppConfigJVM{Version: "7.0"},
			Links: map[string]string{"config": "dist/config.js"},
		},
	}

	result := filterAppsForSmartInit(apps, []string{"jvm-app"})

	if len(result) != 0 {
		t.Fatalf("expected JVM apps to be excluded, got %v", result)
	}
}

func TestInstallRuntimeAppsWithLinksUsesSmartInit(t *testing.T) {
	cfg := &config.Config{
		Apps: binmanager.MapOfApps{
			"app-a": {
				Node:  &binmanager.AppConfigNode{PackageName: "pkg-a"},
				Links: map[string]string{"config-a": "dist/a.js"},
			},
			"app-b": {
				Uv:    &binmanager.AppConfigUV{PackageName: "pkg-b"},
				Links: map[string]string{"config-b": "dist/b.py"},
			},
		},
		Tools: config.MapOfTools{
			"tool-1": {
				Operations: map[config.OperationType]config.ToolOperation{
					config.OpLint: {App: "app-a"},
				},
			},
		},
	}

	// scanReferencedApps should find only app-a
	referenced := scanReferencedApps(cfg)
	if len(referenced) != 1 || referenced[0] != "app-a" {
		t.Fatalf("expected [app-a], got %v", referenced)
	}

	// filterAppsForSmartInit should include app-a (runtime with links, referenced) but not app-b (not referenced)
	toInstall := filterAppsForSmartInit(cfg.Apps, referenced)
	if len(toInstall) != 1 || toInstall[0] != "app-a" {
		t.Fatalf("expected [app-a], got %v", toInstall)
	}
}

// fakeRootResolver reports an app as installed iff its name is in installed.
type fakeRootResolver struct{ installed map[string]bool }

func (f fakeRootResolver) GetInstallRoot(name string) (string, error) {
	if f.installed[name] {
		return "/fake/store/" + name, nil
	}
	return "", errors.New("not installed")
}

func TestInstalledAppsWithLinks(t *testing.T) {
	apps := binmanager.MapOfApps{
		"installed-link-app": {
			Node:  &binmanager.AppConfigNode{PackageName: "pkg1"},
			Links: map[string]string{"config": "dist/config.js"},
		},
		"deferred-link-app": { // has links but NOT installed (e.g. slidev)
			Node:  &binmanager.AppConfigNode{PackageName: "pkg2"},
			Links: map[string]string{"theme": "node_modules/@scope/theme"},
		},
		"installed-no-links": {
			Node: &binmanager.AppConfigNode{PackageName: "pkg3"},
		},
	}
	resolver := fakeRootResolver{installed: map[string]bool{
		"installed-link-app": true,
		"installed-no-links": true,
		// deferred-link-app intentionally absent → treated as not installed
	}}

	result := installedAppsWithLinks(apps, resolver)

	if len(result) != 1 {
		t.Fatalf("expected 1 app, got %d: %v", len(result), result)
	}
	if _, ok := result["installed-link-app"]; !ok {
		t.Errorf("expected installed-link-app to be included, got %v", result)
	}
	if _, ok := result["deferred-link-app"]; ok {
		t.Error("deferred (uninstalled) link-app must be excluded from link materialization")
	}
}

func TestMergeUnique(t *testing.T) {
	t.Run("merges and deduplicates", func(t *testing.T) {
		result := mergeUnique([]string{"a", "b"}, []string{"b", "c"})
		expected := []string{"a", "b", "c"}
		if len(result) != len(expected) {
			t.Fatalf("got %v, want %v", result, expected)
		}
		for i, s := range result {
			if s != expected[i] {
				t.Errorf("result[%d] = %q, want %q", i, s, expected[i])
			}
		}
	})

	t.Run("handles empty inputs", func(t *testing.T) {
		result := mergeUnique(nil, nil)
		if len(result) != 0 {
			t.Fatalf("expected empty, got %v", result)
		}
	})
}

func TestEagerRuntimeLinkApps(t *testing.T) {
	apps := binmanager.MapOfApps{
		"node-link-eager": { // unreferenced but eager (e.g. commitlint)
			Node:  &binmanager.AppConfigNode{PackageName: "pkg1"},
			Links: map[string]string{"config": "dist/config.js"},
		},
		"uv-link-eager": {
			Uv:    &binmanager.AppConfigUV{PackageName: "pkg2"},
			Links: map[string]string{"other": "dist/other.py"},
		},
		"node-link-lazy": { // deferred (e.g. slidev)
			Node:  &binmanager.AppConfigNode{PackageName: "pkg3"},
			Links: map[string]string{"theme": "node_modules/@scope/theme"},
			Lazy:  true,
		},
		"node-no-links": {
			Node: &binmanager.AppConfigNode{PackageName: "pkg4"},
		},
		"binary-with-links": {
			Binary: &binmanager.AppConfigBinary{},
			Links:  map[string]string{"bin": "config.js"},
		},
	}

	result := eagerRuntimeLinkApps(apps)

	expected := []string{"node-link-eager", "uv-link-eager"}
	if len(result) != len(expected) {
		t.Fatalf("got %v, want %v", result, expected)
	}
	for i, name := range result {
		if name != expected[i] {
			t.Errorf("result[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestSmartInitInstallSet(t *testing.T) {
	// The init install set = link-apps referenced by a tool + every non-lazy
	// runtime link-app. A Lazy app (slidev) is the only link-app excluded; an
	// unreferenced non-lazy link-app (commitlint, used by the commit-msg hook)
	// stays eager.
	cfg := &config.Config{
		Apps: binmanager.MapOfApps{
			"tool-referenced": { // referenced by a tool
				Node:  &binmanager.AppConfigNode{PackageName: "pkg-a"},
				Links: map[string]string{"config-a": "dist/a.js"},
			},
			"commitlint-like": { // unreferenced, but eager (hook-consumed link)
				Node:  &binmanager.AppConfigNode{PackageName: "pkg-b"},
				Links: map[string]string{"commitlint.config.js": "dist/b.js"},
			},
			"slidev-like": { // unreferenced and Lazy — the only one deferred
				Node:  &binmanager.AppConfigNode{PackageName: "pkg-c"},
				Links: map[string]string{"theme": "node_modules/@scope/theme"},
				Lazy:  true,
			},
			"no-links": {
				Node: &binmanager.AppConfigNode{PackageName: "pkg-d"},
			},
		},
		Tools: config.MapOfTools{
			"tool-1": {
				Operations: map[config.OperationType]config.ToolOperation{
					config.OpLint: {App: "tool-referenced"},
				},
			},
		},
	}

	// Mirror installRuntimeAppsWithLinks's smart-init set computation.
	referenced := scanReferencedApps(cfg)
	installSet := mergeUnique(
		filterAppsForSmartInit(cfg.Apps, referenced),
		eagerRuntimeLinkApps(cfg.Apps),
	)

	expected := []string{"commitlint-like", "tool-referenced"}
	if len(installSet) != len(expected) {
		t.Fatalf("install set = %v, want %v", installSet, expected)
	}
	for i, name := range installSet {
		if name != expected[i] {
			t.Errorf("installSet[%d] = %q, want %q", i, name, expected[i])
		}
	}
}
