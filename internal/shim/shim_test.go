package shim

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

type execCall struct {
	path    string
	argv    []string
	environ []string
}

// harness builds a Dispatcher over a real temp tree — a git root, a farm
// directory and a manifest beside it — with the process-replacing and
// process-spawning dependencies stubbed so a test can assert what would have
// happened.
type harness struct {
	t            *testing.T
	base         string
	root         string
	farmDir      string
	manifestPath string
	stderr       bytes.Buffer

	execs  []execCall
	spawns [][]string
	// spawnExes records the executable each spawn was pointed at. It is what a
	// test asserting the anti-loop rule reads: the args of a re-entrant spawn
	// look exactly like a correct one.
	spawnExes []string

	// spawnFunc runs on every Spawn call; the default succeeds and does nothing.
	spawnFunc func(args []string) error

	d *Dispatcher
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	// The temp dir is resolved through symlinks because that is what production
	// does: the bake keys the farm on the physical git root (facts resolves the
	// cwd, and `git rev-parse --show-toplevel` reports a physical path), so
	// discoverRoots resolves too. On macOS t.TempDir() hands back a /var path
	// that is really /private/var, and a harness pinned to the unresolved form
	// would assert the bug rather than the behaviour.
	base := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	root := filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("create git root: %v", err)
	}
	cacheRoot := filepath.Join(base, "cache")
	project := filepath.Join(cacheRoot, "projects", "abc")
	farmDir := filepath.Join(project, "bin")
	if err := os.MkdirAll(farmDir, 0o700); err != nil {
		t.Fatalf("create farm dir: %v", err)
	}

	h := &harness{
		t:            t,
		base:         base,
		root:         root,
		farmDir:      farmDir,
		manifestPath: filepath.Join(project, env.ProjectManifestFileName),
	}

	h.d = &Dispatcher{
		Getwd:      func() (string, error) { return root, nil },
		Executable: func() (string, error) { return filepath.Join(base, "datamitsu"), nil },
		Environ:    func() []string { return []string{"PATH=/usr/bin", "HOME=/home/u"} },
		ManifestPath: func(gitRoot string) (string, error) {
			if gitRoot != root {
				return "", errors.New("unexpected root " + gitRoot)
			}
			return h.manifestPath, nil
		},
		CacheRoot: func() string { return cacheRoot },
		Load:      sourcefarm.Load,
		Validate:  func(sourcefarm.Manifest) bool { return true },
		Stat:      os.Stat,
		Exec: func(path string, argv, environ []string) error {
			h.execs = append(h.execs, execCall{path: path, argv: argv, environ: environ})
			return nil
		},
		EvalSymlinks: filepath.EvalSymlinks,
		Spawn: func(exe string, args []string) error {
			h.spawns = append(h.spawns, args)
			h.spawnExes = append(h.spawnExes, exe)
			if h.spawnFunc != nil {
				return h.spawnFunc(args)
			}
			return nil
		},
		Stderr: &h.stderr,
	}
	return h
}

// writeManifest stores a manifest at the harness's manifest path.
func (h *harness) writeManifest(m sourcefarm.Manifest) {
	h.t.Helper()
	m.Root = h.root
	m.FarmDir = h.farmDir
	data, err := sourcefarm.Encode(m)
	if err != nil {
		h.t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(h.manifestPath, data, 0o600); err != nil {
		h.t.Fatalf("write manifest: %v", err)
	}
}

// tool creates an executable file standing in for a store binary.
func (h *harness) tool(name string) string {
	h.t.Helper()
	path := filepath.Join(h.t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		h.t.Fatalf("write tool: %v", err)
	}
	return path
}

// invokeThroughFarm sets argv to a farm symlink invocation.
func (h *harness) invokeThroughFarm(name string, args ...string) {
	h.t.Helper()
	link := filepath.Join(h.farmDir, name)
	if _, err := os.Lstat(link); err != nil {
		if err := os.WriteFile(link, []byte("shim"), 0o755); err != nil {
			h.t.Fatalf("create farm entry: %v", err)
		}
	}
	h.d.Args = append([]string{link}, args...)
}

func TestDispatchDeclinesOwnName(t *testing.T) {
	h := newHarness(t)
	h.d.Args = []string{"/usr/local/bin/datamitsu", "exec", "tofu"}

	code, handled := h.d.Dispatch()

	if handled {
		t.Fatalf("Dispatch() handled a datamitsu invocation (code %d)", code)
	}
	if len(h.execs) != 0 {
		t.Errorf("Dispatch() ran %v for its own name", h.execs)
	}
}

func TestDispatchDeclinesRenamedBinary(t *testing.T) {
	h := newHarness(t)
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: "/store/tofu", Installed: true}},
	})
	// Not a farm path: a datamitsu binary somebody copied to another name.
	h.d.Args = []string{filepath.Join(t.TempDir(), "datamitsu-dev"), "--help"}

	_, handled := h.d.Dispatch()

	if handled {
		t.Fatal("Dispatch() handled a renamed datamitsu binary; the CLI would be unreachable")
	}
	if len(h.execs) != 0 {
		t.Errorf("Dispatch() ran %v for a renamed binary", h.execs)
	}
}

func TestDispatchUnknownNameThroughFarmExits127(t *testing.T) {
	h := newHarness(t)
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: "/store/tofu", Installed: true}},
	})
	h.invokeThroughFarm("terragrunt", "plan")

	code, handled := h.d.Dispatch()

	if !handled {
		t.Fatal("Dispatch() declined a farm invocation; PATH would fall through to a system binary")
	}
	if code != ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, ExitNotFound)
	}
	if len(h.execs) != 0 {
		t.Errorf("Dispatch() ran %v for an undeclared name", h.execs)
	}
	for _, want := range []string{"terragrunt", "not declared", h.root, "datamitsu source status"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr %q does not mention %q", h.stderr.String(), want)
		}
	}
}

func TestDispatchExcludedNameReportsReason(t *testing.T) {
	h := newHarness(t)
	h.writeManifest(sourcefarm.Manifest{
		Excluded: []sourcefarm.Excluded{{Name: "echo", Reason: sourcefarm.ReasonShellApp}},
	})
	h.invokeThroughFarm("echo", "hi")

	code, handled := h.d.Dispatch()

	if !handled || code != ExitNotFound {
		t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
	}
	if !strings.Contains(h.stderr.String(), sourcefarm.ReasonShellApp) {
		t.Errorf("stderr %q does not carry the exclusion reason", h.stderr.String())
	}
}

func TestDispatchNoManifestThroughFarmExits127(t *testing.T) {
	h := newHarness(t)
	// No manifest written: an activated shell that cd'ed into a repository that
	// has never been activated. Baking implicitly would evaluate that
	// repository's JavaScript without the user ever activating it.
	h.invokeThroughFarm("tofu")

	code, handled := h.d.Dispatch()

	if !handled || code != ExitNotFound {
		t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
	}
	if len(h.spawns) != 0 {
		t.Errorf("Dispatch() spawned %v; a missing farm must not be baked implicitly", h.spawns)
	}
	if !strings.Contains(h.stderr.String(), "datamitsu source") {
		t.Errorf("stderr %q does not tell the user how to activate", h.stderr.String())
	}
}

func TestDispatchNoManifestNotThroughFarmRunsCLI(t *testing.T) {
	h := newHarness(t)
	h.d.Args = []string{filepath.Join(t.TempDir(), "tofu")}

	_, handled := h.d.Dispatch()

	if handled {
		t.Fatal("Dispatch() handled an invocation that reached no farm and no manifest")
	}
}

func TestDispatchOutsideGitRepository(t *testing.T) {
	t.Run("through a farm it fails loudly", func(t *testing.T) {
		h := newHarness(t)
		outside := t.TempDir()
		h.d.Getwd = func() (string, error) { return outside, nil }
		h.invokeThroughFarm("tofu")

		code, handled := h.d.Dispatch()

		if !handled || code != ExitNotFound {
			t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
		}
		if !strings.Contains(h.stderr.String(), "git repository") {
			t.Errorf("stderr %q does not explain the missing repository", h.stderr.String())
		}
	})

	t.Run("otherwise it runs the CLI", func(t *testing.T) {
		h := newHarness(t)
		outside := t.TempDir()
		h.d.Getwd = func() (string, error) { return outside, nil }
		h.d.Args = []string{filepath.Join(outside, "dm")}

		if _, handled := h.d.Dispatch(); handled {
			t.Fatal("Dispatch() handled a non-farm invocation outside a repository")
		}
	})
}

func TestDispatchPassesArgvVerbatim(t *testing.T) {
	h := newHarness(t)
	tool := h.tool("tofu")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: tool, Installed: true, Strategy: sourcefarm.StrategySymlink}},
	})
	userArgs := []string{"--version", "an arg with spaces", "quote'and\"quote", "line\nbreak", "-"}
	h.invokeThroughFarm("tofu", userArgs...)

	code, handled := h.d.Dispatch()

	if !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true)", code, handled)
	}
	if len(h.execs) != 1 {
		t.Fatalf("exec calls = %d, want 1", len(h.execs))
	}
	got := h.execs[0]
	if got.path != tool {
		t.Errorf("exec path = %q, want %q", got.path, tool)
	}
	// argv[0] is the name the user typed, not the store path being exec'd: a
	// tool's own usage line and error prefixes come from argv[0], and printing a
	// content-addressed cache path there is both ugly and unstable.
	want := append([]string{"tofu"}, userArgs...)
	if !equalStrings(got.argv, want) {
		t.Errorf("argv = %q, want %q", got.argv, want)
	}
}

func TestDispatchPrependsArgsAndMergesEnv(t *testing.T) {
	h := newHarness(t)
	java := h.tool("java")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{
			Name:      "spectral",
			Command:   java,
			Args:      []string{"-jar", "/store/spectral.jar"},
			Env:       map[string]string{"JAVA_HOME": "/store/jdk", "PATH": "/store/jdk/bin:/usr/bin"},
			Installed: true,
		}},
	})
	h.invokeThroughFarm("spectral", "lint", "api.yaml")

	if code, handled := h.d.Dispatch(); !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true)", code, handled)
	}

	got := h.execs[0]
	// An entry with Args execs an interpreter, and the interpreter owns argv[0]
	// by its own convention: `java -jar …` expects "java" there, not "spectral".
	want := []string{"java", "-jar", "/store/spectral.jar", "lint", "api.yaml"}
	if !equalStrings(got.argv, want) {
		t.Errorf("argv = %q, want %q", got.argv, want)
	}
	// The entry's PATH is prepended to the inherited one, not substituted for
	// it, and a directory already present is not repeated: /usr/bin is inherited
	// and also named by the overlay, so it appears once.
	wantEnv := []string{"HOME=/home/u", "JAVA_HOME=/store/jdk", "PATH=/store/jdk/bin:/usr/bin"}
	if !equalStrings(got.environ, wantEnv) {
		t.Errorf("env = %q, want %q", got.environ, wantEnv)
	}
}

// TestDispatchPrependsRatherThanReplacesPATH is the property that keeps a baked
// manifest usable from a shell other than the one that baked it.
//
// A manifest is written once and replayed by every later shell. An entry whose
// PATH was captured at bake time would pin the baking shell's environment —
// which, for a per-shell version manager, names a directory that stops existing
// when that shell exits — and would drop whatever the calling shell actually
// has.
func TestDispatchPrependsRatherThanReplacesPATH(t *testing.T) {
	h := newHarness(t)
	node := h.tool("eslint")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{
			Name:      "eslint",
			Command:   node,
			Env:       map[string]string{"PATH": "/store/node/bin"},
			Installed: true,
		}},
	})
	h.invokeThroughFarm("eslint")

	if code, handled := h.d.Dispatch(); !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true)", code, handled)
	}

	var gotPath string
	for _, kv := range h.execs[0].environ {
		if key, value, ok := strings.Cut(kv, "="); ok && key == "PATH" {
			gotPath = value
		}
	}
	// The harness's inherited PATH is /usr/bin; it must survive, behind the
	// runtime's own directory.
	if want := "/store/node/bin:/usr/bin"; gotPath != want {
		t.Errorf("PATH = %q, want %q", gotPath, want)
	}
}

func TestDispatchStaleManifestRebakesOnce(t *testing.T) {
	h := newHarness(t)
	oldTool := h.tool("tofu-v1")
	newTool := h.tool("tofu-v2")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: oldTool, Installed: true}},
	})
	h.d.Validate = func(sourcefarm.Manifest) bool { return false }
	h.spawnFunc = func(args []string) error {
		if args[0] == "source" {
			h.writeManifest(sourcefarm.Manifest{
				Entries: []sourcefarm.Entry{{Name: "tofu", Command: newTool, Installed: true}},
			})
		}
		return nil
	}
	h.invokeThroughFarm("tofu", "plan")

	if code, handled := h.d.Dispatch(); !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true)", code, handled)
	}

	if len(h.spawns) != 1 || !equalStrings(h.spawns[0], []string{"source", "refresh"}) {
		t.Fatalf("spawns = %v, want exactly one [source refresh]", h.spawns)
	}
	if len(h.execs) != 1 || h.execs[0].path != newTool {
		t.Fatalf("exec = %v, want one call to %q", h.execs, newTool)
	}
}

func TestDispatchRebakeFailureKeepsPreviousFarm(t *testing.T) {
	h := newHarness(t)
	tool := h.tool("tofu")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: tool, Installed: true}},
	})
	h.d.Validate = func(sourcefarm.Manifest) bool { return false }
	h.spawnFunc = func([]string) error { return errors.New("offline") }
	h.invokeThroughFarm("tofu")

	if code, handled := h.d.Dispatch(); !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true)", code, handled)
	}
	if len(h.execs) != 1 || h.execs[0].path != tool {
		t.Fatalf("exec = %v, want the previous farm's %q", h.execs, tool)
	}
	if !strings.Contains(h.stderr.String(), "offline") {
		t.Errorf("stderr %q does not report the failed refresh", h.stderr.String())
	}
}

func TestDispatchRebakeDroppingTheEntryExits127(t *testing.T) {
	h := newHarness(t)
	tool := h.tool("tofu")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: tool, Installed: true}},
	})
	h.d.Validate = func(sourcefarm.Manifest) bool { return false }
	h.spawnFunc = func([]string) error {
		// The branch that made the manifest stale also removed the app.
		h.writeManifest(sourcefarm.Manifest{})
		return nil
	}
	h.invokeThroughFarm("tofu")

	code, handled := h.d.Dispatch()

	if !handled || code != ExitNotFound {
		t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
	}
	if len(h.execs) != 0 {
		t.Errorf("Dispatch() ran %v for an app the project no longer declares", h.execs)
	}
}

func TestDispatchInstallsOnDemandExactlyOnce(t *testing.T) {
	h := newHarness(t)
	target := filepath.Join(t.TempDir(), "tflint")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tflint", Command: target, Installed: false, Strategy: sourcefarm.StrategyShim}},
	})
	h.spawnFunc = func(args []string) error {
		if args[0] == "install" {
			return os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755)
		}
		return nil
	}
	h.invokeThroughFarm("tflint", "--version")

	if code, handled := h.d.Dispatch(); !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true)", code, handled)
	}
	if len(h.spawns) != 1 || !equalStrings(h.spawns[0], []string{"install", "tflint"}) {
		t.Fatalf("spawns = %v, want exactly one [install tflint]", h.spawns)
	}
	if len(h.execs) != 1 || h.execs[0].path != target {
		t.Fatalf("exec = %v, want one call to %q", h.execs, target)
	}
}

func TestDispatchInstallWithoutRecordedPathRefreshes(t *testing.T) {
	h := newHarness(t)
	target := filepath.Join(t.TempDir(), "prettier")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "prettier", Installed: false, Strategy: sourcefarm.StrategyShim}},
	})
	h.spawnFunc = func(args []string) error {
		switch args[0] {
		case "install":
			return os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755)
		case "source":
			h.writeManifest(sourcefarm.Manifest{
				Entries: []sourcefarm.Entry{{Name: "prettier", Command: target, Installed: true}},
			})
		}
		return nil
	}
	h.invokeThroughFarm("prettier", "--check", ".")

	if code, handled := h.d.Dispatch(); !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true)", code, handled)
	}
	if len(h.spawns) != 2 {
		t.Fatalf("spawns = %v, want an install followed by a forced refresh", h.spawns)
	}
	if !equalStrings(h.spawns[1], []string{"source", "refresh", "--force"}) {
		t.Errorf("second spawn = %v, want [source refresh --force]", h.spawns[1])
	}
	if len(h.execs) != 1 || h.execs[0].path != target {
		t.Fatalf("exec = %v, want one call to %q", h.execs, target)
	}
}

// TestDispatchBareArgv0ResolvesThroughPATH is the shape every real invocation
// has. A shell that finds a command on PATH execs it with argv[0] set to the
// word the user typed — "tofu", not the farm path it was found at — so a farm
// invocation is indistinguishable from a renamed binary by argv[0] alone.
// Getting this wrong is silent: every loud failure (D4's exit 127 among them)
// degrades into an ordinary CLI run that prints datamitsu's help and exits 0
// while holding a tool's arguments.
func TestDispatchBareArgv0ResolvesThroughPATH(t *testing.T) {
	t.Run("found in the farm, fails loudly", func(t *testing.T) {
		h := newHarness(t)
		h.writeManifest(sourcefarm.Manifest{
			Entries: []sourcefarm.Entry{{Name: "tofu", Command: "/store/tofu", Installed: true}},
		})
		// The farm holds the name; the system copy is further down PATH.
		system := t.TempDir()
		writeExecutable(t, filepath.Join(h.farmDir, "terragrunt"))
		writeExecutable(t, filepath.Join(system, "terragrunt"))
		h.d.Environ = func() []string { return []string{"PATH=" + h.farmDir + ":" + system} }
		h.d.Args = []string{"terragrunt", "plan"}

		code, handled := h.d.Dispatch()

		if !handled || code != ExitNotFound {
			t.Fatalf("Dispatch() = (%d, %v), want (%d, true); PATH would fall through to %s",
				code, handled, ExitNotFound, system)
		}
	})

	t.Run("found outside a farm, runs the CLI", func(t *testing.T) {
		h := newHarness(t)
		system := t.TempDir()
		writeExecutable(t, filepath.Join(system, "datamitsu-dev"))
		h.d.Environ = func() []string { return []string{"PATH=" + system + ":" + h.farmDir} }
		h.d.Args = []string{"datamitsu-dev", "--help"}

		if _, handled := h.d.Dispatch(); handled {
			t.Fatal("Dispatch() handled a binary that PATH resolves outside any farm")
		}
	})
}

// writeExecutable creates an executable file, creating parent directories.
func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestDispatchThroughFarmIsDecidedBeforeARebake pins the ordering. The config
// change that makes a manifest stale is usually the one that dropped the app,
// and the rebake deletes the farm entry that answers "was this a farm
// invocation?" — so the answer has to be taken first, or D4's exit 127 turns
// back into a PATH fall-through at the worst possible moment.
func TestDispatchThroughFarmIsDecidedBeforeARebake(t *testing.T) {
	h := newHarness(t)
	entry := filepath.Join(h.farmDir, "tofu")
	writeExecutable(t, entry)
	h.d.Environ = func() []string { return []string{"PATH=" + h.farmDir} }
	h.d.Args = []string{"tofu"}
	h.d.Validate = func(sourcefarm.Manifest) bool { return false }
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: "/store/tofu", Installed: true}},
	})
	// The rebake drops the app, exactly as a branch that stops declaring it does.
	h.spawnFunc = func([]string) error {
		if err := os.Remove(entry); err != nil {
			return err
		}
		h.writeManifest(sourcefarm.Manifest{})
		return nil
	}

	code, handled := h.d.Dispatch()

	if !handled || code != ExitNotFound {
		t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
	}
	if !strings.Contains(h.stderr.String(), "not declared") {
		t.Errorf("stderr %q does not report the app as undeclared", h.stderr.String())
	}
}

// TestDispatchSpawnResolvesTheFarmSymlink pins the anti-loop rule. A farm entry
// is a symlink to the datamitsu binary, and os.Executable reports the path the
// process was invoked through rather than the file behind it on darwin — so the
// executable this process would naively spawn is the tool's own farm entry.
// Spawning it re-enters dispatch under the tool's name, and the install that was
// supposed to happen becomes an exec loop that only ends when the process table
// does. The real-shell tier found this; this test is what keeps it fixed.
func TestDispatchSpawnResolvesTheFarmSymlink(t *testing.T) {
	h := newHarness(t)
	realExe := filepath.Join(h.base, "datamitsu")
	if err := os.WriteFile(realExe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write the datamitsu executable: %v", err)
	}
	link := filepath.Join(h.farmDir, "tflint")
	if err := os.Symlink(realExe, link); err != nil {
		t.Fatalf("create the farm symlink: %v", err)
	}
	// What darwin's os.Executable reports for a process reached through the farm.
	h.d.Executable = func() (string, error) { return link, nil }

	target := filepath.Join(t.TempDir(), "tflint")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tflint", Command: target, Installed: false, Strategy: sourcefarm.StrategyShim}},
	})
	h.spawnFunc = func(args []string) error {
		if args[0] == "install" {
			return os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755)
		}
		return nil
	}
	h.invokeThroughFarm("tflint", "--version")

	if code, handled := h.d.Dispatch(); !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true)", code, handled)
	}
	if len(h.spawnExes) != 1 {
		t.Fatalf("spawns = %v, want exactly one", h.spawnExes)
	}
	// Compared through EvalSymlinks: on macOS a temp path is itself reached via
	// /var -> /private/var, and that difference is not what this test is about.
	wantExe, err := filepath.EvalSymlinks(realExe)
	if err != nil {
		t.Fatalf("resolve the datamitsu executable: %v", err)
	}
	if h.spawnExes[0] != wantExe {
		t.Errorf("spawned %q, want the datamitsu binary %q — spawning a farm entry is an exec loop",
			h.spawnExes[0], wantExe)
	}
}

// TestDispatchRefusesToSpawnAFarmEntry covers the case resolution cannot save:
// the executable path still points inside a farm after symlinks are resolved.
// There is no safe program to spawn, so the invocation fails loudly instead of
// forking itself.
func TestDispatchRefusesToSpawnAFarmEntry(t *testing.T) {
	h := newHarness(t)
	// A regular file in the farm: EvalSymlinks succeeds and returns it unchanged.
	entry := filepath.Join(h.farmDir, "tflint")
	if err := os.WriteFile(entry, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write the farm entry: %v", err)
	}
	h.d.Executable = func() (string, error) { return entry, nil }
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tflint", Installed: false, Strategy: sourcefarm.StrategyShim}},
	})
	h.invokeThroughFarm("tflint")

	code, handled := h.d.Dispatch()

	if !handled || code != ExitNotFound {
		t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
	}
	if len(h.spawns) != 0 {
		t.Fatalf("spawned %v from inside the farm; that is the exec loop", h.spawns)
	}
	if !strings.Contains(h.stderr.String(), "farm entry") {
		t.Errorf("stderr %q does not explain the refusal", h.stderr.String())
	}
}

func TestDispatchInstallFailureExits127(t *testing.T) {
	h := newHarness(t)
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tflint", Installed: false}},
	})
	h.spawnFunc = func([]string) error { return errors.New("no network") }
	h.invokeThroughFarm("tflint")

	code, handled := h.d.Dispatch()

	if !handled || code != ExitNotFound {
		t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
	}
	if len(h.execs) != 0 {
		t.Errorf("Dispatch() ran %v after a failed install", h.execs)
	}
	if !strings.Contains(h.stderr.String(), "no network") {
		t.Errorf("stderr %q does not report the install failure", h.stderr.String())
	}
}

func TestDispatchMissingTargetIsReportedNotRun(t *testing.T) {
	h := newHarness(t)
	missing := filepath.Join(t.TempDir(), "tofu")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: missing, Installed: true}},
	})
	// The install path runs and still produces nothing: the store entry is gone.
	h.invokeThroughFarm("tofu")

	code, handled := h.d.Dispatch()

	if !handled || code != ExitNotFound {
		t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
	}
	if len(h.execs) != 0 {
		t.Errorf("Dispatch() ran %v with a missing target", h.execs)
	}
}

func TestDispatchExecFailureExits126(t *testing.T) {
	h := newHarness(t)
	tool := h.tool("tofu")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: tool, Installed: true}},
	})
	h.d.Exec = func(string, []string, []string) error { return errors.New("exec format error") }
	h.invokeThroughFarm("tofu")

	code, handled := h.d.Dispatch()

	if !handled || code != ExitNotExecutable {
		t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotExecutable)
	}
}

func TestDispatchUsesTheManifestForTheCurrentDirectory(t *testing.T) {
	// An activated shell keeps repo A's farm on PATH after cd'ing into repo B.
	// The manifest that decides what runs is the one for the *current* tree.
	h := newHarness(t)
	other := filepath.Join(t.TempDir(), "other-repo")
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatalf("create second repo: %v", err)
	}
	h.d.Getwd = func() (string, error) { return other, nil }
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: h.tool("tofu"), Installed: true}},
	})
	h.invokeThroughFarm("tofu")

	code, handled := h.d.Dispatch()

	if !handled || code != ExitNotFound {
		t.Fatalf("Dispatch() = (%d, %v), want (%d, true)", code, handled, ExitNotFound)
	}
	if len(h.execs) != 0 {
		t.Errorf("Dispatch() ran %v from another repository's farm", h.execs)
	}
}

func TestDispatchIgnoresABinDirectoryOutsideTheCache(t *testing.T) {
	// A directory that merely happens to be called bin is not a farm. Treating
	// it as one would turn `/opt/tool/bin/tofu` into an exit 127 for a binary
	// datamitsu has nothing to do with.
	h := newHarness(t)
	h.writeManifest(sourcefarm.Manifest{})
	elsewhere := filepath.Join(t.TempDir(), "opt", "bin")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	h.d.Args = []string{filepath.Join(elsewhere, "tofu")}

	if _, handled := h.d.Dispatch(); handled {
		t.Fatal("Dispatch() treated an unrelated bin directory as a farm")
	}
}

func TestDispatchSpawnsTheRunningExecutable(t *testing.T) {
	// Never a PATH lookup: the farm is on PATH, so a lookup could find a
	// shimmed name and turn the rebake spawn into a loop.
	h := newHarness(t)
	tool := h.tool("tofu")
	h.writeManifest(sourcefarm.Manifest{
		Entries: []sourcefarm.Entry{{Name: "tofu", Command: tool, Installed: true}},
	})
	h.d.Validate = func(sourcefarm.Manifest) bool { return false }

	wantExe := filepath.Join(t.TempDir(), "the-real-datamitsu")
	h.d.Executable = func() (string, error) { return wantExe, nil }
	var gotExe string
	h.d.Spawn = func(exe string, args []string) error {
		gotExe = exe
		h.spawns = append(h.spawns, args)
		return nil
	}
	h.invokeThroughFarm("tofu")

	if code, handled := h.d.Dispatch(); !handled || code != 0 {
		t.Fatalf("Dispatch() = (%d, %v), want (0, true)", code, handled)
	}
	if gotExe != wantExe {
		t.Errorf("spawned %q, want the running executable %q", gotExe, wantExe)
	}
}

func TestInvokedName(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"empty argv", nil, ""},
		{"bare name", []string{"tofu"}, "tofu"},
		{"absolute path", []string{"/cache/projects/a/bin/tofu"}, "tofu"},
		{"trailing slash", []string{"/some/dir/"}, "dir"},
		{"root", []string{"/"}, ""},
		{"dot", []string{"."}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := invokedName(tt.args); got != tt.want {
				t.Errorf("invokedName(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestInvokedNameStripsWindowsSuffix(t *testing.T) {
	// filepath.Base is platform-specific, so the suffix rule is asserted on a
	// plain base name rather than on a Windows path.
	if got := invokedName([]string{"tofu.exe"}); got != "tofu" {
		t.Errorf("invokedName(tofu.exe) = %q, want %q", got, "tofu")
	}
}

func TestMergeEnv(t *testing.T) {
	tests := []struct {
		name    string
		base    []string
		overlay map[string]string
		want    []string
	}{
		{
			name: "no overlay returns the base unchanged",
			base: []string{"B=2", "A=1"},
			want: []string{"B=2", "A=1"},
		},
		{
			name:    "overlay wins and the result is sorted",
			base:    []string{"LANG=C", "HOME=/h"},
			overlay: map[string]string{"LANG": "en_US.UTF-8"},
			want:    []string{"HOME=/h", "LANG=en_US.UTF-8"},
		},
		{
			// PATH is the one key that is prepended rather than replaced: the
			// caller's own PATH must survive a farm entry's runtime directories.
			name:    "PATH is prepended, not replaced",
			base:    []string{"PATH=/usr/bin:/bin", "HOME=/h"},
			overlay: map[string]string{"PATH": "/store/bin"},
			want:    []string{"HOME=/h", "PATH=/store/bin:/usr/bin:/bin"},
		},
		{
			name:    "a directory the overlay already names is not repeated",
			base:    []string{"PATH=/usr/bin:/bin"},
			overlay: map[string]string{"PATH": "/store/bin:/usr/bin"},
			want:    []string{"PATH=/store/bin:/usr/bin:/bin"},
		},
		{
			name:    "an empty inherited PATH leaves just the overlay",
			base:    []string{"PATH="},
			overlay: map[string]string{"PATH": "/store/bin"},
			want:    []string{"PATH=/store/bin"},
		},
		{
			name:    "an empty overlay PATH leaves the inherited one",
			base:    []string{"PATH=/usr/bin"},
			overlay: map[string]string{"PATH": ""},
			want:    []string{"PATH=/usr/bin"},
		},
		{
			name:    "new keys are added",
			base:    []string{"HOME=/h"},
			overlay: map[string]string{"UV_CACHE_DIR": "/c"},
			want:    []string{"HOME=/h", "UV_CACHE_DIR=/c"},
		},
		{
			name:    "malformed base entries are dropped",
			base:    []string{"HOME=/h", "NOT_AN_ASSIGNMENT"},
			overlay: map[string]string{"A": "1"},
			want:    []string{"A=1", "HOME=/h"},
		},
		{
			name:    "an empty value is preserved",
			base:    []string{"A=1"},
			overlay: map[string]string{"A": ""},
			want:    []string{"A="},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeEnv(tt.base, tt.overlay); !equalStrings(got, tt.want) {
				t.Errorf("mergeEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// BenchmarkDispatch pins the cost of the steady-state shim path — the work every
// invocation of every shimmed tool pays. It exists so a future change that adds
// a config load, a lock, or a second process to this path shows up as a number
// rather than as a slow shell.
func BenchmarkDispatch(b *testing.B) {
	base := b.TempDir()
	root := filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		b.Fatalf("create git root: %v", err)
	}
	cacheRoot := filepath.Join(base, "cache")
	project := filepath.Join(cacheRoot, "projects", "abc")
	farmDir := filepath.Join(project, "bin")
	if err := os.MkdirAll(farmDir, 0o700); err != nil {
		b.Fatalf("create farm dir: %v", err)
	}
	tool := filepath.Join(base, "tofu")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		b.Fatalf("write tool: %v", err)
	}
	manifestPath := filepath.Join(project, env.ProjectManifestFileName)

	entries := make([]sourcefarm.Entry, 0, 100)
	for i := range 100 {
		entries = append(entries, sourcefarm.Entry{
			Name:      "app" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Command:   tool,
			Installed: true,
		})
	}
	entries = append(entries, sourcefarm.Entry{Name: "tofu", Command: tool, Installed: true})
	data, err := sourcefarm.Encode(sourcefarm.Manifest{Root: root, FarmDir: farmDir, Entries: entries})
	if err != nil {
		b.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		b.Fatalf("write manifest: %v", err)
	}

	// The invocation is shaped the way a shell makes it: a bare name in argv[0]
	// and a PATH whose first entry is the farm, so the resolution the dispatcher
	// has to redo is part of the measurement.
	if err := os.WriteFile(filepath.Join(farmDir, "tofu"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		b.Fatalf("write farm entry: %v", err)
	}
	environ := append(os.Environ(), "PATH="+farmDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	d := &Dispatcher{
		Args:         []string{"tofu", "plan"},
		Getwd:        func() (string, error) { return root, nil },
		Executable:   func() (string, error) { return filepath.Join(base, "datamitsu"), nil },
		Environ:      func() []string { return environ },
		ManifestPath: func(string) (string, error) { return manifestPath, nil },
		CacheRoot:    func() string { return cacheRoot },
		Load:         sourcefarm.Load,
		Validate:     func(sourcefarm.Manifest) bool { return true },
		Stat:         os.Stat,
		Exec:         func(string, []string, []string) error { return nil },
		Spawn:        func(string, []string) error { return errors.New("unexpected spawn") },
		Stderr:       io.Discard,
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, handled := d.Dispatch(); !handled {
			b.Fatal("Dispatch() declined the benchmark invocation")
		}
	}
}
