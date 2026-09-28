package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/gitenv"
)

// This file freezes what a fix reports it changed: observed with a snapshot of
// the working tree around each step, attributed to the invocation that was
// given the file, with the patch a stdout formatter applied.

// commitAll commits everything in dir with the fixture identity, unsigned,
// whatever the developer's global configuration says.
func commitAll(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitenv.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// rewriter is a formatter that writes files itself: it rewrites a.txt and
// b.txt, and restores dirty.txt to what the commit holds.
var rewriter = clitest.ShellTool("rewriter",
	settle+clitest.RecordRun+`; printf 'A\n' > a.txt; printf 'B\n' > b.txt; printf 'committed\n' > dirty.txt`,
	clitest.ToolOpSpec{Operation: "fix"})

// changesProject is a committed repository whose dirty.txt was then edited,
// with the rewriter as its fix tool.
func changesProject(t *testing.T, tools ...string) *execProject {
	t.Helper()
	if len(tools) == 0 {
		tools = []string{rewriter}
	}
	e := newExecProject(t, map[string]string{
		"fixture.marker": "", "a.txt": "a\n", "b.txt": "b\n", "dirty.txt": "committed\n",
	}, fixtureSpec, tools...)
	commitAll(t, e.p.Dir)
	e.p.WriteFile("dirty.txt", "edited\n")
	return e
}

type reportedOperation struct {
	Name            string `json:"name"`
	ChangesObserved bool   `json:"changesObserved"`
	ChangesReason   string `json:"changesReason"`
	ChangesDetail   string `json:"changesDetail"`
	Changes         []any  `json:"changes"`
	Tools           []struct {
		Name        string `json:"name"`
		Invocations []struct {
			Step    int `json:"step"`
			Changes []struct {
				Path  string `json:"path"`
				Kind  string `json:"kind"`
				Patch bool   `json:"patch"`
			} `json:"changes"`
		} `json:"invocations"`
	} `json:"tools"`
}

func (e *execProject) operations(rel string) []reportedOperation {
	e.t.Helper()
	var doc struct {
		Operations []reportedOperation `json:"operations"`
	}
	if err := json.Unmarshal([]byte(e.read(rel)), &doc); err != nil {
		e.t.Fatalf("%s: %v", rel, err)
	}
	return doc.Operations
}

// changesOf lists an operation's changes as "tool path kind patch".
func changesOf(op reportedOperation) []string {
	var out []string
	for _, tool := range op.Tools {
		for _, inv := range tool.Invocations {
			for _, c := range inv.Changes {
				patch := "no-patch"
				if c.Patch {
					patch = "patch"
				}
				out = append(out, strings.Join([]string{tool.Name, c.Path, c.Kind, patch}, " "))
			}
		}
	}
	return out
}

// TestFixChanges: a fix that rewrites two files and restores a third reports
// all three, observed, on the invocation that was given them.
func TestFixChanges(t *testing.T) {
	t.Run("observed", func(t *testing.T) {
		e := changesProject(t)
		res := e.run("", nil, "fix", "--report", "json=out/run.json", "--report", "markdown=out/run.md")
		e.wantExit(res, 0)
		ops := e.operations("out/run.json")
		if len(ops) != 1 || !ops[0].ChangesObserved || ops[0].ChangesReason != "" {
			t.Fatalf("operations = %+v, want one fix whose changes were observed", ops)
		}
		want := "rewriter a.txt modified no-patch|rewriter b.txt modified no-patch|rewriter dirty.txt reverted no-patch"
		if got := strings.Join(changesOf(ops[0]), "|"); got != want {
			t.Errorf("changes = %s, want %s", got, want)
		}
		if md := e.read("out/run.md"); !strings.Contains(md, "#### Changed files\n\n- `a.txt` modified\n- `b.txt` modified\n- `dirty.txt` reverted\n") {
			t.Errorf("the Markdown should list the changed files:\n%s", md)
		}
	})

	t.Run("agent", func(t *testing.T) {
		e := changesProject(t)
		res := e.run("", nil, "fix", "--output", "agent")
		e.wantExit(res, 0)
		if !strings.Contains(res.Stdout, "fix changed 3 files: a.txt, b.txt, dirty.txt\n") {
			t.Errorf("agent output should name the files to read again:\n%s", res.Stdout)
		}
		e.golden("changes_agent", res)
	})

	// check's lint holds no fix task: nothing is observed for it, and the
	// report says why.
	t.Run("check", func(t *testing.T) {
		e := changesProject(t, rewriter, clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}))
		e.wantExit(e.run("", nil, "check", "--report", "json=out/run.json"), 0)
		ops := e.operations("out/run.json")
		if len(ops) != 2 || !ops[0].ChangesObserved || ops[1].ChangesObserved || ops[1].ChangesReason != "no-fix-task" {
			t.Errorf("operations = %+v, want fix observed and lint no-fix-task", ops)
		}
	})

	// A status that cannot be read leaves the changes unobserved, with the
	// reason, instead of an empty list that would read as "nothing changed".
	t.Run("git_fails", func(t *testing.T) {
		clitest.RequireShell(t, "a failing status")
		realGit, err := exec.LookPath("git")
		if err != nil {
			t.Skip("no git on PATH")
		}
		e := changesProject(t)
		bin := t.TempDir()
		fake := "#!/bin/sh\nif [ \"$1\" = --no-optional-locks ]; then echo 'status is broken' >&2; exit 128; fi\nexec " + realGit + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
		res := e.run("", []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")},
			"fix", "--report", "json=out/run.json", "--output", "agent")
		e.wantExit(res, 0)
		ops := e.operations("out/run.json")
		if ops[0].ChangesObserved || ops[0].ChangesReason != "snapshot-failed" || !strings.Contains(ops[0].ChangesDetail, "status is broken") ||
			len(changesOf(ops[0])) != 0 || ops[0].Changes != nil {
			t.Errorf("operation = %+v, want changes not observed, snapshot-failed, and no list", ops[0])
		}
		if !strings.Contains(res.Stdout, "fix changes not observed: snapshot-failed (") {
			t.Errorf("agent output should say the changes were not observed:\n%s", res.Stdout)
		}
	})
}

// TestFixPatch: --report patch writes the diffs stdout formatters applied, in
// the order they applied them; a file two formatters changed has two hunks.
func TestFixPatch(t *testing.T) {
	upper := func(name, from, to string, priority int) string {
		return clitest.ShellTool(name, "sed 's/"+from+"/"+to+"/'", clitest.ToolOpSpec{
			Operation: "fix", Scope: "per-file", Globs: []string{"**/x.txt"}, Input: "stdin", Output: "stdout", Priority: priority,
		})
	}
	e := newExecProject(t, map[string]string{"fixture.marker": "", "x.txt": "one\ntwo\n"}, fixtureSpec,
		upper("first", "one", "ONE", 10), upper("second", "two", "TWO", 20), rewriter)
	e.p.WriteFile("a.txt", "a\n")
	e.p.WriteFile("b.txt", "b\n")
	e.p.WriteFile("dirty.txt", "edited\n")
	res := e.run("", nil, "fix", "--report", "patch=out/fix.patch", "--report", "json=out/run.json")
	e.wantExit(res, 0)
	if got := e.read("x.txt"); got != "ONE\nTWO\n" {
		t.Fatalf("x.txt = %q", got)
	}
	patch := e.read("out/fix.patch")
	want := "--- a/x.txt\n+++ b/x.txt\n@@ -1,2 +1,2 @@\n-one\n+ONE\n two\n" +
		"--- a/x.txt\n+++ b/x.txt\n@@ -1,2 +1,2 @@\n ONE\n-two\n+TWO\n"
	if patch != want {
		t.Errorf("patch =\n%s\nwant\n%s", patch, want)
	}
	got := changesOf(e.operations("out/run.json")[0])
	for _, w := range []string{"first x.txt modified patch", "second x.txt modified patch"} {
		if !strings.Contains(strings.Join(got, "|"), w) {
			t.Errorf("changes = %v, want %q", got, w)
		}
	}
	if !strings.Contains(e.read("out/run.json"), `"patch": "--- a/x.txt`) {
		t.Error("the own JSON of a run asked for patches should carry them")
	}

	// The own JSON holds the patches, so the patch renders again offline.
	res = e.run("", nil, "report", "render", "--input", "out/run.json", "--format", "patch")
	e.wantExit(res, 0)
	if res.Stdout != patch {
		t.Errorf("report render --format patch =\n%s\nwant the run's own\n%s", res.Stdout, patch)
	}
}
