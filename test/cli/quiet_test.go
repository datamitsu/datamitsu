package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestInitQuietReportsOnlyManagedChanges(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	opts := clitest.RunOptions{Dir: p.Dir, CacheDir: t.TempDir()}
	run := func() clitest.Result {
		t.Helper()
		return clitest.Run(t, opts, "--no-auto-config", "--config", cfg, "init", "--quiet")
	}

	first := run()
	if first.ExitCode != 0 {
		t.Fatalf("quiet init exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", first.ExitCode, first.Stdout, first.Stderr)
	}
	artifact := filepath.Join(p.Dir, ".datamitsu", "datamitsu.config.d.ts")
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("quiet init did not create type-definition artifact %s: %v", artifact, err)
	}
	if first.Stdout == "" || strings.Contains(first.Stdout, "┏") || strings.Contains(first.Stdout, "┗") {
		t.Errorf("first quiet init should report a concise unframed change on stdout:\n%s", first.Stdout)
	}
	if first.Stderr != "" {
		t.Errorf("first quiet init wrote to stderr:\n%s", first.Stderr)
	}

	second := run()
	if second.ExitCode != 0 {
		t.Fatalf("second quiet init exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", second.ExitCode, second.Stdout, second.Stderr)
	}
	if second.Stdout != "" || second.Stderr != "" {
		t.Errorf("unchanged quiet init must be fully silent; stdout=%q stderr=%q", second.Stdout, second.Stderr)
	}

	if err := os.Remove(artifact); err != nil {
		t.Fatalf("remove type-definition artifact: %v", err)
	}
	restored := run()
	if restored.ExitCode != 0 {
		t.Fatalf("quiet init after artifact removal exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", restored.ExitCode, restored.Stdout, restored.Stderr)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("quiet init did not restore type-definition artifact: %v", err)
	}
	if restored.Stdout == "" || strings.Contains(restored.Stdout, "┏") || strings.Contains(restored.Stdout, "┗") {
		t.Errorf("restoration should report a concise unframed change on stdout:\n%s", restored.Stdout)
	}
	if restored.Stderr != "" {
		t.Errorf("quiet init wrote to stderr while restoring artifact:\n%s", restored.Stderr)
	}
}

func TestInitQuietReportsRemovedNestedLink(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("quiet.config.js", `globalThis.getConfig = () => ({
  version: "1.0.0",
  apps: { shell: { shell: { name: "sh" } } },
  bundles: { base: { files: { "nested/config.json": "{}" }, links: { "nested/config.json": "nested/config.json" } } },
});
globalThis.getMinVersion = () => "0.0.0";
`)
	opts := clitest.RunOptions{Dir: p.Dir, CacheDir: t.TempDir()}
	run := func() clitest.Result {
		t.Helper()
		return clitest.Run(t, opts, "--no-auto-config", "--config", cfg, "init", "--quiet")
	}
	first := run()
	if first.ExitCode != 0 {
		t.Fatalf("quiet init exit = %d\nstdout:\n%s\nstderr:\n%s", first.ExitCode, first.Stdout, first.Stderr)
	}
	link := filepath.Join(p.Dir, ".datamitsu", "nested", "config.json")
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("init did not materialize nested symlink: info=%v err=%v", info, err)
	}
	p.WriteFile("quiet.config.js", `globalThis.getConfig = () => ({
  version: "1.0.0",
  bundles: { base: { files: { "nested/config.json": "{}" }, links: { "retained.json": "nested/config.json" } } },
});
globalThis.getMinVersion = () => "0.0.0";
`)
	removed := run()
	if removed.ExitCode != 0 {
		t.Fatalf("quiet init after config link removal exit = %d\nstdout:\n%s\nstderr:\n%s", removed.ExitCode, removed.Stdout, removed.Stderr)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("obsolete nested symlink remains: %v", err)
	}
	if removed.Stdout == "" || strings.Contains(removed.Stdout, "┏") || strings.Contains(removed.Stdout, "┗") {
		t.Errorf("removal should report a concise change:\n%s", removed.Stdout)
	}
}

func TestInitQuietPreservesInitCommandErrors(t *testing.T) {
	p := clitest.NewProject(t)
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh unavailable; leaves init-command error capture unverified")
	}
	cfg := p.WriteFile("quiet.config.js", `globalThis.getConfig = () => ({
  version: "1.0.0",
  apps: { shell: { shell: { name: "sh" } } },
  initCommands: { fail: { command: "shell", args: ["-c", "echo quiet-hook-diagnostic >&2; exit 7"] } }
});
globalThis.getMinVersion = () => "0.0.0";
`)
	args := make([]string, 0, 5)
	args = append(args, "--no-auto-config", "--config", cfg, "init")
	cacheDir := t.TempDir()
	baseOpts := clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir}
	normal := clitest.Run(t, baseOpts, args...)
	quiet := clitest.Run(t, baseOpts, append(args, "--quiet")...)
	if normal.ExitCode == 0 {
		t.Fatalf("fixture init should fail; stdout:\n%s\nstderr:\n%s", normal.Stdout, normal.Stderr)
	}
	if quiet.ExitCode != normal.ExitCode {
		t.Errorf("quiet init exit = %d, normal init exit = %d", quiet.ExitCode, normal.ExitCode)
	}
	if !strings.Contains(quiet.Stderr, "quiet-hook-diagnostic") {
		t.Errorf("quiet init suppressed captured hook error text:\n%s", quiet.Stderr)
	}
}

func TestSourceStatusRejectsQuietFlag(t *testing.T) {
	p := clitest.NewProject(t)
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "source", "status", "--quiet")
	if res.ExitCode == 0 {
		t.Fatalf("source status unexpectedly accepted local-only --quiet\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "unknown flag") {
		t.Errorf("source status should reject --quiet as an unknown flag:\n%s", res.Stderr)
	}
}
