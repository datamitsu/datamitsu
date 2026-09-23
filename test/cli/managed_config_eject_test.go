package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// ejectConfigJS declares one ejectable managed config read by a shell tool that
// copies whatever config path it receives into seen.txt, so a test can tell
// which file the tool actually read. __EJECT__ is the project's ejectConfigs.
const ejectConfigJS = `globalThis.getConfig = () => ({
  apps: { "cfg-reader": { shell: { name: "sh", args: ["-c", "cat \"$1\" > seen.txt", "cfg-reader"] } } },
  runtimes: {},
  projectTypes: { fixture: { markers: ["fixture.marker"] } },
  tools: {
    reader: { name: "reader", projectTypes: ["fixture"], operations: {
      lint: { app: "cfg-reader", args: ["{managedConfig:.reader.toml}"], scope: "repository" },
    } },
  },
  managedConfigs: { ".reader.toml": {
    scope: "git-root", tools: ["reader"], ejectable: true,
    content: (ctx) => {
      const base = "managed = true\n";
      const orig = ctx.originalContent ?? "";
      return orig.includes(base) ? orig : base + orig;
    },
  } },
  ejectConfigs: __EJECT__,
});
globalThis.getMinVersion = () => "0.0.0";
`

type ejectProject struct {
	*clitest.Project

	t *testing.T
}

func newEjectProject(t *testing.T) *ejectProject {
	t.Helper()
	p := &ejectProject{Project: clitest.NewProject(t), t: t}
	p.WriteFile("fixture.marker", "fixture\n")
	p.setEject("[]")
	return p
}

func (p *ejectProject) setEject(list string) {
	p.WriteFile("datamitsu.config.js", strings.Replace(ejectConfigJS, "__EJECT__", list, 1))
}

func (p *ejectProject) run(args ...string) clitest.Result {
	return clitest.Run(p.t, clitest.RunOptions{Dir: p.Dir}, args...)
}

func (p *ejectProject) mustRun(args ...string) clitest.Result {
	p.t.Helper()
	res := p.run(args...)
	if res.ExitCode != 0 {
		p.t.Fatalf("`%s` exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), res.ExitCode, res.Stdout, res.Stderr)
	}
	return res
}

func (p *ejectProject) mustFail(want string, args ...string) {
	p.t.Helper()
	res := p.run(args...)
	if res.ExitCode == 0 {
		p.t.Fatalf("`%s` exit = 0, want non-zero\nstdout:\n%s", strings.Join(args, " "), res.Stdout)
	}
	if out := res.Stdout + res.Stderr; !strings.Contains(out, want) {
		p.t.Fatalf("`%s` output does not contain %q:\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), want, res.Stdout, res.Stderr)
	}
}

func (p *ejectProject) read(rel string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(p.Dir, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (p *ejectProject) wantFile(rel, content string) {
	p.t.Helper()
	got, ok := p.read(rel)
	if !ok {
		p.t.Fatalf("%s does not exist", rel)
	}
	if got != content {
		p.t.Fatalf("%s = %q, want %q", rel, got, content)
	}
}

func (p *ejectProject) wantAbsent(rel string) {
	p.t.Helper()
	if _, ok := p.read(rel); ok {
		p.t.Fatalf("%s exists, want it absent", rel)
	}
}

const internalReaderConfig = ".datamitsu/configs/.reader.toml"

// The whole lifecycle of an ejectable config against the real binary: init
// renders it outside the repository, lint reads it from there, an eject moves
// it into the repository through reconcile, and moving it back refuses to
// delete a file the project changed.
func TestManagedConfigEjectLifecycle(t *testing.T) {
	p := newEjectProject(t)

	p.mustFail("run `datamitsu init`", "lint")

	res := p.mustRun("init")
	if !strings.Contains(res.Stdout, ".datamitsu/configs/") {
		t.Errorf("init does not report the internal configs:\n%s", res.Stdout)
	}
	p.wantFile(internalReaderConfig, "managed = true\n")
	p.wantAbsent(".reader.toml")

	p.mustRun("lint")
	p.wantFile("seen.txt", "managed = true\n")

	p.WriteFile(internalReaderConfig, "tampered\n")
	p.mustFail("out of date", "lint")
	p.mustRun("init")
	p.wantFile(internalReaderConfig, "managed = true\n")

	p.setEject(`["reader"]`)
	p.mustFail("config reconcile --tools reader", "lint")
	p.mustRun("config", "reconcile", "--skip-fix")
	p.wantFile(".reader.toml", "managed = true\n")
	p.wantAbsent(internalReaderConfig)

	p.WriteFile(".reader.toml", "managed = true\ncustom = 1\n")
	p.mustRun("config", "reconcile", "--skip-fix")
	p.wantFile(".reader.toml", "managed = true\ncustom = 1\n")
	p.mustRun("lint")
	p.wantFile("seen.txt", "managed = true\ncustom = 1\n")

	p.setEject("[]")
	p.mustFail(`adding "reader" to ejectConfigs`, "config", "reconcile", "--skip-fix")
	p.wantFile(".reader.toml", "managed = true\ncustom = 1\n")
	p.mustFail(`add "reader" to ejectConfigs`, "lint")

	p.WriteFile(".reader.toml", "managed = true\n")
	p.mustRun("config", "reconcile", "--skip-fix")
	p.wantAbsent(".reader.toml")
	p.wantFile(internalReaderConfig, "managed = true\n")
	p.mustRun("lint")
	p.wantFile("seen.txt", "managed = true\n")
}

func TestManagedConfigEjectRejectsUnknownTool(t *testing.T) {
	p := newEjectProject(t)
	p.setEject(`["no-such-tool"]`)
	p.mustFail(`ejectConfigs: "no-such-tool" is not a configured tool`, "config", "show")
}

func TestManagedConfigEjectDryRunTouchesNothing(t *testing.T) {
	p := newEjectProject(t)
	p.WriteFile(".reader.toml", "managed = true\n")

	res := p.mustRun("config", "reconcile", "--dry-run")
	if !strings.Contains(res.Stdout, ".reader.toml") || !strings.Contains(res.Stdout, "internal") {
		t.Errorf("dry-run does not preview the move into .datamitsu/configs/:\n%s", res.Stdout)
	}
	p.wantFile(".reader.toml", "managed = true\n")
	p.wantAbsent(internalReaderConfig)
}
