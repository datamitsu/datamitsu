package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// TestDevtoolsSplitConfig exercises the fully-offline `devtools split-config`
// path: it loads the config, builds the per-stage slice plan and writes one
// self-contained config slice per app into --output. A single binary app yields
// at least one slice (covering RenderSlice + the atomic write), with no network.
func TestDevtoolsSplitConfig(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("bin.config.js", installBinaryConfigJS)
	outDir := filepath.Join(p.Dir, "slices")

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "devtools", "split-config", "--output", outDir)
	if res.ExitCode != 0 {
		t.Fatalf("split-config exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "config slices to") {
		t.Errorf("split-config stdout missing summary line:\n%s", res.Stdout)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("output dir not created: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("split-config wrote no slices for a binary app")
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(outDir, e.Name()))
		if err != nil {
			t.Fatalf("read slice %s: %v", e.Name(), err)
		}
		// Each slice is a runnable config exposing the standard globals.
		if !strings.Contains(string(data), "getConfig") {
			t.Errorf("slice %s does not look like a config:\n%s", e.Name(), string(data))
		}
	}
}

// TestDevtoolsSplitConfigMissingOutput locks the required-flag contract: no
// --output is a usage error (exit 1), independent of any network.
func TestDevtoolsSplitConfigMissingOutput(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "devtools", "split-config")
	if res.ExitCode == 0 {
		t.Fatalf("split-config without --output should fail, got exit 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "output") {
		t.Errorf("expected a required-flag message naming output, got:\n%s", res.Stderr)
	}
}
