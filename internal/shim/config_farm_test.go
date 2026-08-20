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
