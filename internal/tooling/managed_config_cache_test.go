package tooling

import (
	"os"
	"path/filepath"
	"slices"
	zerovalue_sync "sync"
	zerovalue_atomic "sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	zerovalue_toolenv "github.com/datamitsu/datamitsu/internal/toolenv"

	"go.uber.org/zap"
)

func managedConfigTask(root string) Task {
	return Task{
		perFileCache: "",
		inherited:    zerovalue_toolenv.Inherited{},
		ID:           "",
		Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
		Operation:    "",
		Files:        nil,
		UnitDir:      "",
		UnitMembers:  nil,
		UnitGuards:   nil,
		Coverage:     "",
		ToolName:     "yamlfmt",
		OpConfig: config.ToolOperation{
			App:               "",
			Scope:             "",
			Arity:             "",
			Granularity:       "",
			Globs:             nil,
			ExcludeGlobs:      nil,
			Priority:          0,
			Cache:             nil,
			InvalidateOn:      nil,
			Env:               nil,
			InheritEnv:        nil,
			FailOn:            "",
			Input:             "",
			LSP:               nil,
			Output:            "",
			Args:              []string{"-conf={root}/.datamitsu/configs/.yamlfmt.yaml", "{files}"},
			ManagedConfigRefs: []config.ManagedConfigOpRef{{Key: ".yamlfmt.yaml", Path: "{root}/.datamitsu/configs/.yamlfmt.yaml"}},
		},
		ProjectPath: root,
	}
}

func writeManagedConfig(t *testing.T, root, content string) string {
	t.Helper()
	path := filepath.Join(root, ".datamitsu", "configs", ".yamlfmt.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPerFileCacheToolFollowsManagedConfigContent(t *testing.T) {
	root := t.TempDir()
	e := &Executor{dryRun: false, failFast: false, appManager: nil, resultCallback: nil, taskStartCallback: nil, fileProgressCallback: nil, cache: nil, parser: nil, parserModules: nil, gate: nil, envObserver: nil, stepCallback: nil, steps: 0, capturePatches: false, limits: ParseLimits{InputBytes: 0, Findings: 0}, cmdInfos: zerovalue_atomic.Pointer[commandInfoMemo]{}, rootPath: root}
	task := managedConfigTask(root)

	missing := e.perFileCacheTool(task)
	writeManagedConfig(t, root, "indent: 2\n")
	first := e.perFileCacheTool(task)
	if first == missing {
		t.Error("creating the config did not change the cache name")
	}
	if again := e.perFileCacheTool(task); again != first {
		t.Errorf("unchanged config gave %q then %q", first, again)
	}
	writeManagedConfig(t, root, "indent: 4\n")
	if changed := e.perFileCacheTool(task); changed == first {
		t.Error("editing the config left the cache name unchanged")
	}

	task.OpConfig.ManagedConfigRefs = nil
	if got := e.perFileCacheTool(task); got != "yamlfmt" {
		t.Errorf("an operation without managed configs = %q, want the bare tool name", got)
	}
}

func TestUnitGuardsIncludeManagedConfigs(t *testing.T) {
	root := t.TempDir()
	path := writeManagedConfig(t, root, "indent: 2\n")
	p := &Planner{cwdPath: "", detectedTypes: nil, tools: nil, projectTypesConfig: nil, platformChecker: nil, extraIgnoreRules: nil, cachedFiles: nil, cachedRel: nil, cachedProjects: nil, cacheInitialized: false, seedFiles: nil, globCacheMu: zerovalue_sync.Mutex{}, globCache: nil, unitCacheMu: zerovalue_sync.Mutex{}, memberCache: nil, guardCache: nil, ignoreMatcher: nil, execution: nil, widenOverride: "", timings: nil, rootPath: root}

	guards := p.unitGuards(managedConfigTask(root), root)
	if !slices.Contains(guards, path) {
		t.Errorf("guards %v miss the managed config named inside -conf=", guards)
	}
}

func TestAConfigEditedDuringTheRunIsNotCached(t *testing.T) {
	for _, edited := range []bool{false, true} {
		root := t.TempDir()
		writeManagedConfig(t, root, "indent: 2\n")
		file := filepath.Join(root, "a.yaml")
		if err := os.WriteFile(file, []byte("a: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := cache.NewCache(t.TempDir(), root, config.Config{Name: "", Version: "", Apps: nil, Bundles: nil, AI: nil, Runtimes: nil, ManagedConfigs: nil, EjectConfigs: nil, ProjectTypes: nil, Tools: nil, Execution: nil, InitCommands: nil, IgnoreRules: nil, SharedStorage: nil, OCI: nil, Parsers: nil, Lsp: nil}, nil, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		e := &Executor{dryRun: false, failFast: false, appManager: nil, resultCallback: nil, taskStartCallback: nil, fileProgressCallback: nil, parser: nil, parserModules: nil, gate: nil, envObserver: nil, stepCallback: nil, steps: 0, capturePatches: false, limits: ParseLimits{InputBytes: 0, Findings: 0}, cmdInfos: zerovalue_atomic.Pointer[commandInfoMemo]{}, rootPath: root, cache: c}
		task := managedConfigTask(root)
		task.Operation = config.OpLint
		task.OpConfig.Granularity = config.GranularityFile
		task.perFileCache = e.perFileCacheTool(task)

		task.Files = []string{file}
		_, seen := e.filterFilesByCache(task)
		if edited {
			writeManagedConfig(t, root, "indent: 4\n")
		}
		e.updateCacheAfterSuccess(task, []string{file}, seen)

		cached := !c.Check(file, e.perFileCacheTool(task), cache.OperationLint, observe(file), true)
		if cached == edited {
			t.Errorf("edited=%v: result cached under the current config = %v", edited, cached)
		}
	}
}
