package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/ui"
)

func TestInitDependencySets(t *testing.T) {
	for _, tt := range []struct {
		name  string
		root  binmanager.App
		smart bool
	}{
		{"required binary", binmanager.App{Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}, Required: true}, false},
		{"eager link app", binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Archives: nil, Prepare: nil, Node: &binmanager.AppConfigNode{PackageName: "", Version: "", BinPath: "", Runtime: "", LockFile: "", Dependencies: nil}, Links: map[string]string{"config": "config.js"}}, true},
		{"referenced lazy link app", binmanager.App{Required: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Archives: nil, Prepare: nil, Bun: &binmanager.AppConfigBun{PackageName: "", Version: "", BinPath: "", Runtime: "", LockFile: "", Dependencies: nil}, Links: map[string]string{"config": "config.js"}, Lazy: true}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := tt.root
			root.DependsOn = edges("dep")
			cfg := &config.Config{
				Name:           "",
				Version:        "",
				Bundles:        nil,
				ManagedConfigs: nil,
				EjectConfigs:   nil,
				ProjectTypes:   nil,
				Execution:      nil,
				InitCommands:   nil,
				IgnoreRules:    nil,
				SharedStorage:  nil,
				OCI:            nil,
				Parsers:        nil,
				Lsp:            nil, Apps: binmanager.MapOfApps{
					"root":   root,
					"dep":    {Required: false, Description: "", VersionCheck: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Node: &binmanager.AppConfigNode{PackageName: "", Version: "", BinPath: "", LockFile: "", Dependencies: nil, Runtime: "node"}, Lazy: true, DependsOn: edges("leaf")},
					"leaf":   {Required: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}, Lazy: true},
					"unused": {Required: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Node: &binmanager.AppConfigNode{PackageName: "", Version: "", BinPath: "", LockFile: "", Dependencies: nil, Runtime: "unused"}, Lazy: true},
				}, Runtimes: config.MapOfRuntimes{
					"node":   {Mode: "", Managed: nil, System: nil, Bun: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindNode, Node: &config.RuntimeConfigNode{NodeVersion: "", PNPMRuntime: "pnpm"}},
					"pnpm":   {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindPNPM},
					"unused": {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindNode},
					"bun":    {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindBun},
				}, Tools: config.MapOfTools{"tool": {Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", OutputParser: nil, Operations: map[config.OperationType]config.ToolOperation{"lint": {Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "root"}}}},
			}
			want := []string{"leaf", "dep", "root"}
			for _, all := range []bool{false, true} {
				got, err := initInstallAppNames(cfg, all)
				if err != nil {
					t.Fatal(err)
				}
				seedWant := slices.Clone(want)
				if all {
					seedWant = append(seedWant, "unused")
				}
				if !slices.Equal(got, seedWant) {
					t.Fatalf("init set (all=%v) = %v, want %v", all, got, seedWant)
				}
				if tt.smart {
					got, err = smartInitInstallSet(cfg, all)
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(got, want) {
						t.Fatalf("smart set = %v, want %v", got, want)
					}
					mock := &mockCommandInfoGetter{calls: nil, err: nil}
					if err := installSmartInitApps(t.Context(), mock, got); err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(mock.calls, want) {
						t.Fatalf("install order = %v, want %v", mock.calls, want)
					}
				}
			}
			runtimes, err := initRuntimeNames(cfg, false)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(runtimes, "node") || !slices.Contains(runtimes, "pnpm") || slices.Contains(runtimes, "unused") {
				t.Fatalf("init runtimes = %v", runtimes)
			}
		})
	}
}

func TestReportToolsCountsRuntimeDependencies(t *testing.T) {
	previousAll, previousFail := initAll, initFailOnDownloadErr
	initAll, initFailOnDownloadErr = false, false
	t.Cleanup(func() { initAll, initFailOnDownloadErr = previousAll, previousFail })

	for _, tt := range []struct {
		name       string
		cached     bool
		installErr error
		wantCount  int
		wantFailed int
		wantStatus string
		wantFooter string
	}{
		{"downloaded", false, nil, 2, 0, "downloaded", "2 tools"},
		{"cached", true, nil, 2, 0, "cached", "2 tools"},
		{"failed", false, errors.New("fixture install failed"), 1, 1, "failed: fixture install failed", "1 tool"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			t.Setenv("DATAMITSU_OFFLINE", "1")
			host := target.HostTarget()
			apps := binmanager.MapOfApps{
				"root": {
					Lazy:               false,
					Description:        "",
					VersionCheck:       nil,
					OfficialURL:        "",
					OfficialURLDerived: false,
					Bun:                nil,
					Uv:                 nil,
					Node:               nil,
					Jvm:                nil,
					Go:                 nil,
					Shell:              nil,
					Env:                nil,
					RuntimeEnv:         nil,
					Caches:             nil,
					Files:              nil,
					Links:              nil,
					Archives:           nil,
					Prepare:            nil,
					Required:           true,
					DependsOn:          edges("dep"),
					Binary: &binmanager.AppConfigBinary{
						Version: "", Binaries: binmanager.MapOfBinaries{
							syslist.OsType(host.OS): {syslist.ArchType(host.Arch): {string(host.Libc): {
								Auth:       nil,
								BinaryPath: nil,
								ExtractDir: false,
								URL:        "https://example.invalid/root", Hash: strings.Repeat("a", 64), ContentType: binmanager.BinContentTypeBinary,
							}}},
						},
					},
				},
				"dep": {Required: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Uv: &binmanager.AppConfigUV{PackageName: "", Version: "", Runtime: "", LockFile: "", RequiresPython: ""}, Lazy: true},
			}
			manager := &statsRuntimeManager{path: filepath.Join(t.TempDir(), "dep"), installErr: tt.installErr}
			if tt.cached {
				if err := os.WriteFile(manager.path, []byte("cached"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			bm := binmanager.New(apps, nil, manager)
			info, _, err := bm.ResolveCommandInfo("root")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(info.Command), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(info.Command, []byte("cached root"), 0o755); err != nil {
				t.Fatal(err)
			}

			var count, failed int
			output := captureStdout(func() {
				disp := ui.New(term.Plain)
				defer disp.Close()
				count, failed, err = reportTools(t.Context(), disp, bm)
				printInitFooter(disp, initFooterCounts{types: 0, runtimes: 0, bundles: 0, prepared: 0, links: 0, dryRun: false, hadError: false, tools: count, failed: failed, dur: "0s"})
			})
			if err != nil {
				t.Fatal(err)
			}
			if count != tt.wantCount || failed != tt.wantFailed {
				t.Fatalf("tools=%d failed=%d, want tools=%d failed=%d", count, failed, tt.wantCount, tt.wantFailed)
			}
			if !strings.Contains(output, tt.wantFooter) || strings.Contains(strings.ToLower(output), "binary") || strings.Contains(strings.ToLower(output), "binaries") {
				t.Fatalf("runtime dependency mislabeled in summary: %s", output)
			}
			var depRow string
			for line := range strings.SplitSeq(output, "\n") {
				if strings.Contains(line, "dep") {
					depRow = line
				}
			}
			if !strings.Contains(depRow, tt.wantStatus) {
				t.Fatalf("dependency row = %q, want status %q", depRow, tt.wantStatus)
			}
		})
	}
}

type statsRuntimeManager struct {
	path       string
	installErr error
}

func (m *statsRuntimeManager) RuntimeDefinitions() json.RawMessage { return nil }
func (m *statsRuntimeManager) GetCommandInfo(_ context.Context, _ string, _ binmanager.App) (*binmanager.CommandInfo, error) {
	if m.installErr != nil {
		return nil, m.installErr
	}
	if err := os.WriteFile(m.path, []byte("installed"), 0o600); err != nil {
		return nil, err
	}
	return &binmanager.CommandInfo{Args: nil, Env: nil, Artifact: "", ToolCacheDir: "", ToolCacheSlots: nil, RequiredPaths: nil, Type: "uv", Command: m.path}, nil
}

func (m *statsRuntimeManager) ResolveCommandInfo(string, binmanager.App) (*binmanager.CommandInfo, error) {
	return &binmanager.CommandInfo{Args: nil, Env: nil, Artifact: "", ToolCacheDir: "", ToolCacheSlots: nil, RequiredPaths: nil, Type: "uv", Command: m.path}, nil
}

func (m *statsRuntimeManager) ComputeAppPath(string, binmanager.App) (string, error) {
	return filepath.Dir(m.path), nil
}

// edges builds a dependsOn edge list from plain names, mirroring the
// normalized string form of the config.
func edges(names ...string) []binmanager.AppDependency {
	out := make([]binmanager.AppDependency, 0, len(names))
	for _, n := range names {
		out = append(out, binmanager.AppDependency{App: n, Alias: n})
	}
	return out
}
