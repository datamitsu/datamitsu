package clitest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestShellToolDeclaration checks, through the binary's own config loader, that
// every ToolOpSpec field lands where the executor reads it and that a script
// full of shell and JS metacharacters survives the JS literal unchanged.
func TestShellToolDeclaration(t *testing.T) {
	p := NewProject(t)
	cache := t.TempDir()
	script := `printf '%s\n' "$0" "it's" \` + "`x`" + ` </script> $((1+1)); echo "done"` + "\n# trailing line"
	cfg := p.WriteFile("shell.config.js", ShellConfig(
		ShellConfigSpec{
			ProjectTypes: map[string][]string{"fixture": {"fixture.marker"}},
			Parsers:      SeedParserModule(t, cache, echoWASM),
			Extra:        `c.apps["extra"] = { shell: { name: "true", args: [] } };` + "\n",
		},
		ShellTool("alpha", script, ToolOpSpec{
			Operation:    "fix",
			Scope:        "per-file",
			Args:         []string{"--flag", "{file}"},
			Globs:        []string{"**/*.txt"},
			Priority:     7,
			ProjectTypes: []string{"fixture"},
			Parser:       "hadolint",
		}),
		ShellTool("beta", "true", ToolOpSpec{}),
	))

	res := Run(t, RunOptions{Dir: p.Dir, CacheDir: cache}, "--no-auto-config", "--config", cfg, "config", "show")
	if res.ExitCode != 0 {
		t.Fatalf("config show exit = %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	var shown struct {
		Apps map[string]struct {
			Shell struct {
				Name string   `json:"name"`
				Args []string `json:"args"`
			} `json:"shell"`
		} `json:"apps"`
		Tools map[string]struct {
			ProjectTypes []string                  `json:"projectTypes"`
			OutputParser map[string]string         `json:"outputParser"`
			Operations   map[string]map[string]any `json:"operations"`
		} `json:"tools"`
		ProjectTypes map[string]struct {
			Markers []string `json:"markers"`
		} `json:"projectTypes"`
		Parsers map[string]map[string]any `json:"parsers"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &shown); err != nil {
		t.Fatalf("config show is not JSON: %v\n%s", err, res.Stdout)
	}

	if app := shown.Apps["alpha"].Shell; app.Name != "sh" || !reflect.DeepEqual(app.Args, []string{"-c", script, "alpha"}) {
		t.Errorf("app alpha = %+v, want sh -c <script> alpha", app)
	}
	alpha := shown.Tools["alpha"]
	if !reflect.DeepEqual(alpha.ProjectTypes, []string{"fixture"}) {
		t.Errorf("alpha projectTypes = %v", alpha.ProjectTypes)
	}
	if alpha.OutputParser["module"] != SeededParserModule || alpha.OutputParser["parser"] != "hadolint" {
		t.Errorf("alpha outputParser = %v", alpha.OutputParser)
	}
	fix, ok := alpha.Operations["fix"]
	if !ok {
		t.Fatalf("alpha has no fix operation: %v", alpha.Operations)
	}
	want := map[string]any{
		"app":      "alpha",
		"args":     []any{"--flag", "{file}"},
		"scope":    "per-file",
		"globs":    []any{"**/*.txt"},
		"priority": float64(7),
		"env":      map[string]any{"MARKERS": "{root}/" + MarkerDirName},
	}
	for key, value := range want {
		if !reflect.DeepEqual(fix[key], value) {
			t.Errorf("alpha fix.%s = %#v, want %#v", key, fix[key], value)
		}
	}

	beta := shown.Tools["beta"].Operations["lint"]
	if beta["scope"] != "repository" || !reflect.DeepEqual(beta["args"], []any{}) {
		t.Errorf("beta defaults to a lint operation of repository scope with no args, got %v", beta)
	}
	if _, ok := beta["priority"]; ok {
		t.Errorf("beta declares no priority, got %v", beta["priority"])
	}
	if _, ok := shown.Apps["extra"]; !ok {
		t.Error("ShellConfigSpec.Extra did not run")
	}
	if got := shown.ProjectTypes["fixture"].Markers; !reflect.DeepEqual(got, []string{"fixture.marker"}) {
		t.Errorf("projectTypes.fixture.markers = %v", got)
	}
	if _, ok := shown.Parsers[SeededParserModule]; !ok {
		t.Errorf("parsers = %v, want the seeded module declared", shown.Parsers)
	}
}

// TestShellToolRecordsRuns runs a ShellTool through lint and reads back what
// the marker convention recorded.
func TestShellToolRecordsRuns(t *testing.T) {
	RequireShell(t, "the marker convention of ShellTool")
	p := NewProject(t)
	if dir := MarkerDir(p); !strings.HasSuffix(dir, "/"+MarkerDirName) {
		t.Errorf("MarkerDir = %q, want it under the project as %s", dir, MarkerDirName)
	}
	p.WriteFile("fixture.marker", "")
	p.WriteFile("a.txt", "a\n")
	cfg := p.WriteFile("shell.config.js", ShellConfig(
		ShellConfigSpec{ProjectTypes: map[string][]string{"fixture": {"fixture.marker"}}},
		ShellTool("alpha", RecordRun, ToolOpSpec{Scope: "per-file", Globs: []string{"*.txt"}, Args: []string{"{file}"}}),
		ShellTool("beta", "exit 0", ToolOpSpec{}),
	))

	if got := p.Markers(); len(got) != 0 {
		t.Fatalf("Markers before any run = %v, want none", got)
	}
	res := Run(t, RunOptions{Dir: p.Dir}, "--no-auto-config", "--config", cfg, "lint")
	if res.ExitCode != 0 {
		t.Fatalf("lint exit = %d\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	if ran, got := p.Marker("alpha"); !ran || got != "alpha "+p.Dir+"/a.txt\n" {
		t.Errorf("Marker(alpha) = %v, %q", ran, got)
	}
	if ran, _ := p.Marker("beta"); ran {
		t.Error("beta records nothing, yet Marker reports a run")
	}
	if got := p.Markers(); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Errorf("Markers = %v, want [alpha]", got)
	}
}
