//go:build e2e_oci

package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/report"
)

// formatTools declares, over the apps of the vendored configuration, three
// tools that print a standard format on request, each parsed by that
// format's key of the module this crate builds: ruff's SARIF, shellcheck's
// Checkstyle XML and typos's compiler lines.
func formatTools(parsers string) string {
	tool := func(app, parser string, globs []string, args ...string) string {
		spec, err := json.Marshal(map[string]any{
			"name":         app,
			"outputParser": map[string]string{"module": clitest.SeededParserModule, "parser": parser},
			"operations": map[string]any{"lint": map[string]any{
				"app": app, "scope": "repository", "globs": globs, "args": args,
			}},
		})
		if err != nil {
			panic(err)
		}
		return string(spec)
	}
	return "return { ...config, managedConfigs: {}, parsers: " + parsers + ", " +
		"projectTypes: { ...config.projectTypes, fixture: { markers: [\"fixture.marker\"] } }, tools: {" +
		"ruff: " + tool("ruff", "sarif", []string{"**/*.py"}, "check", "--no-cache", "--isolated", "--output-format", "sarif", "{files}") + ", " +
		"shellcheck: " + tool("shellcheck", "checkstyle-xml", []string{"**/*.sh"}, "-f", "checkstyle", "{files}") + ", " +
		"typos: " + tool("typos", "gcc", []string{"**/*.md"}, "--format", "brief", "{files}") +
		"} };"
}

// TestFormatKeysParseRealTools runs the three tools for real and reads their
// findings through the declared format keys, with the format provenance.
func TestFormatKeysParseRealTools(t *testing.T) {
	RequireOCIE2E(t)
	cacheDir := testCacheDir(t)
	module, err := filepath.Abs(filepath.Join("..", "..", "internal", "parsermanager", "testdata", "echo.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	parsers := clitest.SeedParserModule(t, cacheDir, module)
	p := newOverlayProject(t, formatTools(parsers))
	p.WriteFile("fixture.marker", "")
	p.WriteFile("bad.py", "import os\n")
	p.WriteFile("bad.sh", "#!/bin/sh\necho $1\n")
	p.WriteFile("typo.md", "teh\n") //nolint:misspell // the typo typos must find

	res := runOnline(t, p.Dir, cacheDir, "lint", "--fail-fast=false", "--report", "json=run.json")
	if res.ExitCode != 1 {
		t.Fatalf("lint exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	data, err := os.ReadFile(filepath.Join(p.Dir, "run.json"))
	if err != nil {
		t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, res.Stdout, res.Stderr)
	}
	var run report.Run
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ruff": "F401", "shellcheck": "SC2086", "typos": ""}
	for _, tr := range run.Operations[0].Tools {
		code, ok := want[tr.Name]
		if !ok {
			continue
		}
		delete(want, tr.Name)
		var found bool
		for _, inv := range tr.Invocations {
			if inv.Provenance != "format" {
				t.Errorf("%s: provenance %q, want format", tr.Name, inv.Provenance)
			}
			for _, f := range inv.Findings {
				found = found || strings.Contains(f.Code, code)
			}
		}
		if !found {
			t.Errorf("%s: no finding with code %q:\n%s", tr.Name, code, data)
		}
	}
	if len(want) != 0 {
		t.Errorf("tools that did not run: %v\n%s", want, res.Stdout)
	}
}
