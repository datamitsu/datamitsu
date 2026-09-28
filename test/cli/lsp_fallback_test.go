package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// fixersConfigJS declares two per-file fixers without an outputParser: clean
// prints prose, dirty a compiler error line about the file it fixed, and both
// exit 0. A run with no detected project type plans nothing, hence the marker.
const fixersConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  apps: {
    clean: { shell: { name: "sh", args: ["-c", 'echo "$0" >> "$MARKERS"; echo "tidied $1"', "clean"] } },
    dirty: { shell: { name: "sh", args: ["-c", 'echo "$0" >> "$MARKERS"; echo "$1:1:1: error: still broken" >&2', "dirty"] } },
  },
  runtimes: {},
  managedConfigs: {},
  projectTypes: { fixture: { markers: ["fixture.marker"] } },
  tools: {
    clean: { name: "clean", operations: { fix: { app: "clean", args: ["{file}"], scope: "per-file", globs: ["**/*.txt"] } } },
    dirty: { name: "dirty", operations: { fix: { app: "dirty", args: ["{file}"], scope: "per-file", globs: ["**/*.txt"] } } },
  },
});
globalThis.getMinVersion = () => "0.0.0";
`

// TestLspRecordsOnlyPassesTheCLIWould: the language server shares the CLI's
// execution cache, so it reads a fixer's output with the same fallback. The
// fixer whose output held nothing is cached for the CLI; the one that printed
// an error at exit 0 is not, and the CLI's fix finds the error.
func TestLspRecordsOnlyPassesTheCLIWould(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js", fixersConfigJS)
	p.WriteFile("fixture.marker", "")
	file := p.WriteFile("note.txt", "hello\n")
	cache := t.TempDir()
	markers := filepath.Join(t.TempDir(), "markers")
	env := []string{"DATAMITSU_CACHE_DIR=" + cache, "MARKERS=" + markers}

	lsp := startLsp(t, p.Dir, env)
	lsp.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	lsp.await("1")
	lsp.send(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/formatting","params":{"textDocument":{"uri":%q}}}`, "file://"+file))
	lsp.await("2")
	if res := lsp.finish(); res.ExitCode != 0 {
		t.Fatalf("lsp exited %d\n--- stderr ---\n%s", res.ExitCode, res.Stderr)
	}
	if got := ran(t, markers); got != "clean,dirty" {
		t.Fatalf("the language server ran %q, want both fixers", got)
	}

	if err := os.Remove(markers); err != nil {
		t.Fatal(err)
	}
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cache, Env: []string{"MARKERS=" + markers}}, "fix")
	if res.ExitCode != 1 || !strings.Contains(res.Stdout+res.Stderr, "still broken") {
		t.Errorf("fix exited %d without the error the fixer printed\n--- stdout ---\n%s\n--- stderr ---\n%s",
			res.ExitCode, res.Stdout, res.Stderr)
	}
	if got := ran(t, markers); got != "dirty" {
		t.Errorf("fix ran %q, want only dirty: clean's pass from the language server stands", got)
	}
}

// ran lists the tools that appended to the markers file, sorted.
func ran(t *testing.T, markers string) string {
	t.Helper()
	data, err := os.ReadFile(markers)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	slices.Sort(lines)
	return strings.Join(lines, ",")
}
