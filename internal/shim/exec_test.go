//go:build !windows

package shim

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

// These tests exercise the real syscall.Exec path, which by construction cannot
// be observed from the process that performs it: the process image is replaced.
// The dispatch therefore runs in a child — this same test binary, re-invoked
// with helperEnv set — and the parent asserts on what the ran program did
// with the stdio, the exit code and the argv it was given.

const (
	helperEnv      = "DATAMITSU_SHIM_TEST_HELPER"
	helperArgv0    = "DATAMITSU_SHIM_TEST_ARGV0"
	helperArgs     = "DATAMITSU_SHIM_TEST_ARGS"
	helperCwd      = "DATAMITSU_SHIM_TEST_CWD"
	helperManifest = "DATAMITSU_SHIM_TEST_MANIFEST"
	helperCache    = "DATAMITSU_SHIM_TEST_CACHE"
)

// TestShimHelperProcess is not a test. It is the child process the tests below
// spawn: it performs a real dispatch and, on the success path, never returns.
func TestShimHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("helper process; runs only when re-invoked by another test")
	}

	d := New()
	d.Args = []string{os.Getenv(helperArgv0)}
	var userArgs []string
	if err := json.Unmarshal([]byte(os.Getenv(helperArgs)), &userArgs); err != nil {
		t.Fatalf("decode helper args: %v", err)
	}
	d.Args = append(d.Args, userArgs...)
	cwd := os.Getenv(helperCwd)
	d.Getwd = func() (string, error) { return cwd, nil }
	manifest := os.Getenv(helperManifest)
	d.ManifestPath = func(string) (string, error) { return manifest, nil }
	cache := os.Getenv(helperCache)
	d.CacheRoot = func() string { return cache }
	// Freshness is Task 6's concern and has its own tests; what is under test
	// here is the exec itself.
	d.Validate = func(sourcefarm.Manifest) bool { return true }

	code, handled := d.Dispatch()
	if !handled {
		// A distinctive code: "the dispatcher declined" must never be mistaken
		// for a target's own exit status.
		os.Exit(99)
	}
	os.Exit(code)
}

// execFixture is a repository, a farm and a manifest whose single entry runs a
// shell script the test controls.
type execFixture struct {
	root         string
	cacheRoot    string
	farmDir      string
	manifestPath string
	script       string
}

func newExecFixture(t *testing.T, name, body string) execFixture {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh is unavailable; the real-exec path is unverified on this machine")
	}

	base := t.TempDir()
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

	script := filepath.Join(base, name+"-real")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	// The farm entry itself is a symlink to the datamitsu executable in
	// production; here only its path matters, because the child is started
	// directly and argv[0] is set by hand.
	if err := os.WriteFile(filepath.Join(farmDir, name), []byte("shim"), 0o755); err != nil {
		t.Fatalf("create farm entry: %v", err)
	}

	manifestPath := filepath.Join(project, "manifest.json")
	data, err := sourcefarm.Encode(sourcefarm.Manifest{
		Root:    root,
		FarmDir: farmDir,
		Entries: []sourcefarm.Entry{{Name: name, Command: script, Installed: true}},
	})
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	return execFixture{root: root, cacheRoot: cacheRoot, farmDir: farmDir, manifestPath: manifestPath, script: script}
}

// run dispatches name with args in a child process and returns its streams and
// exit code.
func (f execFixture) run(t *testing.T, name string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	// The argv is passed as JSON rather than a delimited string: the arguments
	// under test include a newline and an empty string, and an environment
	// variable cannot carry a NUL.
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("encode helper args: %v", err)
	}

	// #nosec G204 -- the test binary re-invoking itself.
	cmd := exec.Command(os.Args[0], "-test.run=TestShimHelperProcess")
	cmd.Env = append(os.Environ(),
		helperEnv+"=1",
		helperArgv0+"="+filepath.Join(f.farmDir, name),
		helperArgs+"="+string(encodedArgs),
		helperCwd+"="+f.root,
		helperManifest+"="+f.manifestPath,
		helperCache+"="+f.cacheRoot,
	)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	switch exitErr, ok := errors.AsType[*exec.ExitError](err); {
	case ok:
		code = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("run helper: %v (stderr %q)", err, errBuf.String())
	}
	return outBuf.String(), errBuf.String(), code
}

func TestExecPreservesExitCode(t *testing.T) {
	for _, want := range []int{0, 1, 42, 127} {
		t.Run("exit-"+strconv.Itoa(want), func(t *testing.T) {
			f := newExecFixture(t, "coder", "#!/bin/sh\nexit "+strconv.Itoa(want)+"\n")
			_, stderr, code := f.run(t, "coder")
			if code != want {
				t.Errorf("exit code = %d, want %d (stderr %q)", code, want, stderr)
			}
		})
	}
}

func TestExecStreamsAreTheTargets(t *testing.T) {
	f := newExecFixture(t, "talker", "#!/bin/sh\nprintf 'to stdout'\nprintf 'to stderr' >&2\nexit 0\n")

	stdout, stderr, code := f.run(t, "talker")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if stdout != "to stdout" {
		t.Errorf("stdout = %q, want %q with nothing added by the shim", stdout, "to stdout")
	}
	if stderr != "to stderr" {
		t.Errorf("stderr = %q, want %q with nothing added by the shim", stderr, "to stderr")
	}
}

func TestExecPassesArgvVerbatim(t *testing.T) {
	// Every argument is printed inside brackets so a trailing newline or an
	// empty argument is visible in the comparison.
	f := newExecFixture(t, "argv", "#!/bin/sh\nfor a in \"$@\"; do printf '[%s]' \"$a\"; done\n")

	args := []string{"--version", "with space", "with'quote", "with\"quote", "with\nnewline", "-", ""}
	stdout, stderr, code := f.run(t, "argv", args...)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	var want strings.Builder
	for _, a := range args {
		want.WriteString("[" + a + "]")
	}
	if stdout != want.String() {
		t.Errorf("argv seen by the tool = %q, want %q", stdout, want.String())
	}
}

func TestExecPassesEntryEnvironment(t *testing.T) {
	f := newExecFixture(t, "overlay", "#!/bin/sh\nprintf '%s' \"$DATAMITSU_SHIM_TEST_OVERLAY\"\n")

	// Rewrite the manifest with an env overlay on the entry.
	data, err := sourcefarm.Encode(sourcefarm.Manifest{
		Root:    f.root,
		FarmDir: f.farmDir,
		Entries: []sourcefarm.Entry{{
			Name:      "overlay",
			Command:   f.script,
			Env:       map[string]string{"DATAMITSU_SHIM_TEST_OVERLAY": "from-the-manifest"},
			Installed: true,
		}},
	})
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(f.manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	stdout, stderr, code := f.run(t, "overlay")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if stdout != "from-the-manifest" {
		t.Errorf("overlay variable = %q, want %q", stdout, "from-the-manifest")
	}
}
