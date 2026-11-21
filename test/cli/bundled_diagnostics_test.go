package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestDatamitsuIgnoreFindingUsesOperationFrame(t *testing.T) {
	input := "*.go:cspell\n"
	e := newExecProject(t, map[string]string{"fixture.marker": "", ".datamitsuignore": input}, fixtureSpec,
		clitest.ShellTool("formatter", passScript, clitest.ToolOpSpec{Operation: "fix"}),
		clitest.ShellTool("cspell", passScript, clitest.ToolOpSpec{Operation: "lint"}))

	lint := e.run("", nil, "lint")
	e.wantExit(lint, 0)
	if strings.Contains(lint.Stderr, "warning: datamitsuignore") {
		t.Fatalf("bundled warning escaped before lint frame: %s", lint.Stderr)
	}
	coreAt := strings.Index(lint.Stdout, "datamitsu")
	toolAt := strings.Index(lint.Stdout, "cspell")
	if coreAt < 0 || toolAt < coreAt || !strings.Contains(lint.Stdout, `have "*.go:cspell"`) || !strings.Contains(lint.Stdout, `want "*.go: cspell"`) {
		t.Fatalf("bundled lint result missing or ordered after external lint:\n%s", lint.Stdout)
	}
	if got, err := readExecFile(e, ".datamitsuignore"); err != nil || got != input {
		t.Fatalf("lint changed .datamitsuignore: %q, %v", got, err)
	}
	jsonResult := e.run("", nil, jsonl("lint", "--events", "diagnostics=all")...)
	e.wantExit(jsonResult, 0)
	if jsonResult.Stdout != "" {
		t.Fatalf("JSON-L lint wrote to stdout: %q", jsonResult.Stdout)
	}
	events := clitest.MustParseJSONL(t, jsonResult.Stderr)
	coreDiagnostics := diagnosticsOf(events)
	var coreCount int
	for _, event := range coreDiagnostics {
		if event.Fields["tool"] == "datamitsu" {
			coreCount++
			if event.Fields["source"] != "datamitsu" || event.Fields["code"] != "datamitsuignore-format" || event.Fields["severity"] != "warning" || event.Fields["gates"] != false {
				t.Errorf("bundled diagnostic = %v", event.Fields)
			}
		}
	}
	if coreCount != 1 {
		t.Fatalf("core diagnostics = %d, want one: %+v", coreCount, coreDiagnostics)
	}
	agent := e.run("", nil, "lint", "--output", "agent")
	e.wantExit(agent, 0)
	if strings.Contains(agent.Stdout, "┏━") || !strings.Contains(agent.Stdout, "datamitsuignore-format") {
		t.Fatalf("agent output omitted core result or leaked terminal frame:\n%s", agent.Stdout)
	}

	fix := e.run("", nil, "fix")
	e.wantExit(fix, 0)
	coreAt = strings.Index(fix.Stdout, "datamitsu")
	toolAt = strings.Index(fix.Stdout, "formatter")
	if coreAt < 0 || toolAt < coreAt {
		t.Fatalf("bundled fix result missing or ordered after formatter:\n%s", fix.Stdout)
	}
	if got, err := readExecFile(e, ".datamitsuignore"); err != nil || got != "*.go: cspell\n" {
		t.Fatalf("fix did not normalize .datamitsuignore: %q, %v", got, err)
	}

	clean := e.run("", nil, "lint")
	e.wantExit(clean, 0)
	if strings.Contains(clean.Stdout, "built-in checks") || strings.Contains(clean.Stdout, "warning(s)") || strings.Contains(clean.Stderr, "datamitsuignore") {
		t.Fatalf("clean ignore file still produced bundled finding:\n%s\n%s", clean.Stdout, clean.Stderr)
	}
}

func TestDatamitsuIgnoreFindingUsesOperationFrameWithoutExternalTools(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("fixture.marker", "")
	p.WriteFile(".datamitsuignore", "*.go:cspell\n")
	cfg := p.WriteFile("exec.config.js", clitest.ShellConfig(fixtureSpec))
	result := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "--no-auto-config", "--config", cfg, "lint")
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "┏━ lint") || !strings.Contains(result.Stdout, "datamitsu") || strings.Contains(result.Stdout, "No applicable tools found") {
		t.Fatalf("built-in-only lint was not rendered as an operation: exit=%d (stdout=%q, stderr=%q)", result.ExitCode, result.Stdout, result.Stderr)
	}
	t.Run("github annotations", func(t *testing.T) {
		project := clitest.NewProject(t)
		project.WriteFile("fixture.marker", "")
		project.WriteFile(".datamitsuignore", "*.go:cspell\n")
		cfg := project.WriteFile("exec.config.js", clitest.ShellConfig(fixtureSpec))
		result := clitest.Run(t, clitest.RunOptions{Dir: project.Dir}, "--no-auto-config", "--config", cfg, "lint", "--annotations", "github")
		if result.ExitCode != 0 {
			t.Fatalf("core-only annotation run failed: %d\n%s\n%s", result.ExitCode, result.Stdout, result.Stderr)
		}
		stop := strings.Index(result.Stdout, "::stop-commands::")
		finding := strings.Index(result.Stdout, "::warning file=.datamitsuignore")
		if stop < 0 || finding < 0 {
			t.Fatalf("core-only finding was not workflow-annotated:\n%s", result.Stdout)
		}
		tokenEnd := strings.IndexByte(result.Stdout[stop:], '\n')
		if tokenEnd < 0 {
			t.Fatalf("command-protection opener has no token terminator:\n%s", result.Stdout)
		}
		token := strings.TrimPrefix(result.Stdout[stop:stop+tokenEnd], "::stop-commands::")
		resume := strings.Index(result.Stdout, "::"+token+"::")
		if resume < 0 || finding < resume {
			t.Fatalf("annotation did not print after core result command-protection region:\n%s", result.Stdout)
		}
	})
}

func readExecFile(e *execProject, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(e.p.Dir, name))
	return string(b), err
}
