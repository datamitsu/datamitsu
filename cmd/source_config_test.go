package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

// writeMachineConfigFile puts a config in a fresh temp directory and returns its
// path, standing in for a config that lives outside every repository.
func writeMachineConfigFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("//\n"), 0o600); err != nil {
		t.Fatalf("write config %q: %v", name, err)
	}
	return path
}

// TestExplicitConfigTargetIsRootless pins what a --config activation resolves
// to: the explicit-config origin, no git root, the resolved chain, and both
// paths inside the config-farm namespace.
func TestExplicitConfigTargetIsRootless(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	cfg := writeMachineConfigFile(t, "machine.config.ts")

	target := configTarget(t, cfg)

	if target.Origin != sourcefarm.OriginExplicitConfig {
		t.Errorf("origin = %q, want explicit-config", target.Origin)
	}
	if target.Root != "" {
		// A fabricated root would be indistinguishable from a real one to the
		// shim's origin branch, which is where the trust boundary lives.
		t.Errorf("root = %q, want empty for a farm named by --config", target.Root)
	}
	if len(target.ConfigPaths) != 1 || filepath.Base(target.ConfigPaths[0]) != "machine.config.ts" {
		t.Fatalf("config chain = %v, want the named config", target.ConfigPaths)
	}
	if !filepath.IsAbs(target.ConfigPaths[0]) {
		t.Errorf("config chain entry %q is not absolute", target.ConfigPaths[0])
	}

	wantFarm, err := env.GetConfigFarmBinPath(target.ConfigPaths)
	if err != nil {
		t.Fatalf("GetConfigFarmBinPath() error = %v", err)
	}
	if target.FarmDir != wantFarm {
		t.Errorf("farm = %q, want %q", target.FarmDir, wantFarm)
	}
	if filepath.Dir(target.ManifestPath) != filepath.Dir(target.FarmDir) {
		t.Errorf("manifest %q is not a sibling of the farm %q", target.ManifestPath, target.FarmDir)
	}
	if !strings.Contains(target.FarmDir, string(filepath.Separator)+env.ConfigFarmsDirName+string(filepath.Separator)) {
		t.Errorf("farm %q is not in the config-farm namespace", target.FarmDir)
	}
}

// TestExplicitConfigTargetIncludesBeforeConfigsInItsIdentity pins that
// --before-config is part of what a farm *is*, not a modifier of it. Those files
// are loaded before the named config and change what the farm contains, so two
// invocations differing only in them must be two farms — otherwise one would
// overwrite the other at the same path, and the watch set of either would report
// the other fresh.
func TestExplicitConfigTargetIncludesBeforeConfigsInItsIdentity(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Cleanup(func() { BeforeConfigPaths = nil })

	cfg := writeMachineConfigFile(t, "machine.config.ts")
	shared := writeMachineConfigFile(t, "shared.js")

	plain := configTarget(t, cfg)

	BeforeConfigPaths = []string{shared}
	withShared := configTarget(t, cfg)

	if plain.FarmDir == withShared.FarmDir {
		t.Errorf("--before-config did not change the farm identity: %s", plain.FarmDir)
	}
	if len(withShared.ConfigPaths) != 2 || filepath.Base(withShared.ConfigPaths[0]) != "shared.js" {
		t.Errorf("chain = %v, want the before-config first", withShared.ConfigPaths)
	}
}

// TestSourceConfigArgsForceNoAutoConfig is the trust boundary in its bake form.
// The flags recorded in the manifest are what the shim replays on a rebake, and
// they must name the chain explicitly and switch discovery off — otherwise a
// machine-level farm baked (or re-baked) from inside a repository merges that
// repository's config into the toolchain the user activates in every shell.
func TestSourceConfigArgsForceNoAutoConfig(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	cfg := writeMachineConfigFile(t, "machine.config.ts")

	target := configTarget(t, cfg)
	args := sourceConfigArgs(target)

	if !slices.Contains(args, "--no-auto-config") {
		t.Errorf("recorded args = %v, want --no-auto-config", args)
	}
	if !slices.Contains(args, target.ConfigPaths[0]) {
		t.Errorf("recorded args = %v, want the resolved config path", args)
	}
	for _, p := range args {
		if p != "--no-auto-config" && p != "--config" && !filepath.IsAbs(p) {
			t.Errorf("recorded arg %q is not absolute; the shim replays these from another directory", p)
		}
	}

	// A git-root farm's recorded args are untouched: this is the branch every
	// existing manifest goes through, and it must stay byte-identical.
	ConfigPaths = nil
	if got := sourceConfigArgs(gitRootTarget(t, "/repo")); len(got) != 0 {
		t.Errorf("a plain git-root activation recorded %v, want nothing", got)
	}
}

// TestSourceConfigArgsAreOneFragmentPerIdentity asserts every spelling of one
// chain records the same argv fragment. Identity already collapses them into a
// single farm, so a fragment that varied by spelling would make the same farm
// look chain-mismatched to the next activation and re-bake on every alternation.
func TestSourceConfigArgsAreOneFragmentPerIdentity(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	cfg := writeMachineConfigFile(t, "machine.config.ts")
	t.Chdir(filepath.Dir(cfg))

	absolute := sourceConfigArgs(configTarget(t, cfg))
	relative := sourceConfigArgs(configTarget(t, "machine.config.ts"))
	dotted := sourceConfigArgs(configTarget(t, "./machine.config.ts"))

	if !slices.Equal(absolute, relative) || !slices.Equal(absolute, dotted) {
		t.Errorf("one chain recorded three fragments:\n%v\n%v\n%v", absolute, relative, dotted)
	}
}

// TestTargetWatchPathsForAConfigFarm pins the watch set of a rootless farm: the
// chain and nothing else. Borrowing a project farm's tripwires — .git/HEAD, the
// lockfile, the auto-config candidates — would tie a machine-level farm to
// whichever repository the shell happened to be standing in when it was baked.
func TestTargetWatchPathsForAConfigFarm(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	cfg := writeMachineConfigFile(t, "machine.config.ts")
	target := configTarget(t, cfg)

	got := targetWatchPaths(target, []string{"/somewhere/else.ts"})
	if !slices.Equal(got, target.ConfigPaths) {
		t.Errorf("watch paths = %v, want exactly the resolved chain %v", got, target.ConfigPaths)
	}
	for _, p := range got {
		if strings.Contains(p, ".git") || strings.HasSuffix(p, "pnpm-lock.yaml") {
			t.Errorf("a rootless farm watches a repository file: %q", p)
		}
	}
}

// TestConfigActivationExportsTheChain pins what the shells are told. A farm with
// no git root exports the chain that identifies it and deliberately not a root
// variable: an empty DATAMITSU_ROOT reads as a repository that could not be
// determined, and a fabricated one is worse.
func TestConfigActivationExportsTheChain(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	cfg := writeMachineConfigFile(t, "machine.config.ts")
	target := configTarget(t, cfg)

	a := activationFor(target, sourcefarm.Plan{FarmDir: target.FarmDir})

	bash := renderBash(a)
	fish := renderFish(a)
	for name, clear := range map[string]string{
		"bash": "unset " + env.SourceRootVarName() + "\n",
		"fish": "set -e -g " + env.SourceRootVarName() + "\n",
	} {
		out := map[string]string{"bash": bash, "fish": fish}[name]
		if strings.Contains(out, "export "+env.SourceRootVarName()) ||
			strings.Contains(out, "set -gx "+env.SourceRootVarName()) {
			t.Errorf("%s activation exported a git root for a rootless farm:\n%s", name, out)
		}
		// A shell re-activated from a project farm carries DATAMITSU_ROOT from
		// that activation. Leaving it behind would have this shell advertise a
		// repository it is no longer pinned to — well-formed and wrong, which a
		// prompt and a bug report both read as current.
		if !strings.Contains(out, clear) {
			t.Errorf("%s activation did not clear a stale %s:\n%s", name, env.SourceRootVarName(), out)
		}
	}
	for name, out := range map[string]string{"bash": bash, "fish": fish} {
		if !strings.Contains(out, env.SourceFarmConfigVarName()) {
			t.Errorf("%s activation did not export the config chain:\n%s", name, out)
		}
		if !strings.Contains(out, target.ConfigPaths[0]) {
			t.Errorf("%s activation does not name the config:\n%s", name, out)
		}
		if !strings.Contains(out, target.FarmDir) {
			t.Errorf("%s activation does not name the farm:\n%s", name, out)
		}
	}
	if n := countPathAssignments(bash); n != 1 {
		t.Errorf("bash activation assigned PATH %d times, want 1:\n%s", n, bash)
	}
	if !strings.Contains(fish, "fish_add_path --global --move --path ") {
		t.Errorf("fish activation does not use --move:\n%s", fish)
	}
}

// TestConfigFarmAndProjectFarmDoNotShareAManifest asserts a config farm's
// manifest never answers for a project farm or the reverse. The two live in
// different namespaces, so this is really a check that the origin recorded in a
// manifest is compared before it is served: a farm baked for one identity and
// read back under the other would activate a toolchain nobody asked for.
func TestConfigFarmAndProjectFarmDoNotShareAManifest(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	cfg := writeMachineConfigFile(t, "machine.config.ts")

	configTgt := configTarget(t, cfg)
	if err := os.MkdirAll(configTgt.FarmDir, 0o700); err != nil {
		t.Fatalf("create config farm: %v", err)
	}
	writeFarmEntries(t, configTgt.FarmDir, "machine-tool")

	plan := sourcefarm.Plan{
		FarmDir: configTgt.FarmDir,
		Entries: []sourcefarm.Entry{{Name: "machine-tool", Installed: true}},
	}
	watch := sourcefarm.WatchSet(targetWatchPaths(configTgt, nil))
	m := sourcefarm.BuildConfigManifest(plan, configTgt.ConfigPaths, watch)
	m.ConfigArgs = sourceConfigArgs(configTgt)
	data, err := sourcefarm.Encode(m)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(configTgt.ManifestPath), 0o700); err != nil {
		t.Fatalf("create config farm directory: %v", err)
	}
	if err := os.WriteFile(configTgt.ManifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if _, fresh := freshSourcePlanFor(configTgt); !fresh {
		t.Fatal("a config farm was not served back to the activation that baked it")
	}

	// The same manifest read as a project farm's: same file, other origin.
	asProject := configTgt
	asProject.Origin = sourcefarm.OriginGitRoot
	asProject.Root = "/repo"
	asProject.ConfigPaths = nil
	if _, fresh := freshSourcePlanFor(asProject); fresh {
		t.Error("an explicit-config manifest was served to a git-root activation")
	}
	if manifestStatus(configTgt.ManifestPath, asProject).Fresh {
		t.Error("`source status` reported a config farm as a fresh project farm")
	}
	if !manifestStatus(configTgt.ManifestPath, configTgt).Fresh {
		t.Error("`source status` reported a farm as stale to the activation that baked it")
	}
}

// TestConfigFarmStatusReportsOriginAndChain pins the status document for a
// rootless farm: which origin it has, and the chain a user would pass to
// --config again in place of a root they cannot cd to.
func TestConfigFarmStatusReportsOriginAndChain(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	cfg := writeMachineConfigFile(t, "machine.config.ts")
	target := configTarget(t, cfg)

	s := buildSourceStatus(sourcefarm.Plan{FarmDir: target.FarmDir}, target)
	if s.Origin != sourcefarm.OriginExplicitConfig {
		t.Errorf("origin = %q, want explicit-config", s.Origin)
	}
	if s.Root != "" {
		t.Errorf("root = %q, want empty", s.Root)
	}
	if !slices.Equal(s.ConfigPaths, target.ConfigPaths) {
		t.Errorf("configPaths = %v, want %v", s.ConfigPaths, target.ConfigPaths)
	}

	var b strings.Builder
	if err := renderSourceStatus(&b, s); err != nil {
		t.Fatalf("renderSourceStatus() error = %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "origin:   explicit-config") {
		t.Errorf("the report does not name the origin:\n%s", out)
	}
	if !strings.Contains(out, "config:   "+target.ConfigPaths[0]) {
		t.Errorf("the report does not name the chain:\n%s", out)
	}
	if strings.Contains(out, "root:") {
		t.Errorf("the report printed a root line for a rootless farm:\n%s", out)
	}
}

// TestConfigFarmLabelNamesTheChain pins the identity used in every human
// message a rootless farm produces — the refresh summary, the fallback line,
// the bake failure. Naming a repository that does not exist is an instruction
// nobody can follow.
func TestConfigFarmLabelNamesTheChain(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	cfg := writeMachineConfigFile(t, "machine.config.ts")
	target := configTarget(t, cfg)

	if got := target.label(); got != target.ConfigPaths[0] {
		t.Errorf("label = %q, want the config chain %q", got, target.ConfigPaths[0])
	}
	if got := gitRootTarget(t, "/repo").label(); got != "/repo" {
		t.Errorf("a git-root farm is labelled %q, want /repo", got)
	}
}

// TestPreviousConfigFarmSurvivesAFailedBake covers the fallback on the rootless
// path. A machine-level config lives in a shell rc file, so a config that stops
// evaluating — an edit mid-save, a remote import unreachable offline — would
// otherwise leave every new shell on the machine with no toolchain at all. The
// git-root equivalent is TestPreviousSourcePlanServesAStaleFarm; this is the
// same guarantee for a farm that has no repository to fall back through.
func TestPreviousConfigFarmSurvivesAFailedBake(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	cfg := writeMachineConfigFile(t, "machine.config.ts")
	target := configTarget(t, cfg)

	if err := os.MkdirAll(target.FarmDir, 0o700); err != nil {
		t.Fatalf("create config farm: %v", err)
	}
	writeFarmEntries(t, target.FarmDir, "machine-tool")

	plan := sourcefarm.Plan{
		FarmDir: target.FarmDir,
		Entries: []sourcefarm.Entry{{Name: "machine-tool", Installed: true}},
	}
	watch := sourcefarm.WatchSet(targetWatchPaths(target, nil))
	m := sourcefarm.BuildConfigManifest(plan, target.ConfigPaths, watch)
	m.ConfigArgs = sourceConfigArgs(target)
	data, err := sourcefarm.Encode(m)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := os.WriteFile(target.ManifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Editing the chain is what makes the manifest stale, and it is also the
	// only thing an explicit-config farm watches.
	if err := os.WriteFile(target.ConfigPaths[0], []byte("// changed\n"), 0o600); err != nil {
		t.Fatalf("rewrite the config: %v", err)
	}
	if _, fresh := freshSourcePlanFor(target); fresh {
		t.Fatal("a stale config farm was served by the activation fast path")
	}

	got, ok := previousSourcePlan(target)
	if !ok {
		t.Fatal("a stale machine-level farm still on disk was not offered as the fallback")
	}
	if got.FarmDir != target.FarmDir {
		t.Errorf("fallback farm = %q, want %q", got.FarmDir, target.FarmDir)
	}
	if got.Root != "" {
		t.Errorf("fallback plan carries root %q, want none for a rootless farm", got.Root)
	}
	if len(got.Entries) != 1 || got.Entries[0].Name != "machine-tool" {
		t.Errorf("fallback entries = %+v, want machine-tool", got.Entries)
	}
	if !farmOnDisk(got, target) {
		t.Error("farmOnDisk() refused a machine-level farm that is present and readable")
	}

	// The chain check applies to the fallback too: a farm baked from a different
	// chain must never be activated in place of the one that was asked for.
	other := target
	other.ConfigPaths = []string{filepath.Join(filepath.Dir(target.ConfigPaths[0]), "other.config.ts")}
	if _, ok := previousSourcePlan(other); ok {
		t.Error("a farm baked from a different config chain was offered as the fallback")
	}

	// Nothing on disk to fall back to is the one state that must report failure
	// instead of activating a directory that is not there.
	if err := os.RemoveAll(target.FarmDir); err != nil {
		t.Fatalf("remove the farm directory: %v", err)
	}
	if _, ok := previousSourcePlan(target); ok {
		t.Error("a machine-level farm that is gone was offered as the fallback")
	}
}
