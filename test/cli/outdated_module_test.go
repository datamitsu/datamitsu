package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

const outdatedNote = `note: parser module "core" (datamitsu-parsers v0.2.1) is descriptor schema 1, older than 3`

// TestAnOutdatedModuleIsReportedWhereItIsAskedAbout: a pinned module older
// than descriptor schema 3 is named by config show and devtools parsers list,
// once each, and never by a plain run, whose user cannot change the pin.
func TestAnOutdatedModuleIsReportedWhereItIsAskedAbout(t *testing.T) {
	parsed := parsedTool(hadolintFinding, 1)
	e := newExecProject(t, map[string]string{"fixture.marker": "", "Dockerfile": "FROM debian\n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, releasedParserModule)
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, parsed))

	show := e.run("", nil, "config", "show")
	e.wantExit(show, 0)
	if n := strings.Count(show.Stderr, outdatedNote); n != 1 {
		t.Errorf("config show names the outdated module %d times, want once:\n%s", n, show.Stderr)
	}

	list := e.run("", nil, "devtools", "parsers", "list")
	e.wantExit(list, 0)
	if n := strings.Count(list.Stderr, outdatedNote); n != 1 {
		t.Errorf("devtools parsers list names the outdated module %d times, want once:\n%s", n, list.Stderr)
	}

	lint := e.run("", nil, "lint")
	e.wantExit(lint, 1)
	if strings.Contains(lint.Stderr, "descriptor schema 1") || strings.Contains(lint.Stdout, "descriptor schema 1") {
		t.Errorf("a plain run names the outdated module:\n%s%s", lint.Stdout, lint.Stderr)
	}
	verbose := e.run("", nil, "lint", "-v")
	if !strings.Contains(verbose.Stderr, "is descriptor schema 1, older than 3") {
		t.Errorf("a verbose run should name the outdated module:\n%s", verbose.Stderr)
	}
}

// TestACurrentModuleIsNotReported: the module this core is built with draws
// no note, and a module file is described without any configuration.
func TestACurrentModuleIsNotReported(t *testing.T) {
	p := clitest.NewProject(t)
	for _, module := range []string{currentParserModule, releasedParserModule} {
		wasm, err := os.ReadFile(module)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(p.Dir, filepath.Base(module))
		if err := os.WriteFile(path, wasm, 0o644); err != nil {
			t.Fatal(err)
		}
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "devtools", "parsers", "list", "--wasm", path)
		if res.ExitCode != 0 {
			t.Fatalf("list --wasm %s: exit %d\n%s", module, res.ExitCode, res.Stderr)
		}
		wantNote := module == releasedParserModule
		if got := strings.Contains(res.Stderr, "older than 3"); got != wantNote {
			t.Errorf("list --wasm %s: note %v, want %v:\n%s", module, got, wantNote, res.Stderr)
		}
	}
}
