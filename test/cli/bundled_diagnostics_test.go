package cli_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/digest"
)

func TestDatamitsuIgnoreFindingUsesOperationFrame(t *testing.T) {
	input := "*.go:cspell\n"
	e := newExecProject(t, map[string]string{"fixture.marker": "", ".datamitsuignore": input}, fixtureSpec,
		clitest.ShellTool("formatter", passScript, clitest.ToolOpSpec{
			Scope:        "",
			Args:         nil,
			Globs:        nil,
			Priority:     0,
			ProjectTypes: nil,
			Parser:       "",
			ParserModule: "",
			Env:          nil,
			InheritEnv:   nil,
			FailOn:       "",
			Input:        "",
			Output:       "",
			Operation:    "fix",
		}),
		clitest.ShellTool("cspell", passScript, clitest.ToolOpSpec{
			Scope:        "",
			Args:         nil,
			Globs:        nil,
			Priority:     0,
			ProjectTypes: nil,
			Parser:       "",
			ParserModule: "",
			Env:          nil,
			InheritEnv:   nil,
			FailOn:       "",
			Input:        "",
			Output:       "",
			Operation:    "lint",
		}))

	lint := e.run("", nil, "lint", "--fail-fast=false")
	e.wantExit(lint, 1)
	frameAt := strings.Index(lint.Stdout, "┏━ lint")
	coreAt := strings.Index(lint.Stdout, "┃ ✗ datamitsu")
	toolAt := strings.Index(lint.Stdout, "┃ ✓ cspell")
	if frameAt < 0 || toolAt <= frameAt || coreAt <= toolAt || strings.Contains(lint.Stdout, "warning") || !strings.Contains(lint.Stdout, "[datamitsuignore-format]") {
		t.Fatalf("expected the core error after the successful tool:\n%s", lint.Stdout)
	}
	if got, err := readExecFile(e, ".datamitsuignore"); err != nil || got != input {
		t.Fatalf("lint changed .datamitsuignore: %q, %v", got, err)
	}
	jsonResult := e.run("", nil, jsonl("lint", "--events", "diagnostics=all")...)
	e.wantExit(jsonResult, 1)
	if jsonResult.Stdout != "" {
		t.Fatalf("JSON-L lint wrote to stdout: %q", jsonResult.Stdout)
	}
	events := clitest.MustParseJSONL(t, jsonResult.Stderr)
	coreDiagnostics := diagnosticsOf(events)
	var coreCount int
	for _, event := range coreDiagnostics {
		if event.Fields["tool"] == "datamitsu" {
			coreCount++
			if event.Fields["source"] != "datamitsu" || event.Fields["code"] != "datamitsuignore-format" || event.Fields["severity"] != "error" || event.Fields["gates"] != true {
				t.Errorf("bundled diagnostic = %v", event.Fields)
			}
		}
	}
	if coreCount != 1 {
		t.Fatalf("core diagnostics = %d, want one: %+v", coreCount, coreDiagnostics)
	}
	agent := e.run("", nil, "lint", "--output", "agent")
	e.wantExit(agent, 1)
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
	if strings.Contains(clean.Stdout, "[datamitsuignore-format]") || strings.Contains(clean.Stderr, "datamitsuignore") {
		t.Fatalf("clean ignore file still produced bundled finding:\n%s\n%s", clean.Stdout, clean.Stderr)
	}
}

func TestBundledLintSeesConfiguredFixFileChanges(t *testing.T) {
	tests := []struct {
		name, initial, fixScript string
		wantFinding              bool
	}{
		{name: "rewrite", initial: "*.go:cspell\n", fixScript: `printf '*.go:  cspell  \n' > .datamitsuignore`, wantFinding: true},
		{
			initial: "",
			name:    "create", fixScript: `printf '*.go:cspell\n' > .datamitsuignore`, wantFinding: true,
		},
		{name: "delete", initial: "*.go:cspell\n", fixScript: `rm -f .datamitsuignore`, wantFinding: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := clitest.NewProject(t)
			p.WriteFile("fixture.marker", "")
			if tt.initial != "" {
				p.WriteFile(".datamitsuignore", tt.initial)
			}
			cfg := p.WriteFile("exec.config.js", clitest.ShellConfig(fixtureSpec,
				clitest.ShellTool("fixer", tt.fixScript, clitest.ToolOpSpec{
					Scope:        "",
					Args:         nil,
					Globs:        nil,
					Priority:     0,
					ProjectTypes: nil,
					Parser:       "",
					ParserModule: "",
					Env:          nil,
					InheritEnv:   nil,
					FailOn:       "",
					Input:        "",
					Output:       "",
					Operation:    "fix",
				})))
			result := clitest.Run(t, clitest.RunOptions{
				CacheDir: "",
				Env:      nil,
				Stdin:    "",
				Timeout:  0,
				Dir:      p.Dir,
			}, "--no-auto-config", "--config", cfg, "check", "--fail-fast=false")
			if tt.wantFinding && (result.ExitCode != 1 || !strings.Contains(result.Stdout, "[datamitsuignore-format]")) {
				t.Fatalf("post-fix bundled finding missing: exit=%d\n%s\n%s", result.ExitCode, result.Stdout, result.Stderr)
			}
			if !tt.wantFinding && (result.ExitCode != 0 || strings.Contains(result.Stdout, "[datamitsuignore-format]")) {
				t.Fatalf("bundled lint used stale pre-fix files: exit=%d\n%s\n%s", result.ExitCode, result.Stdout, result.Stderr)
			}
		})
	}
}

func TestBundledLintUsesPostFixIgnoreRules(t *testing.T) {
	tests := []struct {
		name, initial, fixScript string
		wantFinding              bool
	}{
		{name: "new ignore rule", initial: "", fixScript: `printf '*.go: cspell\n' > .datamitsuignore`, wantFinding: false},
		{name: "newly unignored cold app", initial: "*.go: cspell\n", fixScript: `printf '' > .datamitsuignore`, wantFinding: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := clitest.NewProject(t)
			p.WriteFile("fixture.marker", "")
			p.WriteFile("issue.go", "package fixture\n")
			if tt.initial != "" {
				p.WriteFile(".datamitsuignore", tt.initial)
			}
			payload := []byte("#!/bin/sh\nexit 0\n")
			if runtime.GOOS == "windows" {
				t.Skip("fixture binary is a POSIX shell script")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !tt.wantFinding {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			hash := digest.SHA256Of(payload).String()
			spec := fixtureSpec
			build := fmt.Sprintf(`{url:%q,hash:%q,contentType:"binary"}`, server.URL, hash)
			spec.Extra = fmt.Sprintf(`c.apps.cspell = {binary:{binaries:{%q:{%q:{glibc:%s,musl:%s,unknown:%s}}}}};`, runtime.GOOS, runtime.GOARCH, build, build, build)
			cfg := p.WriteFile("exec.config.js", clitest.ShellConfig(spec,
				clitest.ShellTool("fixer", tt.fixScript, clitest.ToolOpSpec{
					Scope:        "",
					Args:         nil,
					Globs:        nil,
					Priority:     0,
					ProjectTypes: nil,
					Parser:       "",
					ParserModule: "",
					Env:          nil,
					InheritEnv:   nil,
					FailOn:       "",
					Input:        "",
					Output:       "",
					Operation:    "fix",
				}),
				clitest.ShellTool("cspell", passScript, clitest.ToolOpSpec{
					Operation:    "",
					Priority:     0,
					ProjectTypes: nil,
					Parser:       "",
					ParserModule: "",
					Env:          nil,
					InheritEnv:   nil,
					FailOn:       "",
					Input:        "",
					Output:       "",
					Scope:        "per-file", Globs: []string{"*.go"}, Args: []string{"{file}"},
				})))
			result := clitest.Run(t, clitest.RunOptions{
				Stdin:   "",
				Timeout: 0,
				Dir:     p.Dir, CacheDir: t.TempDir(), Env: []string{"DATAMITSU_OFFLINE="},
			}, "--no-auto-config", "--config", cfg, "check", "--fail-fast=false")
			if tt.wantFinding && (result.ExitCode != 0 || !strings.Contains(result.Stdout, "┃ ✓ cspell") || !strings.Contains(result.Stdout, "prepare tools")) {
				t.Fatalf("post-fix lint plan did not include newly unignored app: exit=%d\n%s\n%s", result.ExitCode, result.Stdout, result.Stderr)
			}
			if !tt.wantFinding && (result.ExitCode != 0 || strings.Contains(result.Stdout, "┃ ✓ cspell")) {
				t.Fatalf("post-fix lint plan retained newly ignored app: exit=%d\n%s\n%s", result.ExitCode, result.Stdout, result.Stderr)
			}
		})
	}
}

func TestCheckKeepsFixFailureWhenLintSetupFails(t *testing.T) {
	e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
		clitest.ShellTool("fixer", `printf 'malformed\n' > .datamitsuignore; exit 1`, clitest.ToolOpSpec{
			Scope:        "",
			Args:         nil,
			Globs:        nil,
			Priority:     0,
			ProjectTypes: nil,
			Parser:       "",
			ParserModule: "",
			Env:          nil,
			InheritEnv:   nil,
			FailOn:       "",
			Input:        "",
			Output:       "",
			Operation:    "fix",
		}))
	res := e.run("", nil, "check", "--fail-fast=false")
	e.wantExit(res, 1)
	if !strings.Contains(res.Stderr, "operation failed") || !strings.Contains(res.Stderr, "missing colon separator") {
		t.Fatalf("expected both fix and lint setup failures:\n%s", res.Stderr)
	}
}

func TestDatamitsuIgnoreFindingUsesOperationFrameWithoutExternalTools(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("fixture.marker", "")
	p.WriteFile(".datamitsuignore", "*.go:cspell\n")
	cfg := p.WriteFile("exec.config.js", clitest.ShellConfig(fixtureSpec))
	result := clitest.Run(t, clitest.RunOptions{
		CacheDir: "",
		Env:      nil,
		Stdin:    "",
		Timeout:  0,
		Dir:      p.Dir,
	}, "--no-auto-config", "--config", cfg, "lint")
	if result.ExitCode != 1 || !strings.Contains(result.Stdout, "┏━ lint") || !strings.Contains(result.Stdout, "┃ ✗ datamitsu") || strings.Contains(result.Stdout, "No applicable tools found") {
		t.Fatalf("built-in-only lint was not rendered as an operation: exit=%d (stdout=%q, stderr=%q)", result.ExitCode, result.Stdout, result.Stderr)
	}
	t.Run("github annotations", func(t *testing.T) {
		project := clitest.NewProject(t)
		project.WriteFile("fixture.marker", "")
		project.WriteFile(".datamitsuignore", "*.go:cspell\n")
		cfg := project.WriteFile("exec.config.js", clitest.ShellConfig(fixtureSpec))
		result := clitest.Run(t, clitest.RunOptions{
			CacheDir: "",
			Env:      nil,
			Stdin:    "",
			Timeout:  0,
			Dir:      project.Dir,
		}, "--no-auto-config", "--config", cfg, "lint", "--annotations", "github")
		if result.ExitCode != 1 {
			t.Fatalf("core-only annotation exit = %d, want 1\n%s\n%s", result.ExitCode, result.Stdout, result.Stderr)
		}
		stop := strings.Index(result.Stdout, "::stop-commands::")
		finding := strings.Index(result.Stdout, "::error file=.datamitsuignore")
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
