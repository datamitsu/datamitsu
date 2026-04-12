package cmd

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"fmt"
	"sort"
	"testing"
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
				Fnm:   &binmanager.AppConfigFNM{PackageName: "pkg2"},
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
					Fnm:   &binmanager.AppConfigFNM{PackageName: "pkg-a"},
					Links: map[string]string{"config-a": "dist/a.js"},
				},
				"app-b": {
					Uv:    &binmanager.AppConfigUV{PackageName: "pkg-b"},
					Links: map[string]string{"config-b": "dist/b.py"},
				},
				"app-c": {
					Fnm:   &binmanager.AppConfigFNM{PackageName: "pkg-c"},
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

		// Only app-a (fnm with links, referenced) and app-c (fnm with links, referenced) should be installed
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

func (m *mockCommandInfoGetter) GetCommandInfo(appName string) (*binmanager.CommandInfo, error) {
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

		err := installSmartInitApps(mock, appsToInstall)
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
		mock := &mockCommandInfoGetter{err: fmt.Errorf("install failed")}

		err := installSmartInitApps(mock, []string{"broken-app"})
		if err == nil {
			t.Fatal("expected error")
		}
		if got := err.Error(); got != "failed to install broken-app: install failed" {
			t.Errorf("error = %q, want 'failed to install broken-app: install failed'", got)
		}
	})

	t.Run("no-op when list is empty", func(t *testing.T) {
		mock := &mockCommandInfoGetter{}

		err := installSmartInitApps(mock, nil)
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
				Fnm:   &binmanager.AppConfigFNM{PackageName: "pkg-a"},
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

func TestAllRuntimeAppsWithLinks(t *testing.T) {
	t.Run("returns only runtime-managed apps with links", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"fnm-with-links": {
				Fnm:   &binmanager.AppConfigFNM{PackageName: "pkg1"},
				Links: map[string]string{"config": "dist/config.js"},
			},
			"uv-with-links": {
				Uv:    &binmanager.AppConfigUV{PackageName: "pkg2"},
				Links: map[string]string{"other": "dist/other.py"},
			},
			"fnm-no-links": {
				Fnm: &binmanager.AppConfigFNM{PackageName: "pkg3"},
			},
			"binary-with-links": {
				Binary: &binmanager.AppConfigBinary{},
				Links:  map[string]string{"bin": "config.js"},
			},
			"jvm-with-links": {
				Jvm:   &binmanager.AppConfigJVM{Version: "1.0"},
				Links: map[string]string{"jvm": "config.js"},
			},
		}

		result := allRuntimeAppsWithLinks(apps)

		expected := []string{"fnm-with-links", "uv-with-links"}
		if len(result) != len(expected) {
			t.Fatalf("got %v, want %v", result, expected)
		}
		for i, name := range result {
			if name != expected[i] {
				t.Errorf("result[%d] = %q, want %q", i, name, expected[i])
			}
		}
	})

	t.Run("returns empty when no apps have links", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"app1": {Fnm: &binmanager.AppConfigFNM{PackageName: "pkg1"}},
		}
		result := allRuntimeAppsWithLinks(apps)
		if len(result) != 0 {
			t.Fatalf("expected empty, got %v", result)
		}
	})
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

func TestSmartInitIncludesAllAppsWithLinks(t *testing.T) {
	// Simulates the case where an app has Links but is not referenced by any
	// tool operation (e.g., only referenced via tools.Config.linkPath in ConfigInit).
	cfg := &config.Config{
		Apps: binmanager.MapOfApps{
			"tool-referenced": {
				Fnm:   &binmanager.AppConfigFNM{PackageName: "pkg-a"},
				Links: map[string]string{"config-a": "dist/a.js"},
			},
			"linkpath-only": {
				Uv:    &binmanager.AppConfigUV{PackageName: "pkg-b"},
				Links: map[string]string{"config-b": "dist/b.py"},
			},
			"no-links": {
				Fnm: &binmanager.AppConfigFNM{PackageName: "pkg-c"},
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

	// scanReferencedApps only finds tool-referenced
	referenced := scanReferencedApps(cfg)
	filtered := filterAppsForSmartInit(cfg.Apps, referenced)

	// allRuntimeAppsWithLinks finds both apps with Links
	linkApps := allRuntimeAppsWithLinks(cfg.Apps)

	// mergeUnique combines both sets
	result := mergeUnique(filtered, linkApps)

	// Both apps with Links should be included
	expected := []string{"linkpath-only", "tool-referenced"}
	if len(result) != len(expected) {
		t.Fatalf("got %v, want %v", result, expected)
	}
	for i, name := range result {
		if name != expected[i] {
			t.Errorf("result[%d] = %q, want %q", i, name, expected[i])
		}
	}
}
