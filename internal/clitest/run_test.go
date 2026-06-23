package clitest

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestRunVersionExitZero(t *testing.T) {
	res := Run(t, RunOptions{}, "version")
	if res.ExitCode != 0 {
		t.Fatalf("version exit = %d, want 0 (stderr: %q)", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "version") {
		t.Fatalf("version stdout = %q, want it to contain %q", res.Stdout, "version")
	}
	// The version line goes to stdout; stderr must be clean (coverage dir exists,
	// so no emit warnings leak through).
	if strings.TrimSpace(res.Stderr) != "" {
		t.Fatalf("version stderr = %q, want empty", res.Stderr)
	}
}

func TestRunUnknownCommandFails(t *testing.T) {
	res := Run(t, RunOptions{}, "definitely-not-a-command")
	if res.ExitCode == 0 {
		t.Fatalf("unknown command exit = 0, want non-zero")
	}
	if !strings.Contains(res.Stderr, "unknown command") {
		t.Fatalf("unknown command stderr = %q, want it to mention %q", res.Stderr, "unknown command")
	}
	// The error belongs on stderr, not stdout.
	if strings.Contains(res.Stdout, "unknown command") {
		t.Fatalf("error leaked to stdout: %q", res.Stdout)
	}
}

func TestRunSeparatesStreams(t *testing.T) {
	// version writes only to stdout; an unknown command writes only to stderr.
	// Proving both directions confirms the two streams are captured separately.
	ok := Run(t, RunOptions{}, "version")
	if ok.Stdout == "" || strings.TrimSpace(ok.Stderr) != "" {
		t.Fatalf("expected stdout-only for version, got stdout=%q stderr=%q", ok.Stdout, ok.Stderr)
	}
	bad := Run(t, RunOptions{}, "definitely-not-a-command")
	if strings.TrimSpace(bad.Stdout) != "" || bad.Stderr == "" {
		t.Fatalf("expected stderr-only for bad command, got stdout=%q stderr=%q", bad.Stdout, bad.Stderr)
	}
}

func TestRunStdinAndDir(t *testing.T) {
	// A run in an explicit working directory still succeeds; stdin is accepted
	// even when the command ignores it. This exercises the Dir/Stdin plumbing.
	dir := t.TempDir()
	res := Run(t, RunOptions{Dir: dir, Stdin: "ignored input\n"}, "version")
	if res.ExitCode != 0 {
		t.Fatalf("version in dir exit = %d, want 0 (stderr: %q)", res.ExitCode, res.Stderr)
	}
}

func TestBaseEnvStripsAndSetsVars(t *testing.T) {
	// Inherited DATAMITSU_*, CI and TERM must not survive into the clean env.
	t.Setenv("DATAMITSU_VERBOSE", "1")
	t.Setenv("CI", "true")
	t.Setenv("TERM", "xterm-256color")

	env := BaseEnv("/tmp/some-cache")
	got := envMap(env)

	for _, stripped := range []string{"DATAMITSU_VERBOSE", "CI", "TERM"} {
		if _, ok := got[stripped]; ok {
			t.Errorf("BaseEnv leaked stripped var %q", stripped)
		}
	}

	want := map[string]string{
		"NO_COLOR":            "1",
		"DATAMITSU_CACHE_DIR": "/tmp/some-cache",
		"DATAMITSU_OFFLINE":   "1",
		"DATAMITSU_NO_OCI":    "1",
		"GOCOVERDIR":          CoverDir(),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("BaseEnv[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestBaseEnvHasNoDuplicateSetKeys(t *testing.T) {
	// Even if a set key is also inherited, BaseEnv must emit exactly one entry
	// for it (the harness value), so child processes see an unambiguous value.
	t.Setenv("NO_COLOR", "0")
	t.Setenv("GOCOVERDIR", "/inherited/cover")

	counts := map[string]int{}
	for _, kv := range BaseEnv("/tmp/cache") {
		key, _, _ := strings.Cut(kv, "=")
		counts[key]++
	}
	for _, k := range []string{"NO_COLOR", "GOCOVERDIR", "DATAMITSU_CACHE_DIR", "DATAMITSU_OFFLINE", "DATAMITSU_NO_OCI"} {
		if counts[k] != 1 {
			t.Errorf("BaseEnv has %d entries for %q, want 1", counts[k], k)
		}
	}
}

func TestExitCodeOf(t *testing.T) {
	if got := ExitCodeOf(nil); got != 0 {
		t.Errorf("ExitCodeOf(nil) = %d, want 0", got)
	}
	if got := ExitCodeOf(errors.New("plain non-exit error")); got != -1 {
		t.Errorf("ExitCodeOf(plain) = %d, want -1", got)
	}
	// A real non-zero process exit must surface its code.
	err := exec.Command("false").Run()
	if got := ExitCodeOf(err); got != 1 {
		t.Errorf("ExitCodeOf(false) = %d, want 1", got)
	}
}

// envMap collapses a KEY=VALUE slice into the last-value-wins map a child
// process would observe.
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		key, val, _ := strings.Cut(kv, "=")
		m[key] = val
	}
	return m
}
