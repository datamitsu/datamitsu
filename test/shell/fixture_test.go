package shell_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/gitenv"
	"github.com/datamitsu/datamitsu/internal/target"
)

// toolName is the fake tool every test in this tier runs. It is not a
// deny-listed name, not a shell app, and not something a developer machine is
// likely to have installed — so a test that accidentally resolves it through the
// system PATH fails rather than passing against the wrong binary.
const toolName = "stub-tool"

// The branches of the fixture repository. v1 and v2 pin different versions of
// the same tool; none declares no apps at all, which is what a branch that drops
// a tool looks like to source mode.
const (
	branchV1   = "v1"
	branchV2   = "v2"
	branchNone = "none"
)

// versionOf maps a branch to the version string its pinned stub prints.
var versionOf = map[string]string{branchV1: "1.0.0", branchV2: "2.0.0"}

// The machine-level toolchain: a config outside every repository, named with
// --config from a shell rc file. It pins the same tool at a version no branch
// uses, so "which farm answered" is readable straight off the output, and one
// tool no repository declares at all, which is how a machine-level farm stays
// useful from inside a project.
const (
	machineVersion  = "9.9.9"
	machineOnlyTool = "stub-only"
)

// impostorOutput is what the same-named binary planted later on PATH prints. It
// must never appear in any test's output: every time it does, source mode has
// fallen through to a system binary, which is the exact failure the feature
// exists to prevent.
const impostorOutput = "IMPOSTOR"

// stubScript renders the fake tool. It is a /bin/sh script rather than a
// compiled binary because the shim execs it through execve either way, and a
// script keeps the fixture readable — the interpreter line also exercises the
// stat-before-execve rule, where ENOENT is ambiguous between a missing script
// and a missing interpreter.
func stubScript(name, version string) string {
	return "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --exit) exit \"$2\" ;;\n" +
		"  --argv) shift; for a in \"$@\"; do printf '<%s>\\n' \"$a\"; done; exit 0 ;;\n" +
		"esac\n" +
		"printf '" + name + " %s\\n' '" + version + "'\n"
}

// sha256Hex is the artifact hash the config declares. Downloads are verified
// against it for real: this tier serves over loopback, but nothing about the
// verification path is stubbed out.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// stubApp is one binary app a rendered config declares: a name, the loopback URL
// its artifact is served from, and the SHA-256 that artifact must verify against.
type stubApp struct{ Name, URL, Hash string }

// configJS renders a config declaring each app as a binary app for the host
// target only. The libc dimension is filled in for all three values so the
// fixture does not depend on how the host's libc was detected.
func configJS(apps ...stubApp) string {
	host := target.DetectHost(context.Background())
	var entries strings.Builder
	for _, app := range apps {
		var libcs strings.Builder
		for _, libc := range []string{"glibc", "musl", "unknown"} {
			fmt.Fprintf(&libcs, "        %s: { url: %q, hash: %q, contentType: \"binary\" },\n", libc, app.URL, app.Hash)
		}
		fmt.Fprintf(&entries, `    %q: { binary: { binaries: {
      %s: {
        %s: {
%s        },
      },
    } } },
`, app.Name, host.OS, host.Arch, libcs.String())
	}
	return fmt.Sprintf(`globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  apps: {
%s  },
  tools: {},
  projectTypes: {},
});
globalThis.getMinVersion = () => "0.0.0";
`, entries.String())
}

// poisonedConfigJS throws the moment it is evaluated. It is how the trust
// boundary is proved rather than assumed: a machine-level shim that ever
// resolved a repository's config would have to evaluate this file, and the
// failure would be loud instead of inferred from an absence.
const poisonMarker = "PROJECT-CONFIG-EVALUATED"

const poisonedConfigJS = `throw new Error("` + poisonMarker + `");
`

// emptyConfigJS is the config on the `none` branch: a real, valid project that
// simply declares no apps.
const emptyConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({ apps: {}, tools: {}, projectTypes: {} });
globalThis.getMinVersion = () => "0.0.0";
`

// fixture is a two-branch git repository, a loopback release host serving the
// stub tool, an isolated cache, and a directory holding a same-named impostor
// planted on PATH.
type fixture struct {
	t     *testing.T
	Dir   string // the git root, symlink-resolved
	Cache string // DATAMITSU_CACHE_DIR
	Plant string // a directory on PATH holding an impostor named toolName
	srv   *httptest.Server

	// The machine-level config, materialized on first use by machineConfig().
	// MachineDir is a directory git knows nothing about — no repository above it
	// either — which is the only place the outside-a-repository case can be
	// proved from.
	MachineDir string
	machineCfg string
}

// newFixture builds the fixture with a plain temp cache directory.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithCache(t, t.TempDir())
}

// with returns a copy of the fixture bound to t, and every per-shell subtest
// must start with it.
//
// A fixture is built once per top-level test and shared across the subtests, so
// the t it captured is the parent's. Reporting against a parent from inside a
// subtest does not skip or fail that subtest: Skipf and Fatalf call
// runtime.Goexit in the subtest's goroutine, which Go reports as "test executed
// panic(nil) or runtime.Goexit: subtest may have called FailNow on a parent
// test" — a failure, not a skip. That is what a machine without zsh saw, which
// is every Linux CI runner and no developer laptop.
//
// The copy shares the release host and the directories; they stay owned by the
// parent's Cleanup, which is what keeps the server alive for the later shells.
func (f *fixture) with(t *testing.T) *fixture {
	t.Helper()
	bound := *f
	bound.t = t
	return &bound
}

// newFixtureWithCache builds the fixture with a caller-chosen cache directory,
// which is how the hostile-path test gets a farm directory containing a space,
// a single quote and a glob character.
func newFixtureWithCache(t *testing.T, cache string) *fixture {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlinks for the fixture repository: %v", err)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatalf("create the cache directory: %v", err)
	}
	cache, err = filepath.EvalSymlinks(cache)
	if err != nil {
		t.Fatalf("eval symlinks for the cache directory: %v", err)
	}

	f := &fixture{t: t, Dir: dir, Cache: cache}
	f.startReleaseHost()
	f.plantImpostor()
	f.initRepository()
	return f
}

// startReleaseHost serves each branch's stub at its own path. It stands in for
// the release host the config's URLs point at; the download, the SHA-256 check
// and the store write that follow are the production ones.
func (f *fixture) startReleaseHost() {
	mux := http.NewServeMux()
	serve := func(path, body string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(body))
		})
	}
	for branch, version := range versionOf {
		serve("/"+branch+"/"+toolName, stubScript(toolName, version))
	}
	// The machine-level tier serves both its tools from one path prefix: the
	// version they print is what tells a machine-level answer apart from a
	// project one.
	for _, name := range machineTools {
		serve("/machine/"+name, stubScript(name, machineVersion))
	}
	f.srv = httptest.NewServer(mux)
	f.t.Cleanup(f.srv.Close)
}

// plantImpostor writes a same-named executable into a directory that is on PATH
// but behind the farm. Every assertion that the pinned version ran is only
// meaningful because this exists: without it, "the right output" and "no output
// at all from the wrong binary" look identical.
func (f *fixture) plantImpostor() {
	f.t.Helper()
	f.Plant = filepath.Join(f.t.TempDir(), "system-bin")
	if err := os.MkdirAll(f.Plant, 0o700); err != nil {
		f.t.Fatalf("create the impostor directory: %v", err)
	}
	script := "#!/bin/sh\nprintf '" + impostorOutput + "\\n'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(f.Plant, toolName), []byte(script), 0o700); err != nil {
		f.t.Fatalf("plant the impostor: %v", err)
	}
}

// initRepository creates the three branches and leaves the tree on v1.
func (f *fixture) initRepository() {
	f.t.Helper()
	f.git("init", "-q")
	if top := f.gitOut("rev-parse", "--show-toplevel"); top != f.Dir {
		f.t.Fatalf("git init did not create a repository at %s (toplevel = %q)", f.Dir, top)
	}
	f.git("config", "user.name", "datamitsu-shell-test")
	f.git("config", "user.email", "shell-test@datamitsu.invalid")

	f.writeFile("Makefile", ".PHONY: version\nversion:\n\t@"+toolName+"\n")
	f.writeConfigFor(branchV1)
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "v1")
	f.git("branch", "-M", branchV1)

	f.git("checkout", "-q", "-b", branchV2)
	f.writeConfigFor(branchV2)
	f.git("commit", "-q", "-a", "-m", "v2")

	f.git("checkout", "-q", "-b", branchNone)
	f.writeFile("datamitsu.config.js", emptyConfigJS)
	f.git("commit", "-q", "-a", "-m", "none")

	f.git("checkout", "-q", branchV1)
}

// writeConfigFor writes the config pinning the given branch's stub.
func (f *fixture) writeConfigFor(branch string) {
	f.t.Helper()
	url := f.srv.URL + "/" + branch + "/" + toolName
	f.writeFile("datamitsu.config.js", configJS(stubApp{
		Name: toolName,
		URL:  url,
		Hash: sha256Hex(stubScript(toolName, versionOf[branch])),
	}))
}

// machineTools are the apps the machine-level config declares. toolName collides
// with the repository's on purpose; machineOnlyTool exists nowhere else.
var machineTools = []string{toolName, machineOnlyTool}

// machineConfig writes the machine-level config into a directory outside every
// repository and returns its path. It is created once per fixture, so the chain
// — and therefore the farm identity — is the same for every activation a test
// performs.
func (f *fixture) machineConfig() string {
	f.t.Helper()
	if f.machineCfg != "" {
		return f.machineCfg
	}
	d := clitest.NewBareDir(f.t)
	apps := make([]stubApp, 0, len(machineTools))
	for _, name := range machineTools {
		apps = append(apps, stubApp{
			Name: name,
			URL:  f.srv.URL + "/machine/" + name,
			Hash: sha256Hex(stubScript(name, machineVersion)),
		})
	}
	f.MachineDir = d.Dir
	f.machineCfg = d.WriteFile("machine.config.js", configJS(apps...))
	return f.machineCfg
}

// poisonProjectConfig replaces the repository's config with one that throws on
// evaluation, leaving the tree dirty on purpose. Any command that resolves this
// repository's config fails loudly from here on.
func (f *fixture) poisonProjectConfig() {
	f.t.Helper()
	f.writeFile("datamitsu.config.js", poisonedConfigJS)
}

// projectFarms lists the per-git-root farm directories in the isolated cache.
// A machine-level activation must never create one: baking a project farm is
// what evaluating a repository's config looks like on disk.
func (f *fixture) projectFarms() []string {
	f.t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.Cache, "cache", "projects"))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func (f *fixture) writeFile(rel, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.Dir, rel), []byte(content), 0o600); err != nil {
		f.t.Fatalf("write %s: %v", rel, err)
	}
}

// fixtureGitEnv is gitenv.Environ with the developer's own git configuration
// taken out of the picture.
//
// gitenv.Environ only strips git's hook variables, so the fixture still
// inherited ~/.gitconfig — and a contributor who signs commits got
// "Enter passphrase ... fatal: failed to write commit object" instead of a
// passing tier. This is invisible in CI, where no signing key is configured,
// and this tier is not build-tagged, so it runs on every `go test ./...`.
// Pointing both config scopes at the null device also keeps templates, hook
// paths and init.defaultBranch out; the fixture sets its own identity and
// creates its branches explicitly, so it needs nothing from either scope.
func fixtureGitEnv() []string {
	return append(gitenv.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)
}

func (f *fixture) git(args ...string) {
	f.t.Helper()
	// G204: fixed test-controlled git subcommands, not untrusted input.
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = f.Dir
	cmd.Env = fixtureGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func (f *fixture) gitOut(args ...string) string {
	f.t.Helper()
	// G204: fixed test-controlled git subcommands, not untrusted input.
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = f.Dir
	cmd.Env = fixtureGitEnv()
	out, err := cmd.Output()
	if err != nil {
		f.t.Fatalf("git %v failed: %v", args, err)
	}
	resolved, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		return strings.TrimSpace(string(out))
	}
	return resolved
}

// env returns the environment every subprocess in this tier runs with: the
// harness's hermetic base, with the offline guard lifted so the loopback release
// host is reachable, and a PATH that holds the impostor plus whatever the system
// needs for git, make and /bin/sh.
func (f *fixture) env() []string {
	base := clitest.BaseEnv(f.Cache)
	// BaseEnv pins DATAMITSU_OFFLINE=1; an empty value is how env.Offline reads
	// "not set", and appending overrides on key collision.
	return append(base,
		"DATAMITSU_OFFLINE=",
		"PATH="+f.Plant+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SHELL=/bin/sh",
	)
}

// datamitsu runs the CLI in the fixture repository.
func (f *fixture) datamitsu(args ...string) clitest.Result {
	f.t.Helper()
	return f.datamitsuIn(f.Dir, args...)
}

// datamitsuIn runs the CLI in an arbitrary directory, which is how the
// machine-level activation runs from outside every repository.
func (f *fixture) datamitsuIn(dir string, args ...string) clitest.Result {
	f.t.Helper()
	return clitest.Run(f.t, clitest.RunOptions{
		Dir:      dir,
		CacheDir: f.Cache,
		Env: []string{
			"DATAMITSU_OFFLINE=",
			"PATH=" + f.Plant + string(os.PathListSeparator) + os.Getenv("PATH"),
			"SHELL=/bin/sh",
		},
	}, args...)
}

// activation returns the shell code `datamitsu source <shell>` emits, failing
// the test if the command did not succeed. Baking the farm is a side effect of
// this call, exactly as it is for a user.
func (f *fixture) activation(shell string) string {
	f.t.Helper()
	res := f.datamitsu("source", shell)
	if res.ExitCode != 0 {
		f.t.Fatalf("`datamitsu source %s` exit = %d\nstderr:\n%s", shell, res.ExitCode, res.Stderr)
	}
	return res.Stdout
}

// machineActivation returns the shell code a rc file gets for the machine-level
// toolchain: `datamitsu source <shell> --config <path>`, run from outside every
// repository so nothing about the result can come from a discovered config.
func (f *fixture) machineActivation(shell string) string {
	f.t.Helper()
	cfg := f.machineConfig()
	res := f.datamitsuIn(f.MachineDir, "source", shell, "--config", cfg)
	if res.ExitCode != 0 {
		f.t.Fatalf("`datamitsu source %s --config %s` exit = %d\nstderr:\n%s", shell, cfg, res.ExitCode, res.Stderr)
	}
	return res.Stdout
}

// farmDir returns the farm directory the project activation points the shell at.
func (f *fixture) farmDir() string {
	f.t.Helper()
	return f.farmDirOf(f.activation("bash"))
}

// machineFarmDir is farmDir for the machine-level farm.
func (f *fixture) machineFarmDir() string {
	f.t.Helper()
	return f.farmDirOf(f.machineActivation("bash"))
}

// farmDirOf reads the farm directory out of a bash activation block.
func (f *fixture) farmDirOf(activation string) string {
	f.t.Helper()
	for line := range strings.SplitSeq(activation, "\n") {
		rest, ok := strings.CutPrefix(line, "export DATAMITSU_FARM=")
		if !ok {
			continue
		}
		return strings.Trim(strings.TrimPrefix(rest, "$"), "'")
	}
	f.t.Fatal("the activation declares no farm directory")
	return ""
}

// shellResult is the outcome of running a script in a real shell.
type shellResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// run executes body in the named shell with the fixture's environment, after
// the activation the same shell would have been given. The shell starts with no
// user configuration: a developer's rc file is not part of the contract.
func (f *fixture) run(shell, body string) shellResult {
	f.t.Helper()
	script := f.activation(shell) + body
	return f.runRaw(shell, script)
}

// runRaw executes a script with no activation prepended, in the repository.
func (f *fixture) runRaw(shell, script string) shellResult {
	f.t.Helper()
	return f.runRawIn(shell, f.Dir, script)
}

// runRawIn executes a script with no activation prepended, in an arbitrary
// directory — the machine-level cases need both a cwd outside every repository
// and a cwd deep inside one that was never activated.
func (f *fixture) runRawIn(shell, dir, script string) shellResult {
	f.t.Helper()
	bin := requireShell(f.t, shell)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var args []string
	switch shell {
	case "bash":
		args = []string{"--noprofile", "--norc", "-c", script}
	case "zsh":
		// --no-rcs is zsh's spelling of bash's --noprofile --norc: no startup
		// file may put anything on PATH that the test did not.
		args = []string{"--no-rcs", "-c", script}
	case "fish":
		args = []string{"--no-config", "-c", script}
	default:
		f.t.Fatalf("unsupported shell %q", shell)
	}

	// G204: bin is a shell resolved from PATH by the test, script is test-owned.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = f.env()
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		f.t.Fatalf("%s script timed out:\n%s\n--- stdout ---\n%s\n--- stderr ---\n%s",
			shell, script, stdout.String(), stderr.String())
	}
	return shellResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: clitest.ExitCodeOf(err)}
}

// requireShell resolves a shell or skips the test, naming the property that goes
// unverified on this machine — a silent skip is how a tier stops testing
// anything without anybody noticing.
func requireShell(t *testing.T, shell string) string {
	t.Helper()
	bin, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("%s is not installed: source mode's shell integration (transparent versions, "+
			"branch switching and lazy materialization) is unverified on this machine", shell)
	}
	return bin
}

// shells is the set every property is proved against.
//
// zsh is listed even though it shares bash's renderer and test/cli asserts the
// two outputs are byte-identical. Identical output is not identical execution:
// zsh's rules for pattern matching inside ${p//pat/rep} differ from bash's, and
// that substitution is what keeps re-activation from growing PATH. Running it is
// the only way to know. requireShell skips cleanly where zsh is absent.
var shells = []string{"bash", "zsh", "fish"}

// assertRan fails unless the tool printed exactly the version the given branch
// pins — and never the impostor's output.
func assertRan(t *testing.T, res shellResult, branch string) {
	t.Helper()
	assertRanTool(t, res, toolName, versionOf[branch])
}

// assertRanTool is assertRan for a name and version that no branch defines — the
// machine-level farm pins the same tool at a version of its own, and declares one
// tool no repository has.
func assertRanTool(t *testing.T, res shellResult, name, version string) {
	t.Helper()
	want := name + " " + version + "\n"
	if res.ExitCode != 0 {
		t.Fatalf("running %s exited %d\nstdout:\n%s\nstderr:\n%s", name, res.ExitCode, res.Stdout, res.Stderr)
	}
	if strings.Contains(res.Stdout, impostorOutput) {
		t.Fatalf("PATH fell through to the system binary:\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	}
	if res.Stdout != want {
		t.Fatalf("stdout = %q, want %q\nstderr:\n%s", res.Stdout, want, res.Stderr)
	}
}

// storeEntries lists the tool's content-addressed store files, which is how the
// tests tell "installed" from "not installed" without asking datamitsu.
func (f *fixture) storeEntries() []string {
	f.t.Helper()
	dir := filepath.Join(f.Cache, "store", ".bin", toolName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, filepath.Join(dir, e.Name()))
	}
	return names
}
