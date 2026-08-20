package shim

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

// configHarness extends the git-root harness with a second farm: one baked from
// an explicitly named config chain, living in the sibling cache namespace and
// carrying no root at all.
//
// Both farms exist in every fixture here on purpose. The properties under test
// are all about which of the two answers a name, and a fixture with only the
// config farm present would pass them by accident.
type configHarness struct {
	*harness

	// configDir is a directory outside every repository, and configPath the
	// config file in it the farm was baked from.
	configDir  string
	configPath string

	configFarmDir      string
	configManifestPath string

	// getwdCalls and superprojectCalls count the git-discovery steps. The trust
	// boundary is that an explicit-config invocation performs none of them, and
	// that is asserted on the calls rather than on the outcome: a fixture whose
	// repository happens to have no usable farm would produce the right answer
	// while still having walked the tree.
	getwdCalls        int
	superprojectCalls int
}

func newConfigHarness(t *testing.T) *configHarness {
	t.Helper()

	h := newHarness(t)
	ch := &configHarness{harness: h}

	ch.configDir = filepath.Join(h.base, "machine-config")
	if err := os.MkdirAll(ch.configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	ch.configPath = filepath.Join(ch.configDir, "datamitsu.config.ts")
	if err := os.WriteFile(ch.configPath, []byte("export function getConfig() { return {} }\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	configFarmRoot := filepath.Join(h.base, "cache", env.ConfigFarmsDirName, "deadbeef")
	ch.configFarmDir = filepath.Join(configFarmRoot, "bin")
	if err := os.MkdirAll(ch.configFarmDir, 0o700); err != nil {
		t.Fatalf("create config farm dir: %v", err)
	}
	ch.configManifestPath = filepath.Join(configFarmRoot, env.ProjectManifestFileName)

	inner := h.d.Getwd
	h.d.Getwd = func() (string, error) {
		ch.getwdCalls++
		return inner()
	}
	innerSuper := h.d.Superproject
	h.d.Superproject = func(root string) (string, bool) {
		ch.superprojectCalls++
		return innerSuper(root)
	}
	return ch
}

// writeConfigManifest stores an explicit-config manifest beside the config farm,
// filling in the fields every such manifest carries.
func (ch *configHarness) writeConfigManifest(m sourcefarm.Manifest) {
	ch.t.Helper()
	m.Origin = sourcefarm.OriginExplicitConfig
	m.Root = ""
	m.FarmDir = ch.configFarmDir
	if m.ConfigPaths == nil {
		m.ConfigPaths = []string{ch.configPath}
	}
	if m.FormatVersion == 0 {
		m.FormatVersion = sourcefarm.ManifestFormatVersion
	}
	if m.OS == "" {
		m.OS = runtime.GOOS
	}
	if m.Arch == "" {
		m.Arch = runtime.GOARCH
	}
	data, err := sourcefarm.Encode(m)
	if err != nil {
		ch.t.Fatalf("encode config manifest: %v", err)
	}
	if err := os.WriteFile(ch.configManifestPath, data, 0o600); err != nil {
		ch.t.Fatalf("write config manifest: %v", err)
	}
}

// invokeThroughConfigFarm sets argv to an entry of the config farm.
func (ch *configHarness) invokeThroughConfigFarm(name string, args ...string) {
	ch.t.Helper()
	link := filepath.Join(ch.configFarmDir, name)
	if _, err := os.Lstat(link); err != nil {
		if err := os.WriteFile(link, []byte("shim"), 0o755); err != nil {
			ch.t.Fatalf("create config farm entry: %v", err)
		}
	}
	ch.d.Args = append([]string{link}, args...)
}

// activate makes the dispatcher's environment look like a shell that activated
// the given farm directory.
func (ch *configHarness) activate(farmDir string) {
	ch.d.Environ = func() []string {
		return []string{"PATH=/usr/bin", "HOME=/home/u", env.SourceFarmVarName() + "=" + farmDir}
	}
}

// activateConfig is activate for a machine-level activation: `datamitsu source
// <shell> --config <path>` exports the config chain beside the farm directory
// and clears the root variable, and the shim reads the pair.
func (ch *configHarness) activateConfig(farmDir string) {
	ch.d.Environ = func() []string {
		return []string{
			"PATH=/usr/bin",
			"HOME=/home/u",
			env.SourceFarmVarName() + "=" + farmDir,
			env.SourceFarmConfigVarName() + "=" + ch.configPath,
		}
	}
}

func TestDispatchExplicitConfigFarmPerformsNoGitDiscovery(t *testing.T) {
	ch := newConfigHarness(t)

	// The repository the shell is standing in has its own farm, declaring the
	// same name at a different target. Nothing here may reach it.
	projectTool := ch.tool("tofu-project")
	ch.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: projectTool, Installed: true}},
	})

	configTool := ch.tool("tofu-machine")
	ch.writeConfigManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: configTool, Installed: true}},
	})
	ch.invokeThroughConfigFarm("tofu", "plan")

	code, handled := ch.d.Dispatch()

	if !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true); stderr: %s", code, handled, ch.stderr.String())
	}
	if len(ch.execs) != 1 || ch.execs[0].path != configTool {
		t.Fatalf("Dispatch() ran %v, want %s", ch.execs, configTool)
	}
	if ch.getwdCalls != 0 {
		t.Errorf("Dispatch() called Getwd %d times; an explicit-config farm must not resolve from cwd", ch.getwdCalls)
	}
	if ch.superprojectCalls != 0 {
		t.Errorf("Dispatch() called Superproject %d times; an explicit-config farm must not walk the tree", ch.superprojectCalls)
	}
}

func TestDispatchProjectFarmWinsByPathOrder(t *testing.T) {
	ch := newConfigHarness(t)

	projectTool := ch.tool("tofu-project")
	ch.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: projectTool, Installed: true}},
	})
	ch.writeConfigManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: ch.tool("tofu-machine"), Installed: true}},
	})

	// Both farms are active; the shell resolved the name through the project
	// farm, because that is what PATH order gave it.
	ch.activate(ch.configFarmDir)
	ch.invokeThroughFarm("tofu")

	code, handled := ch.d.Dispatch()

	if !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true); stderr: %s", code, handled, ch.stderr.String())
	}
	if len(ch.execs) != 1 || ch.execs[0].path != projectTool {
		t.Fatalf("Dispatch() ran %v, want the project's %s", ch.execs, projectTool)
	}
}

func TestDispatchExplicitConfigFarmRebakesOnConfigChange(t *testing.T) {
	ch := newConfigHarness(t)
	tool := ch.tool("tofu")
	ch.writeConfigManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: tool, Installed: true}},
	})
	// The config file changed, so the recorded watch tuples no longer match.
	ch.d.Validate = func(sourcefarm.Manifest) bool { return false }
	ch.invokeThroughConfigFarm("tofu")

	code, handled := ch.d.Dispatch()

	if !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true); stderr: %s", code, handled, ch.stderr.String())
	}
	if len(ch.spawnReqs) != 1 {
		t.Fatalf("Dispatch() spawned %v, want one refresh", ch.spawns)
	}
	req := ch.spawnReqs[0]
	wantArgs := []string{"--config", ch.configPath, noAutoConfigFlag, "source", "refresh"}
	if !slices.Equal(req.Args, wantArgs) {
		t.Errorf("rebake args = %v, want %v", req.Args, wantArgs)
	}
	if req.Dir != ch.configDir {
		t.Errorf("rebake ran in %q, want the config's own directory %q", req.Dir, ch.configDir)
	}
	if len(ch.execs) != 1 || ch.execs[0].path != tool {
		t.Fatalf("Dispatch() ran %v after the rebake, want %s", ch.execs, tool)
	}
}

func TestDispatchExplicitConfigFarmReplaysRecordedFlags(t *testing.T) {
	ch := newConfigHarness(t)
	tool := ch.tool("tofu")
	ch.writeConfigManifest(sourcefarm.Manifest{
		ConfigArgs: []string{"--before-config", filepath.Join(ch.configDir, "shared.js"), "--config", ch.configPath},
		Entries:    []sourcefarm.Entry{{Name: "tofu", Command: tool, Installed: true}},
	})
	ch.d.Validate = func(sourcefarm.Manifest) bool { return false }
	ch.invokeThroughConfigFarm("tofu")

	if _, handled := ch.d.Dispatch(); !handled {
		t.Fatalf("Dispatch() declined a config farm entry; stderr: %s", ch.stderr.String())
	}
	if len(ch.spawnReqs) != 1 {
		t.Fatalf("Dispatch() spawned %v, want one refresh", ch.spawns)
	}
	want := []string{
		"--before-config", filepath.Join(ch.configDir, "shared.js"),
		"--config", ch.configPath,
		noAutoConfigFlag,
		"source", "refresh",
	}
	if !slices.Equal(ch.spawnReqs[0].Args, want) {
		t.Errorf("rebake args = %v, want %v", ch.spawnReqs[0].Args, want)
	}
}

func TestDispatchExplicitConfigFarmMissingManifest(t *testing.T) {
	t.Run("invoked through the farm directory", func(t *testing.T) {
		ch := newConfigHarness(t)
		// The repository has a perfectly good farm declaring the name. Falling
		// back to it is the silent failure this must not produce.
		ch.writeManifest(sourcefarm.Manifest{
			Entries: []sourcefarm.Entry{{Name: "tofu", Command: ch.tool("tofu-project"), Installed: true}},
		})
		ch.invokeThroughConfigFarm("tofu")

		code, handled := ch.d.Dispatch()

		if !handled || code != ExitNotFound {
			t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
		}
		if len(ch.execs) != 0 {
			t.Errorf("Dispatch() ran %v for a farm with no manifest", ch.execs)
		}
		if ch.getwdCalls != 0 {
			t.Errorf("Dispatch() called Getwd %d times; a config farm must not fall back to git discovery", ch.getwdCalls)
		}
		for _, want := range []string{"tofu", "no readable manifest", ch.configFarmDir, "--config"} {
			if !strings.Contains(ch.stderr.String(), want) {
				t.Errorf("stderr %q does not mention %q", ch.stderr.String(), want)
			}
		}
	})

	t.Run("named by a dangling activation variable", func(t *testing.T) {
		ch := newConfigHarness(t)
		gone := filepath.Join(ch.base, "cache", env.ConfigFarmsDirName, "vanished", "bin")
		ch.activate(gone)
		ch.d.Args = []string{filepath.Join(gone, "tofu")}

		code, handled := ch.d.Dispatch()

		if !handled || code != ExitNotFound {
			t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
		}
		if !strings.Contains(ch.stderr.String(), "no readable manifest") {
			t.Errorf("stderr %q does not explain the dangling farm", ch.stderr.String())
		}
	})

	t.Run("recognizable only through the activation variable", func(t *testing.T) {
		ch := newConfigHarness(t)
		// Both faults at once: the cache root this process computes disagrees
		// with the one activation computed, so the farm is a farm only because
		// the variable says so, and its manifest is gone. The origin cannot be
		// read, and the repository's own farm must not be what answers instead.
		ch.writeManifest(sourcefarm.Manifest{
			Entries: []sourcefarm.Entry{{Name: "tofu", Command: ch.tool("tofu-project"), Installed: true}},
		})
		ch.d.CacheRoot = func() string { return filepath.Join(ch.base, "other-cache") }
		ch.activate(ch.configFarmDir)
		ch.invokeThroughConfigFarm("tofu")

		code, handled := ch.d.Dispatch()

		if !handled || code != ExitNotFound {
			t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
		}
		if len(ch.execs) != 0 {
			t.Errorf("Dispatch() ran %v for a farm with no manifest", ch.execs)
		}
		if ch.getwdCalls != 0 {
			t.Errorf("Dispatch() called Getwd %d times; a dangling farm must not fall back to git discovery", ch.getwdCalls)
		}
		if !strings.Contains(ch.stderr.String(), "no readable manifest") {
			t.Errorf("stderr %q does not explain the unreadable manifest", ch.stderr.String())
		}
	})
}

// writeWrongOriginConfigManifest stores a manifest beside the config farm that
// decodes cleanly but records a git-root origin — what a truncated write, a hand
// edit or a build with another schema leaves behind.
func (ch *configHarness) writeWrongOriginConfigManifest() {
	ch.t.Helper()
	data, err := sourcefarm.Encode(sourcefarm.Manifest{
		FormatVersion: sourcefarm.ManifestFormatVersion,
		Origin:        sourcefarm.OriginGitRoot,
		Root:          ch.root,
		FarmDir:       ch.configFarmDir,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Entries:       []sourcefarm.Entry{{Name: "tofu", Command: ch.tool("tofu-machine"), Installed: true}},
	})
	if err != nil {
		ch.t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(ch.configManifestPath, data, 0o600); err != nil {
		ch.t.Fatalf("write manifest: %v", err)
	}
}

// TestDispatchConfigFarmWrongOrigin pins that a decodable manifest is not a
// substitute for a *correct* one. A farm known to be machine-level whose
// manifest records a git-root origin must fail as loudly as one with no manifest
// at all, rather than hand the name to git discovery — and it must do so through
// either way the farm is known to be machine-level, the cache namespace or the
// variables the activated shell exported.
func TestDispatchConfigFarmWrongOrigin(t *testing.T) {
	run := func(t *testing.T, prepare func(ch *configHarness)) {
		t.Helper()
		ch := newConfigHarness(t)

		// The repository the shell stands in has a good farm declaring the name.
		// Answering from it is the silent cross-boundary failure under test.
		ch.writeManifest(sourcefarm.Manifest{
			Entries: []sourcefarm.Entry{{Name: "tofu", Command: ch.tool("tofu-project"), Installed: true}},
		})
		ch.writeWrongOriginConfigManifest()
		prepare(ch)
		ch.invokeThroughConfigFarm("tofu")

		code, handled := ch.d.Dispatch()

		if !handled || code != ExitNotFound {
			t.Fatalf("Dispatch() = (%d, %v), want (%d, true); stderr: %s", code, handled, ExitNotFound, ch.stderr.String())
		}
		if len(ch.execs) != 0 {
			t.Errorf("Dispatch() ran %v for a config farm with a wrong-origin manifest", ch.execs)
		}
		if ch.getwdCalls != 0 {
			t.Errorf("Dispatch() called Getwd %d times; a config farm must not fall back to git discovery", ch.getwdCalls)
		}
		for _, want := range []string{"tofu", "different origin", ch.configFarmDir, "--config"} {
			if !strings.Contains(ch.stderr.String(), want) {
				t.Errorf("stderr %q does not mention %q", ch.stderr.String(), want)
			}
		}
	}

	t.Run("recognized through the config namespace", func(t *testing.T) {
		run(t, func(*configHarness) {})
	})

	// The same manifest, in a farm this process cannot place in its own cache:
	// a DATAMITSU_CACHE_DIR or HOME that resolves differently than at activation
	// leaves the exported variables as the only thing that says machine-level.
	// The origin check must hold there too — this is precisely the invocation
	// that already depends on the variable to be seen as a farm invocation.
	t.Run("recognized through the activation variables", func(t *testing.T) {
		run(t, func(ch *configHarness) {
			ch.d.CacheRoot = func() string { return filepath.Join(ch.base, "other-cache") }
			ch.activateConfig(ch.configFarmDir)
		})
	})
}

// TestDispatchUnrecognizedFarmGitRootManifestFallsThrough pins the other side of
// that rule: with neither the namespace nor the config variables saying
// machine-level, a git-root manifest is taken at face value and the ordinary
// git-root path answers. Only DATAMITSU_FARM_CONFIG distinguishes this fixture
// from the one above, so it is what the origin check keys on.
func TestDispatchUnrecognizedFarmGitRootManifestFallsThrough(t *testing.T) {
	ch := newConfigHarness(t)
	tool := ch.tool("tofu-project")
	ch.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: tool, Installed: true}},
	})
	ch.writeWrongOriginConfigManifest()
	ch.d.CacheRoot = func() string { return filepath.Join(ch.base, "other-cache") }
	ch.activate(ch.configFarmDir)
	ch.invokeThroughConfigFarm("tofu")

	code, handled := ch.d.Dispatch()

	if !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true); stderr: %s", code, handled, ch.stderr.String())
	}
	if len(ch.execs) != 1 || ch.execs[0].path != tool {
		t.Fatalf("Dispatch() ran %v, want the project farm's %s", ch.execs, tool)
	}
}

func TestDispatchExplicitConfigFarmFoundThroughActivationVariable(t *testing.T) {
	ch := newConfigHarness(t)
	tool := ch.tool("tofu")
	ch.writeConfigManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: tool, Installed: true}},
	})
	// The cache root this process computes is not the one activation computed,
	// so the farm directory is recognizable only through the exported variable.
	ch.d.CacheRoot = func() string { return filepath.Join(ch.base, "other-cache") }
	ch.activate(ch.configFarmDir)
	ch.invokeThroughConfigFarm("tofu")

	code, handled := ch.d.Dispatch()

	if !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true); stderr: %s", code, handled, ch.stderr.String())
	}
	if len(ch.execs) != 1 || ch.execs[0].path != tool {
		t.Fatalf("Dispatch() ran %v, want %s", ch.execs, tool)
	}
	if ch.getwdCalls != 0 {
		t.Errorf("Dispatch() called Getwd %d times", ch.getwdCalls)
	}
}

func TestDispatchExplicitConfigUnknownNameExits127(t *testing.T) {
	ch := newConfigHarness(t)
	// The repository declares the name; the machine-level config does not. A
	// PATH search would find it, which is exactly what must not happen.
	ch.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "terragrunt", Command: ch.tool("terragrunt"), Installed: true}},
	})
	ch.writeConfigManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: ch.tool("tofu"), Installed: true}},
	})
	ch.invokeThroughConfigFarm("terragrunt", "plan")

	code, handled := ch.d.Dispatch()

	if !handled {
		t.Fatal("Dispatch() declined a config farm invocation; PATH would fall through to a system binary")
	}
	if code != ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, ExitNotFound)
	}
	if len(ch.execs) != 0 {
		t.Errorf("Dispatch() ran %v for a name the config farm does not declare", ch.execs)
	}
	if ch.getwdCalls != 0 {
		t.Errorf("Dispatch() called Getwd %d times while declining", ch.getwdCalls)
	}
	if !strings.Contains(ch.stderr.String(), ch.configPath) {
		t.Errorf("stderr %q does not name the config chain the farm was baked from", ch.stderr.String())
	}
}

func TestDispatchExplicitConfigRetiredFarmNamesTheConfigFlag(t *testing.T) {
	ch := newConfigHarness(t)
	ch.writeConfigManifest(sourcefarm.Manifest{
		// A format version this build retired: it cannot be served back, and the
		// rebake below is made to fail.
		FormatVersion: sourcefarm.ManifestFormatVersion - 1,
		Entries:       []sourcefarm.Entry{{Name: "tofu", Command: ch.tool("tofu"), Installed: true}},
	})
	ch.d.Validate = func(sourcefarm.Manifest) bool { return false }
	ch.spawnFunc = func([]string) error { return os.ErrPermission }
	ch.invokeThroughConfigFarm("tofu")

	code, handled := ch.d.Dispatch()

	if !handled || code != ExitNotFound {
		t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
	}
	if strings.Contains(ch.stderr.String(), "in that repository") {
		t.Errorf("stderr %q tells the user to cd to a repository that does not exist", ch.stderr.String())
	}
	if !strings.Contains(ch.stderr.String(), "--config "+ch.configPath) {
		t.Errorf("stderr %q does not spell the refresh command for a config farm", ch.stderr.String())
	}
}

func TestConfigFarmDirectoryIsStrippedFromChildPath(t *testing.T) {
	ch := newConfigHarness(t)
	tool := ch.tool("tofu")
	ch.writeConfigManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: tool, Installed: true}},
	})
	ch.d.Environ = func() []string {
		return []string{"PATH=" + ch.configFarmDir + string(os.PathListSeparator) + "/usr/bin"}
	}
	ch.d.Validate = func(sourcefarm.Manifest) bool { return false }
	ch.invokeThroughConfigFarm("tofu")

	if _, handled := ch.d.Dispatch(); !handled {
		t.Fatalf("Dispatch() declined a config farm entry; stderr: %s", ch.stderr.String())
	}
	if len(ch.spawnReqs) != 1 {
		t.Fatalf("Dispatch() spawned %v, want one refresh", ch.spawns)
	}
	for _, kv := range ch.spawnReqs[0].Environ {
		if strings.HasPrefix(kv, "PATH=") && strings.Contains(kv, ch.configFarmDir) {
			t.Errorf("child PATH %q still contains the config farm; a bare-name subprocess could re-enter the shim", kv)
		}
	}
}

// TestDispatchUnreadableProjectManifestNeverFallsBackToTheConfigFarm covers the
// one state where the two farms could be confused: PATH resolved the name
// through the project farm, but that farm's manifest is missing or corrupt,
// while a machine-level farm is also active and declares the same name.
//
// The answer must be the git-root path's loud decline, not the machine-level
// tool. Substituting it would exec a different binary than the one PATH chose,
// silently — the exact wrong-binary failure the whole farm exists to prevent,
// and worse than a decline because it exits 0 and prints plausible output.
func TestDispatchUnreadableProjectManifestNeverFallsBackToTheConfigFarm(t *testing.T) {
	ch := newConfigHarness(t)

	machineTool := ch.tool("tofu-machine")
	ch.writeConfigManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: machineTool, Installed: true}},
	})

	// The project farm exists on disk and PATH selected it; its manifest does
	// not decode.
	if err := os.WriteFile(ch.manifestPath, []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("write a corrupt project manifest: %v", err)
	}
	ch.activate(ch.configFarmDir)
	ch.invokeThroughFarm("tofu")

	code, handled := ch.d.Dispatch()

	if !handled || code != ExitNotFound {
		t.Fatalf("Dispatch() = (%d, %v), want (%d, true); stderr: %s", code, handled, ExitNotFound, ch.stderr.String())
	}
	if len(ch.execs) != 0 {
		t.Fatalf("Dispatch() ran %v, want nothing at all", ch.execs)
	}
	if strings.Contains(ch.stderr.String(), machineTool) {
		t.Errorf("the machine-level farm answered for a project-farm invocation:\n%s", ch.stderr.String())
	}
}

// TestRefreshHintQuotesAChainThatNeedsIt pins the one command a user is given
// when a machine-level farm is broken and cannot repair itself. An ordinary
// path stays readable; a path with a space is quoted, because unquoted it would
// split into two arguments and name neither config.
func TestRefreshHintQuotesAChainThatNeedsIt(t *testing.T) {
	plain := refreshHint(sourcefarm.Manifest{
		Origin:      sourcefarm.OriginExplicitConfig,
		ConfigPaths: []string{"/home/u/tools.config.ts"},
	})
	if !strings.Contains(plain, "--config /home/u/tools.config.ts --force") {
		t.Errorf("an ordinary path was not spelled plainly: %s", plain)
	}

	spaced := refreshHint(sourcefarm.Manifest{
		Origin:      sourcefarm.OriginExplicitConfig,
		ConfigPaths: []string{"/home/u/My Configs/tools.config.ts"},
	})
	if strings.Contains(spaced, "--config /home/u/My Configs/") {
		t.Errorf("a path with a space was left unquoted: %s", spaced)
	}
	if !strings.Contains(spaced, "My Configs") {
		t.Errorf("the quoted hint no longer names the config: %s", spaced)
	}
}
