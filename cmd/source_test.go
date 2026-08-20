package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/shellquote"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

// hostilePlan is a plan whose paths contain everything a shell mis-parses:
// a space, a single quote, a glob character and a dollar sign.
func hostilePlan() sourcefarm.Plan {
	return sourcefarm.Plan{
		Root:    "/tmp/my repo's [work]/$dir",
		FarmDir: "/tmp/my repo's [work]/$dir/farm bin",
	}
}

// realHostilePlan is hostilePlan with directories that actually exist, for the
// tests that run the emitted code in a real shell. fish_add_path silently skips
// a directory that is not there, so the shell tests must bake against a real
// farm to test anything at all.
func realHostilePlan(t *testing.T) sourcefarm.Plan {
	t.Helper()
	root := filepath.Join(t.TempDir(), "my repo's [work] $dir")
	farm := filepath.Join(root, "farm bin")
	if err := os.MkdirAll(farm, 0o700); err != nil {
		t.Fatalf("create farm directory: %v", err)
	}
	return sourcefarm.Plan{Root: root, FarmDir: farm}
}

// writeFarmEntries creates a file per entry name in farm. loadSourcePlan checks
// that the farm still holds one, so a manifest listing entries needs them on
// disk to be served at all.
func writeFarmEntries(t *testing.T, farm string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(farm, name), []byte("shim"), 0o700); err != nil {
			t.Fatalf("write farm entry %q: %v", name, err)
		}
	}
}

func simplePlan() sourcefarm.Plan {
	return sourcefarm.Plan{Root: "/repo", FarmDir: "/cache/projects/abc/bin"}
}

// gitRootTarget is the target a plain `datamitsu source` inside root resolves
// to, built without going through git so a test can name the root directly.
func gitRootTarget(t *testing.T, root string) sourceTarget {
	t.Helper()
	farmDir, err := env.GetProjectBinPath(root)
	if err != nil {
		t.Fatalf("GetProjectBinPath() error = %v", err)
	}
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		t.Fatalf("GetProjectManifestPath() error = %v", err)
	}
	return sourceTarget{
		Origin:       sourcefarm.OriginGitRoot,
		Root:         root,
		FarmDir:      farmDir,
		ManifestPath: manifestPath,
	}
}

// configTarget is the target `datamitsu source --config <chain>` resolves to.
func configTarget(t *testing.T, configPaths ...string) sourceTarget {
	t.Helper()
	t.Cleanup(func() { ConfigPaths = nil })
	ConfigPaths = configPaths
	target, err := explicitConfigTarget()
	if err != nil {
		t.Fatalf("explicitConfigTarget() error = %v", err)
	}
	return target
}

// activationOf is what runSource hands a renderer for a git-root farm.
func activationOf(plan sourcefarm.Plan) sourceActivation {
	return activationFor(sourceTarget{Origin: sourcefarm.OriginGitRoot, Root: plan.Root}, plan)
}

// countPathAssignments counts the lines that assign PATH itself. The scratch
// variable the renderer edits does not count: the invariant is that PATH takes
// exactly one new value, so a half-rendered activation can never be observed.
func countPathAssignments(script string) int {
	n := 0
	for line := range strings.SplitSeq(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "PATH=") {
			n++
		}
	}
	return n
}

func TestRenderBashSinglePathMutation(t *testing.T) {
	for _, plan := range []sourcefarm.Plan{simplePlan(), hostilePlan()} {
		got := renderBash(activationOf(plan))
		if n := countPathAssignments(got); n != 1 {
			t.Errorf("renderBash assigned PATH %d times, want exactly 1:\n%s", n, got)
		}
		if !strings.Contains(got, "hash -r") {
			t.Errorf("renderBash did not flush the command hash:\n%s", got)
		}
		if !strings.HasSuffix(got, "\n") {
			t.Errorf("renderBash output does not end with a newline:\n%q", got)
		}
		if !strings.Contains(got, "export "+env.SourceRootVarName()+"=") {
			t.Errorf("renderBash did not export %s:\n%s", env.SourceRootVarName(), got)
		}
		if !strings.Contains(got, "export "+env.SourceFarmVarName()+"=") {
			t.Errorf("renderBash did not export %s:\n%s", env.SourceFarmVarName(), got)
		}
		// Every emitted path goes through shellquote rather than being pasted
		// in with quotes around it.
		if !strings.Contains(got, "="+shellquote.Bash(plan.FarmDir)+"\n") {
			t.Errorf("renderBash did not emit the farm through shellquote:\n%s", got)
		}
	}
}

func TestRenderFishUsesMove(t *testing.T) {
	for _, plan := range []sourcefarm.Plan{simplePlan(), hostilePlan()} {
		got := renderFish(activationOf(plan))
		if !strings.Contains(got, "fish_add_path --global --move --path ") {
			// --move is what makes re-activation actually re-order PATH instead
			// of silently doing nothing when the farm is already present.
			t.Errorf("renderFish did not use --move:\n%s", got)
		}
		if strings.Contains(got, "hash -r") {
			t.Errorf("renderFish emitted a bash builtin:\n%s", got)
		}
		if !strings.Contains(got, "set -gx "+env.SourceRootVarName()+" ") {
			t.Errorf("renderFish did not export %s:\n%s", env.SourceRootVarName(), got)
		}
		if !strings.Contains(got, "set -gx "+env.SourceFarmVarName()+" ") {
			t.Errorf("renderFish did not export %s:\n%s", env.SourceFarmVarName(), got)
		}
	}
}

// TestRenderersAreDeterministic is what lets the CLI goldens exist at all.
func TestRenderersAreDeterministic(t *testing.T) {
	renderers := map[string]func(sourceActivation) string{"bash": renderBash, "fish": renderFish}
	for name, render := range renderers {
		t.Run(name, func(t *testing.T) {
			plan := hostilePlan()
			a := activationOf(plan)
			if first, second := render(a), render(a); first != second {
				t.Errorf("%s renderer is not byte-stable:\n%q\n%q", name, first, second)
			}
		})
	}
}

// TestRenderersArePure pins that a renderer is a function of its Plan: no config
// load, no filesystem access, no git. A plan describing directories that do not
// exist renders exactly like one that does.
func TestRenderersArePure(t *testing.T) {
	missing := sourcefarm.Plan{Root: "/no/such/root", FarmDir: "/no/such/root/bin"}
	for name, render := range map[string]func(sourceActivation) string{"bash": renderBash, "fish": renderFish} {
		t.Run(name, func(t *testing.T) {
			got := render(activationOf(missing))
			if !strings.Contains(got, "/no/such/root") {
				t.Errorf("%s renderer dropped the plan's paths:\n%s", name, got)
			}
		})
	}
}

// TestRenderBashRunsInRealBash is the only check that the generated code is
// valid rather than merely plausible. It runs the activation in a real bash
// against a hostile farm path and reads back the PATH it produced.
func TestRenderBashRunsInRealBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed; the generated activation code is unverified on this machine")
	}

	plan := realHostilePlan(t)
	script := renderBash(activationOf(plan)) + "printf '%s' \"$PATH\"\n"

	run := func(t *testing.T, startPath string) string {
		t.Helper()
		cmd := exec.Command(bash, "-c", script)
		cmd.Env = append(cmd.Environ(), "PATH="+startPath)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("bash rejected the activation code: %v\nstderr: %s\nscript:\n%s", err, stderr.String(), script)
		}
		return stdout.String()
	}

	got := run(t, "/usr/bin:/bin")
	parts := strings.Split(got, ":")
	if parts[0] != plan.FarmDir {
		t.Fatalf("farm is not first on PATH: %q", got)
	}
	if len(parts) != 3 {
		t.Fatalf("activation did not preserve the rest of PATH: %q", got)
	}

	// Idempotence: activating a shell that already has the farm on PATH must
	// not add a second copy.
	twice := run(t, got)
	if twice != got {
		t.Fatalf("re-activation changed PATH:\nfirst:  %q\nsecond: %q", got, twice)
	}
	if strings.Count(twice, plan.FarmDir) != 1 {
		t.Fatalf("re-activation duplicated the farm entry: %q", twice)
	}
}

// TestRenderBashRemovesAnExistingFarmEntry proves the removal step is real: a
// farm sitting in the middle of PATH moves to the front rather than being
// duplicated.
func TestRenderBashRemovesAnExistingFarmEntry(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed; the generated activation code is unverified on this machine")
	}

	plan := realHostilePlan(t)
	script := renderBash(activationOf(plan)) + "printf '%s' \"$PATH\"\n"
	cmd := exec.Command(bash, "-c", script)
	cmd.Env = append(cmd.Environ(), "PATH=/usr/bin:"+plan.FarmDir+":/bin")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash rejected the activation code: %v", err)
	}
	want := plan.FarmDir + ":/usr/bin:/bin"
	if string(out) != want {
		t.Fatalf("PATH = %q, want %q", out, want)
	}
}

// TestRenderFishRunsInRealFish exercises the fish renderer in fish itself,
// which is the only thing that can tell fish_add_path's flags apart from a
// plausible-looking string.
func TestRenderFishRunsInRealFish(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed; the generated activation code is unverified on this machine")
	}

	plan := realHostilePlan(t)
	script := renderFish(activationOf(plan)) + "printf '%s' \"$PATH[1]\"\n"
	cmd := exec.Command(fish, "--no-config", "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("fish rejected the activation code: %v\nstderr: %s\nscript:\n%s", err, stderr.String(), script)
	}
	if stdout.String() != plan.FarmDir {
		t.Fatalf("farm is not first on fish's PATH: got %q, want %q", stdout.String(), plan.FarmDir)
	}
}

// TestWarningsNeverReachStdout is the guard behind "the output is always safe to
// eval": warnings describe the farm, and describing it must not put a word of
// prose into the stream the shell is about to execute.
func TestWarningsNeverReachStdout(t *testing.T) {
	plan := sourcefarm.Plan{
		Root:    "/repo",
		FarmDir: "/cache/bin",
		Entries: []sourcefarm.Entry{
			{Name: "tofu", Strategy: sourcefarm.StrategySymlink, Installed: true},
			{Name: "tflint", Strategy: sourcefarm.StrategyShim, Installed: false},
		},
		Shadowed: []sourcefarm.Shadow{{Name: "tofu", Path: "/usr/local/bin/tofu"}},
	}

	var stderr bytes.Buffer
	warnSourceFarm(&stderr, plan)
	warnings := stderr.String()

	for _, want := range []string{"tflint", "not downloaded yet", "/usr/local/bin/tofu", "shadowing"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("stderr is missing %q:\n%s", want, warnings)
		}
	}
	// The shadow warning must name both sides on one line: the name that changed
	// meaning and the binary it now hides. Either half alone is unactionable.
	if !strings.Contains(warnings, "tofu now runs this project's version, shadowing /usr/local/bin/tofu") {
		t.Errorf("shadow warning is not the full line:\n%s", warnings)
	}

	for name, render := range map[string]func(sourceActivation) string{"bash": renderBash, "fish": renderFish} {
		stdout := render(activationOf(plan))
		for _, forbidden := range []string{"not downloaded yet", "shadowing", "tflint"} {
			if strings.Contains(stdout, forbidden) {
				t.Errorf("%s activation code contains warning text %q:\n%s", name, forbidden, stdout)
			}
		}
	}
}

// TestWarnSourceFarmSilentWhenClean pins that a farm which is fully installed
// and shadows nothing says nothing at all — activation in a shell rc file must
// not print on every new terminal.
func TestWarnSourceFarmSilentWhenClean(t *testing.T) {
	plan := sourcefarm.Plan{
		Entries: []sourcefarm.Entry{{Name: "tofu", Installed: true}},
	}
	var stderr bytes.Buffer
	warnSourceFarm(&stderr, plan)
	if stderr.Len() != 0 {
		t.Fatalf("a clean farm printed a warning: %q", stderr.String())
	}
}

// TestConfigChainFilesRecordsOnlyFilePaths pins what the farm's watch set is
// built from: the on-disk files of the config chain, in chain order. The
// embedded default config and remote configs have no path and must not appear —
// a watch entry for "" would stat the working directory on every tool
// invocation.
func TestConfigChainFilesRecordsOnlyFilePaths(t *testing.T) {
	// configChainFiles is a package global; leaving it set would leak these
	// fake paths into any later test in this package that reads it.
	t.Cleanup(func() { setConfigChainFiles(nil) })

	setConfigChainFiles([]configSource{
		{name: "default", isDefault: true},
		{name: "/repo/base.config.ts", path: "/repo/base.config.ts"},
		{name: "https://example.com/c.js", content: "…", isRemote: true},
		{name: "auto", path: "/repo/datamitsu.config.ts"},
	})

	got := ConfigChainFiles()
	want := []string{"/repo/base.config.ts", "/repo/datamitsu.config.ts"}
	if len(got) != len(want) {
		t.Fatalf("ConfigChainFiles() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ConfigChainFiles() = %v, want %v", got, want)
		}
	}

	// The accessor returns a copy: a caller mutating the slice must not corrupt
	// the next load's watch set.
	got[0] = "/tampered"
	if again := ConfigChainFiles(); again[0] != want[0] {
		t.Fatalf("ConfigChainFiles() handed out its own slice: %v", again)
	}
}

// TestFarmOnDiskRequiresBothHalves pins what "a previous farm is still usable"
// means. Only when the farm directory and a decodable manifest are both there
// may a failed bake be downgraded to a warning; anything less and activation has
// nothing to fall back on.
func TestFarmOnDiskRequiresBothHalves(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)

	root := t.TempDir()
	farm, err := env.GetProjectBinPath(root)
	if err != nil {
		t.Fatalf("GetProjectBinPath() error = %v", err)
	}
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		t.Fatalf("GetProjectManifestPath() error = %v", err)
	}
	plan := sourcefarm.Plan{Root: root, FarmDir: farm}

	if farmOnDisk(plan, gitRootTarget(t, root)) {
		t.Error("a root that was never baked reported a usable farm")
	}

	if err := os.MkdirAll(farm, 0o700); err != nil {
		t.Fatalf("create farm directory: %v", err)
	}
	if farmOnDisk(plan, gitRootTarget(t, root)) {
		t.Error("a farm directory with no manifest reported usable")
	}

	if err := os.WriteFile(manifestPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if farmOnDisk(plan, gitRootTarget(t, root)) {
		t.Error("a manifest that does not decode reported usable")
	}

	data, err := sourcefarm.Encode(sourcefarm.BuildManifest(plan, sourcefarm.OriginGitRoot, nil))
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if !farmOnDisk(plan, gitRootTarget(t, root)) {
		t.Error("a baked farm reported unusable")
	}
	if _, ok := loadSourcePlan(gitRootTarget(t, root), false); !ok {
		t.Fatal("a baked farm was not served to the activation fallback")
	}

	// A farm materialization refuses to touch must not be reported usable
	// either: this is the answer that decides whether a materialization failure
	// is survivable, and the failure it must not survive is that very refusal.
	if err := os.Chmod(farm, 0o777); err != nil {
		t.Fatalf("chmod farm directory: %v", err)
	}
	if farmOnDisk(plan, gitRootTarget(t, root)) {
		t.Error("a world-writable farm reported usable, so a refused bake would activate it")
	}
	if _, fresh := loadSourcePlan(gitRootTarget(t, root), false); fresh {
		t.Error("a world-writable farm was served to the activation fallback")
	}
}

// TestFreshSourcePlanServesTheManifest covers the activation fast path: a fresh
// manifest is rendered straight back, and every state that is not "fresh with a
// farm behind it" falls through to a full resolve rather than activating a
// directory that is not there.
func TestFreshSourcePlanServesTheManifest(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)

	root := t.TempDir()
	farm, err := env.GetProjectBinPath(root)
	if err != nil {
		t.Fatalf("GetProjectBinPath() error = %v", err)
	}
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		t.Fatalf("GetProjectManifestPath() error = %v", err)
	}
	if err := os.MkdirAll(farm, 0o700); err != nil {
		t.Fatalf("create farm directory: %v", err)
	}
	writeFarmEntries(t, farm, "tofu")

	watched := filepath.Join(root, "datamitsu.config.js")
	if err := os.WriteFile(watched, []byte("//\n"), 0o600); err != nil {
		t.Fatalf("write watched file: %v", err)
	}
	plan := sourcefarm.Plan{
		Root:     root,
		FarmDir:  farm,
		Entries:  []sourcefarm.Entry{{Name: "tofu", Installed: true}},
		Shadowed: []sourcefarm.Shadow{{Name: "tofu", Path: "/usr/local/bin/tofu"}},
	}
	m := sourcefarm.BuildManifest(plan, sourcefarm.OriginGitRoot, sourcefarm.WatchSet(sourcefarm.WatchPaths(root, []string{watched})))
	data, err := sourcefarm.Encode(m)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	got, fresh := freshSourcePlanFor(gitRootTarget(t, root))
	if !fresh {
		t.Fatal("a fresh manifest with its farm on disk was not served from the manifest")
	}
	if got.Root != root || got.FarmDir != farm {
		t.Fatalf("served plan = %+v, want root %q farm %q", got, root, farm)
	}
	// The warnings the activation prints come from the served plan, so the
	// manifest's entry and shadow lists have to survive the round trip.
	if len(got.Entries) != 1 || got.Entries[0].Name != "tofu" {
		t.Errorf("served plan lost its entries: %+v", got.Entries)
	}
	if len(got.Shadowed) != 1 || got.Shadowed[0].Path != "/usr/local/bin/tofu" {
		t.Errorf("served plan lost its shadow list: %+v", got.Shadowed)
	}

	// A farm directory deleted out from under a manifest that is otherwise fresh
	// must re-bake: activating it would prepend a path that does not exist.
	if err := os.RemoveAll(farm); err != nil {
		t.Fatalf("remove farm directory: %v", err)
	}
	if _, fresh := freshSourcePlanFor(gitRootTarget(t, root)); fresh {
		t.Error("a manifest whose farm is gone was served anyway")
	}

	// A farm that lost one entry file is the same failure one name at a time: the
	// tree is unchanged, so nothing about freshness notices, and that name falls
	// through PATH to whatever the system has.
	if err := os.MkdirAll(farm, 0o700); err != nil {
		t.Fatalf("recreate farm directory: %v", err)
	}
	if _, fresh := freshSourcePlanFor(gitRootTarget(t, root)); fresh {
		t.Error("a farm missing the entry its manifest describes was served anyway")
	}
	writeFarmEntries(t, farm, "tofu")
	if _, fresh := freshSourcePlanFor(gitRootTarget(t, root)); !fresh {
		t.Fatal("restoring the entry did not make the farm servable again")
	}

	// A changed tree must re-bake too.
	if err := os.WriteFile(watched, []byte("// changed\n"), 0o600); err != nil {
		t.Fatalf("rewrite watched file: %v", err)
	}
	if _, fresh := freshSourcePlanFor(gitRootTarget(t, root)); fresh {
		t.Error("a stale manifest was served anyway")
	}
}

// TestWatchSetSincePrefersThePreLoadTuple pins the anti-TOCTOU rule in
// bakeSourceFarm: a config edit that lands while the config is being evaluated
// must be recorded as the state *before* the read, so the next freshness check
// reports stale. Recording the post-edit tuple would claim the farm already
// reflects an edit it never saw, and it would keep claiming it forever.
func TestWatchSetSincePrefersThePreLoadTuple(t *testing.T) {
	prior := []sourcefarm.WatchFile{
		{Path: "/repo/datamitsu.config.ts", MtimeNS: 100, Size: 10, Exists: true},
		{Path: "/repo/.git/HEAD", MtimeNS: 200, Size: 20, Exists: true},
	}
	current := []sourcefarm.WatchFile{
		{Path: "/repo/before.config.ts", MtimeNS: 300, Size: 30, Exists: true},
		{Path: "/repo/datamitsu.config.ts", MtimeNS: 999, Size: 99, Exists: true},
		{Path: "/repo/.git/HEAD", MtimeNS: 200, Size: 20, Exists: true},
	}

	got := watchSetSince(prior, current)
	if len(got) != len(current) {
		t.Fatalf("watchSetSince changed the watch set length: %+v", got)
	}
	if got[1] != prior[0] {
		t.Errorf("a file edited during the load recorded its new tuple: %+v", got[1])
	}
	if got[0] != current[0] {
		t.Errorf("a path the pre-load snapshot never saw was rewritten: %+v", got[0])
	}
	if got[2] != current[2] {
		t.Errorf("an unchanged path was not passed through: %+v", got[2])
	}

	// With nothing to compare against the post-load set is the only answer there
	// is, and it must survive untouched.
	if got := watchSetSince(nil, current); !slices.Equal(got, current) {
		t.Errorf("watchSetSince(nil, current) = %+v, want %+v", got, current)
	}
}

// TestPreviousSourcePlanServesAStaleFarm covers the failed-bake fallback: when
// the config cannot be resolved at all — it does not evaluate on this branch, a
// remote config is unreachable offline — activation takes the stale farm every
// already-activated shell for this root is running over activating nothing.
// Activating nothing exits 0 with a shell that was never activated, and every declared tool
// then resolves through the rest of PATH to whatever the system has.
func TestPreviousSourcePlanServesAStaleFarm(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)

	root := t.TempDir()
	farm, err := env.GetProjectBinPath(root)
	if err != nil {
		t.Fatalf("GetProjectBinPath() error = %v", err)
	}
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		t.Fatalf("GetProjectManifestPath() error = %v", err)
	}
	if err := os.MkdirAll(farm, 0o700); err != nil {
		t.Fatalf("create farm directory: %v", err)
	}
	writeFarmEntries(t, farm, "tofu")

	watched := filepath.Join(root, "datamitsu.config.js")
	if err := os.WriteFile(watched, []byte("//\n"), 0o600); err != nil {
		t.Fatalf("write watched file: %v", err)
	}
	plan := sourcefarm.Plan{
		Root:    root,
		FarmDir: farm,
		Entries: []sourcefarm.Entry{{Name: "tofu", Installed: true}},
	}
	m := sourcefarm.BuildManifest(plan, sourcefarm.OriginGitRoot, sourcefarm.WatchSet(sourcefarm.WatchPaths(root, []string{watched})))
	data, err := sourcefarm.Encode(m)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Exactly the state a broken config leaves behind: the watch set no longer
	// matches, so the fast path refuses it, but the farm is still there and still
	// works.
	if err := os.WriteFile(watched, []byte("// changed\n"), 0o600); err != nil {
		t.Fatalf("rewrite watched file: %v", err)
	}
	if _, fresh := freshSourcePlanFor(gitRootTarget(t, root)); fresh {
		t.Fatal("a stale manifest was served by the fast path")
	}

	got, ok := previousSourcePlan(gitRootTarget(t, root))
	if !ok {
		t.Fatal("a stale farm still on disk was not offered as the fallback")
	}
	if got.FarmDir != farm || len(got.Entries) != 1 || got.Entries[0].Name != "tofu" {
		t.Fatalf("fallback plan = %+v, want the farm at %q with tofu", got, farm)
	}

	// With the farm gone there is nothing to fall back on, and the caller must
	// report the failure instead of activating a directory that is not there.
	if err := os.RemoveAll(farm); err != nil {
		t.Fatalf("remove farm directory: %v", err)
	}
	if _, ok := previousSourcePlan(gitRootTarget(t, root)); ok {
		t.Error("a farm that is gone was offered as the fallback")
	}
}

// TestPreviousSourcePlanRefusesARetiredManifest is where the fallback above
// stops. "Stale is fine" is not "anything is fine": a manifest written in a
// format this build reads differently — the version is bumped exactly when the
// shim's correctness starts depending on a new field — must not go on PATH
// precisely when the bake that would have replaced it could not run.
func TestPreviousSourcePlanRefusesARetiredManifest(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)

	root := t.TempDir()
	farm, err := env.GetProjectBinPath(root)
	if err != nil {
		t.Fatalf("GetProjectBinPath() error = %v", err)
	}
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		t.Fatalf("GetProjectManifestPath() error = %v", err)
	}
	if err := os.MkdirAll(farm, 0o700); err != nil {
		t.Fatalf("create farm directory: %v", err)
	}

	plan := sourcefarm.Plan{
		Root:    root,
		FarmDir: farm,
		Entries: []sourcefarm.Entry{{Name: "tofu", Installed: true}},
	}
	m := sourcefarm.BuildManifest(plan, sourcefarm.OriginGitRoot, nil)
	m.FormatVersion = sourcefarm.ManifestFormatVersion - 1
	data, err := sourcefarm.Encode(m)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if _, ok := previousSourcePlan(gitRootTarget(t, root)); ok {
		t.Error("a manifest from a retired format was offered as the fallback")
	}
}

// TestSourceManifestDecidesRequiresDiscoveredConfig pins that a flag-supplied
// config never answers a *git-root* farm from the manifest. That farm's identity
// is the root, so a flag's file is not part of what selected the manifest: its
// watch set describes a chain it has never seen, and serving it would activate
// the wrong toolchain and report it fresh.
func TestSourceManifestDecidesRequiresDiscoveredConfig(t *testing.T) {
	t.Cleanup(func() {
		ConfigPaths = nil
		BeforeConfigPaths = nil
		NoAutoConfig = false
	})

	target := gitRootTarget(t, "/repo")

	ConfigPaths, BeforeConfigPaths, NoAutoConfig = nil, nil, false
	if !sourceManifestDecides(target) {
		t.Error("a discovered config was not allowed to use the manifest")
	}

	ConfigPaths = []string{"/elsewhere/other.config.ts"}
	if sourceManifestDecides(target) {
		t.Error("an explicit --config was served from a git-root manifest")
	}

	// --before-config prepends files to the chain, so a manifest baked without
	// them describes a farm missing whatever they declare.
	ConfigPaths, BeforeConfigPaths = nil, []string{"/elsewhere/shared.js"}
	if sourceManifestDecides(target) {
		t.Error("an explicit --before-config was served from the manifest")
	}

	ConfigPaths, BeforeConfigPaths, NoAutoConfig = nil, nil, true
	if sourceManifestDecides(target) {
		t.Error("--no-auto-config was served from the manifest")
	}
}

// TestConfigFarmManifestDecides is the other half: an explicit-config farm may
// use its manifest, because the manifest it can find is the one the chain itself
// hashes to. Activation lives in a shell rc file, so the alternative is a full
// config resolution in every shell and every tmux pane.
func TestConfigFarmManifestDecides(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	cfg := filepath.Join(t.TempDir(), "tools.config.ts")
	if err := os.WriteFile(cfg, []byte("//\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if !sourceManifestDecides(configTarget(t, cfg)) {
		t.Error("an explicit-config farm was forced to re-resolve on every activation")
	}
}

// TestConfigChainArgsAreAbsoluteForReplay pins what the manifest records for
// the shim to replay. Paths must be absolute: the shim spawns from whatever
// directory the user happened to run a tool in, not the one that baked the farm.
func TestConfigChainArgsAreAbsoluteForReplay(t *testing.T) {
	t.Cleanup(func() {
		ConfigPaths = nil
		BeforeConfigPaths = nil
		NoAutoConfig = false
	})

	ConfigPaths, BeforeConfigPaths, NoAutoConfig = nil, nil, false
	if got := configChainArgs(); len(got) != 0 {
		t.Errorf("configChainArgs() = %v for an auto-discovered config, want empty", got)
	}

	dir := t.TempDir()
	t.Chdir(dir)
	BeforeConfigPaths = []string{"shared.js"}
	NoAutoConfig = true

	got := configChainArgs()
	want := []string{"--no-auto-config", "--before-config", filepath.Join(dir, "shared.js")}
	if len(got) != len(want) {
		t.Fatalf("configChainArgs() = %v, want %v", got, want)
	}
	for i := range want {
		// The temp dir may itself be a symlink (/var on macOS); compare the
		// flag words exactly and the path by its base plus absoluteness.
		if strings.HasPrefix(want[i], "--") {
			if got[i] != want[i] {
				t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
			}
			continue
		}
		if !filepath.IsAbs(got[i]) || filepath.Base(got[i]) != "shared.js" {
			t.Errorf("arg %d = %q, want an absolute path to shared.js", i, got[i])
		}
	}
}

// TestFlaggedFarmIsNotServedToAPlainInvocation is the inverse of
// TestSourceManifestDecidesRequiresDiscoveredConfig and the direction that fails
// silently. A `--config other.ts` bake writes the *root's* farm — the farm's
// identity is the git root, not the chain — and records a watch set that
// includes that flag's own file, so every stat tuple in it compares equal
// afterwards. Without a config-chain comparison the next plain `source bash` in
// the repository activates the other chain's toolchain and reports success.
func TestFlaggedFarmIsNotServedToAPlainInvocation(t *testing.T) {
	t.Cleanup(func() {
		ConfigPaths = nil
		setConfigChainFiles(nil)
	})

	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)

	root := t.TempDir()
	farm, err := env.GetProjectBinPath(root)
	if err != nil {
		t.Fatalf("GetProjectBinPath() error = %v", err)
	}
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		t.Fatalf("GetProjectManifestPath() error = %v", err)
	}
	if err := os.MkdirAll(farm, 0o700); err != nil {
		t.Fatalf("create farm directory: %v", err)
	}
	writeFarmEntries(t, farm, "from-other-chain")

	// Bake as the flagged invocation would: the other chain's file is in the
	// watch set, so nothing about the tree makes this manifest stale.
	other := filepath.Join(t.TempDir(), "other.config.ts")
	if err := os.WriteFile(other, []byte("//\n"), 0o600); err != nil {
		t.Fatalf("write other config: %v", err)
	}
	ConfigPaths = []string{other}
	plan := sourcefarm.Plan{Root: root, FarmDir: farm, Entries: []sourcefarm.Entry{{Name: "from-other-chain"}}}
	m := sourcefarm.BuildManifest(plan, sourcefarm.OriginGitRoot, sourcefarm.WatchSet(sourcefarm.WatchPaths(root, []string{other})))
	m.ConfigArgs = configChainArgs()
	if len(m.ConfigArgs) == 0 {
		t.Fatal("configChainArgs() recorded nothing for a --config invocation")
	}
	data, err := sourcefarm.Encode(m)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Still fresh by the watch set alone — this is what makes the bug silent.
	if !sourcefarm.Validate(m) {
		t.Fatal("the flagged manifest is stale by its watch set; the test no longer covers the silent case")
	}

	ConfigPaths = nil
	if _, fresh := freshSourcePlanFor(gitRootTarget(t, root)); fresh {
		t.Error("a plain invocation was served a farm baked from an explicit --config")
	}
	if manifestStatus(manifestPath, gitRootTarget(t, root)).Fresh {
		t.Error("`source status` reported a farm from another config chain as fresh")
	}
	if sourceFarmIsFresh(gitRootTarget(t, root)) {
		t.Error("`source refresh` would have answered \"already up to date\" for another chain's farm")
	}

	// The same invocation that baked it is still served from it: this must not
	// force a rebake on every wrapper-driven activation.
	ConfigPaths = []string{other}
	if _, fresh := freshSourcePlanFor(gitRootTarget(t, root)); !fresh {
		t.Error("the invocation that baked the farm was not served from it")
	}
}

// TestConfigChainFilesAreAbsolute pins the watch set against a relative
// --config. The shim stats the recorded paths from whatever directory a tool was
// invoked in, so a relative entry records "exists" at bake time and "missing" on
// every later stat: the farm reads as permanently stale and every tool
// invocation pays a full rebake.
func TestConfigChainFilesAreAbsolute(t *testing.T) {
	t.Cleanup(func() { setConfigChainFiles(nil) })

	dir := t.TempDir()
	t.Chdir(dir)

	setConfigChainFiles([]configSource{
		{name: "default", isDefault: true},
		{name: "shared.js", path: "shared.js"},
		{name: "auto", path: filepath.Join(dir, "datamitsu.config.ts")},
	})

	got := ConfigChainFiles()
	if len(got) != 2 {
		t.Fatalf("ConfigChainFiles() = %v, want two entries", got)
	}
	for _, p := range got {
		if !filepath.IsAbs(p) {
			t.Errorf("ConfigChainFiles() recorded a relative path %q", p)
		}
	}
	if filepath.Base(got[0]) != "shared.js" {
		t.Errorf("ConfigChainFiles()[0] = %q, want an absolute path to shared.js", got[0])
	}
}
