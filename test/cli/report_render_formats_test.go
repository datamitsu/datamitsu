package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// renderFormats are the formats `report render` writes from a run's own JSON,
// with the file each is written to.
var renderFormats = []struct {
	format, file string
	companion    bool
}{
	{format: "markdown", file: "run.md"},
	{format: "sarif", file: "results.sarif"},
	{format: "junit", file: "junit.xml", companion: true},
	{format: "codequality", file: "gl-code-quality.json", companion: true},
	{format: "checkstyle", file: "checkstyle.xml", companion: true},
	{format: "rdjsonl", file: "rd.jsonl", companion: true},
}

// TestReportRenderFormats: every format `report render` writes from a run's
// own JSON is the file the run wrote, byte for byte, companion included — on
// another machine, without the checkout, after the sources changed.
func TestReportRenderFormats(t *testing.T) {
	e := reportProject(t, outsider, clitest.ShellTool("beta", failScript, clitest.ToolOpSpec{}))
	args := make([]string, 0, 3+2*len(renderFormats))
	args = append(args, "lint", "--report", "json=out/run.json")
	for _, f := range renderFormats {
		args = append(args, "--report", f.format+"=out/"+f.file)
	}
	e.wantExit(e.run("", nil, args...), 1)

	// Another machine: a directory outside any repository holding the
	// document alone, and a checkout whose sources changed.
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "run.json"), []byte(e.read("out/run.json")), 0o644); err != nil {
		t.Fatal(err)
	}
	e.p.WriteFile("Dockerfile", "FROM debian:12\nWORKDIR /app\n")

	for _, f := range renderFormats {
		t.Run(f.format, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{Dir: elsewhere, CacheDir: e.cache},
				"report", "render", "--input", "run.json", "--format", f.format, "--output", "again/"+f.file)
			e.wantExit(res, 0)
			again, err := os.ReadFile(filepath.Join(elsewhere, "again", f.file))
			if err != nil {
				t.Fatal(err)
			}
			if string(again) != e.read("out/"+f.file) {
				t.Errorf("report render wrote another %s than the run:\n--- run\n%s\n--- render\n%s", f.format, e.read("out/"+f.file), again)
			}
			companion, err := os.ReadFile(filepath.Join(elsewhere, "again", f.file+".completeness.json"))
			switch {
			case f.companion && err != nil:
				t.Errorf("no companion beside the rendered %s: %v", f.format, err)
			case f.companion && string(companion) != e.read("out/"+f.file+".completeness.json"):
				t.Errorf("report render wrote another companion than the run:\n%s", companion)
			case !f.companion && err == nil:
				t.Errorf("%s has a companion", f.format)
			}

			stdout := clitest.Run(t, clitest.RunOptions{Dir: elsewhere, CacheDir: e.cache},
				"report", "render", "--input", "run.json", "--format", f.format)
			e.wantExit(stdout, 0)
			if stdout.Stdout != e.read("out/"+f.file) {
				t.Errorf("report render to stdout wrote another %s", f.format)
			}
		})
	}
}

// TestReportRenderNarrowedFormats: a document of a narrowed run is refused for
// every format that lists findings unless --allow-partial, and written for
// SARIF, which leaves its incomplete tools out.
func TestReportRenderNarrowedFormats(t *testing.T) {
	e := reportProject(t)
	e.wantExit(e.run("", nil, "lint", "Dockerfile", "--allow-partial", "--report", "json=run.json"), 0)
	for _, f := range renderFormats {
		t.Run(f.format, func(t *testing.T) {
			res := e.run("", nil, "report", "render", "--input", "run.json", "--format", f.format, "--output", "out/"+f.file)
			if f.format == "sarif" {
				// Not refused, and not written either: every tool is left
				// out, and code scanning refuses a file without a run.
				e.wantExit(res, 0)
				if _, err := os.Stat(filepath.Join(e.p.Dir, "out", f.file)); !os.IsNotExist(err) {
					t.Errorf("the SARIF of a narrowed run was written: %v", err)
				}
				if !strings.Contains(res.Stderr, "sarif is not written") {
					t.Errorf("stderr:\n%s", res.Stderr)
				}
				return
			}
			e.wantExit(res, 2)
			if !strings.Contains(res.Stderr, "pass --allow-partial") {
				t.Errorf("stderr:\n%s", res.Stderr)
			}
			e.wantExit(e.run("", nil, "report", "render", "--input", "run.json", "--format", f.format, "--output", "out/"+f.file, "--allow-partial"), 0)
		})
	}
}
