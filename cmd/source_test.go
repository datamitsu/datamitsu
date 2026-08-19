package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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

func simplePlan() sourcefarm.Plan {
	return sourcefarm.Plan{Root: "/repo", FarmDir: "/cache/projects/abc/bin"}
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
		got := renderBash(plan)
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
		got := renderFish(plan)
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
	renderers := map[string]func(sourcefarm.Plan) string{"bash": renderBash, "fish": renderFish}
	for name, render := range renderers {
		t.Run(name, func(t *testing.T) {
			plan := hostilePlan()
			if first, second := render(plan), render(plan); first != second {
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
	for name, render := range map[string]func(sourcefarm.Plan) string{"bash": renderBash, "fish": renderFish} {
		t.Run(name, func(t *testing.T) {
			got := render(missing)
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
	script := renderBash(plan) + "printf '%s' \"$PATH\"\n"

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
	script := renderBash(plan) + "printf '%s' \"$PATH\"\n"
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
	script := renderFish(plan) + "printf '%s' \"$PATH[1]\"\n"
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
	if strings.Contains(warnings, "tofu\n") && !strings.Contains(warnings, "shadowing") {
		t.Errorf("shadow warning lost its path:\n%s", warnings)
	}

	for name, render := range map[string]func(sourcefarm.Plan) string{"bash": renderBash, "fish": renderFish} {
		stdout := render(plan)
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

	if farmOnDisk(plan) {
		t.Error("a root that was never baked reported a usable farm")
	}

	if err := os.MkdirAll(farm, 0o700); err != nil {
		t.Fatalf("create farm directory: %v", err)
	}
	if farmOnDisk(plan) {
		t.Error("a farm directory with no manifest reported usable")
	}

	if err := os.WriteFile(manifestPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if farmOnDisk(plan) {
		t.Error("a manifest that does not decode reported usable")
	}

	data, err := sourcefarm.Encode(sourcefarm.BuildManifest(plan, sourcefarm.OriginGitRoot, nil))
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if !farmOnDisk(plan) {
		t.Error("a baked farm reported unusable")
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
	m := sourcefarm.BuildManifest(plan, sourcefarm.OriginGitRoot, sourcefarm.WatchPaths(root, []string{watched}))
	data, err := sourcefarm.Encode(m)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	got, fresh := freshSourcePlanFor(root)
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
	if _, fresh := freshSourcePlanFor(root); fresh {
		t.Error("a manifest whose farm is gone was served anyway")
	}

	// A changed tree must re-bake too.
	if err := os.MkdirAll(farm, 0o700); err != nil {
		t.Fatalf("recreate farm directory: %v", err)
	}
	if err := os.WriteFile(watched, []byte("// changed\n"), 0o600); err != nil {
		t.Fatalf("rewrite watched file: %v", err)
	}
	if _, fresh := freshSourcePlanFor(root); fresh {
		t.Error("a stale manifest was served anyway")
	}
}

// TestSourceManifestDecidesRequiresDiscoveredConfig pins that a flag-supplied
// config always re-resolves. The manifest's watch set describes the chain that
// baked it and cannot answer for a file it has never seen, so serving it for an
// explicit --config would activate the wrong toolchain and report it fresh.
func TestSourceManifestDecidesRequiresDiscoveredConfig(t *testing.T) {
	t.Cleanup(func() {
		ConfigPaths = nil
		NoAutoConfig = false
	})

	ConfigPaths, NoAutoConfig = nil, false
	if !sourceManifestDecides() {
		t.Error("a discovered config was not allowed to use the manifest")
	}

	ConfigPaths = []string{"/elsewhere/other.config.ts"}
	if sourceManifestDecides() {
		t.Error("an explicit --config was served from the manifest")
	}

	ConfigPaths, NoAutoConfig = nil, true
	if sourceManifestDecides() {
		t.Error("--no-auto-config was served from the manifest")
	}
}
