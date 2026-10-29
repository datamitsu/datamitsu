package cli_test

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// This file freezes what `datamitsu cache copy` does: it moves a warmed
// project's execution cache to another project root — the git-worktree
// situation — without the tools ever re-running there, and without the source
// losing its own cache. The projects are clones of one tree (same files, same
// config), sharing one DATAMITSU_CACHE_DIR, so the only thing separating their
// cache namespaces is the absolute root path.

// copyProject is one clone of the scenario's tree with its own config file.
type copyProject struct {
	p   *clitest.Project
	cfg string
}

func newCopyProject(t *testing.T, files map[string]string, tools ...string) *copyProject {
	t.Helper()
	clitest.RequireShell(t, "the cache copy contract")
	p := clitest.NewProject(t)
	clitest.MarkerDir(p)
	for rel, content := range files {
		p.WriteFile(rel, content)
	}
	return &copyProject{
		p:   p,
		cfg: p.WriteFile("copy.config.js", clitest.ShellConfig(fixtureSpec, tools...)),
	}
}

func (c *copyProject) run(t *testing.T, cache string, args ...string) clitest.Result {
	t.Helper()
	full := append([]string{"--no-auto-config", "--config", c.cfg}, args...)
	return clitest.Run(t, clitest.RunOptions{Dir: c.p.Dir, CacheDir: cache}, full...)
}

// runCopy invokes `cache copy` from within dst with --source src.
func runCopy(t *testing.T, cache, dst, src string, extra ...string) clitest.Result {
	t.Helper()
	args := append([]string{"cache", "copy", "--source", src}, extra...)
	return clitest.Run(t, clitest.RunOptions{Dir: dst, CacheDir: cache}, args...)
}

// runsRecorded counts the lines a tool appended to its marker file.
func runsRecorded(t *testing.T, p *clitest.Project, tool string) int {
	t.Helper()
	ran, got := p.Marker(tool)
	if !ran {
		return 0
	}
	return strings.Count(got, "\n")
}

// cachedFlags returns the cached flag of every tool_run event of tool, in the
// order the stream reported them.
func cachedFlags(t *testing.T, res clitest.Result, tool string) []bool {
	t.Helper()
	var out []bool
	for _, ev := range clitest.MustParseJSONL(t, res.Stderr) {
		if ev.Type != "tool_run" || ev.Tool != tool || ev.Status != "done" {
			continue
		}
		v, ok := ev.Fields["cached"].(bool)
		if !ok {
			t.Fatalf("tool_run for %s carries no boolean cached: %+v", tool, ev.Fields)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		t.Fatalf("no tool_run events for %s in:\n%s", tool, res.Stderr)
	}
	return out
}

// TestCacheCopyWarmsNewWorktree is the contract of the command: a destination
// that never ran, warmed only by the copy, serves its first lint from the cache
// while an identical control clone without the copy runs the tool cold.
func TestCacheCopyWarmsNewWorktree(t *testing.T) {
	files := map[string]string{"fixture.marker": "", "a.txt": "a\n", "b.txt": "b\n", "c.txt": "c\n"}
	tool := clitest.ShellTool("alpha", passScript,
		clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/*.txt"}, Args: []string{"{file}"}})
	cache := t.TempDir()

	src := newCopyProject(t, files, tool)
	dst := newCopyProject(t, files, tool)
	ctl := newCopyProject(t, files, tool)

	// Warm the source: the tool runs once per file.
	warm := src.run(t, cache, "lint")
	if warm.ExitCode != 0 {
		t.Fatalf("the source's lint failed:\n%s\n%s", warm.Stdout, warm.Stderr)
	}
	if got := runsRecorded(t, src.p, "alpha"); got != 3 {
		t.Fatalf("the warm run recorded %d runs, want 3", got)
	}

	// The control clone, identical but never copied to, is cold at its own
	// path: the tool runs there too. Without this, a copy that did nothing
	// would pass the destination's assertions below just as well.
	ctlRun := ctl.run(t, cache, "lint")
	if ctlRun.ExitCode != 0 {
		t.Fatalf("the control clone's lint failed:\n%s\n%s", ctlRun.Stdout, ctlRun.Stderr)
	}
	if got := runsRecorded(t, ctl.p, "alpha"); got != 3 {
		t.Fatalf("the control clone recorded %d runs, want 3 — a new root must start cold", got)
	}

	// The destination never runs before the copy.
	copied := runCopy(t, cache, dst.p.Dir, src.p.Dir)
	if copied.ExitCode != 0 {
		t.Fatalf("cache copy failed:\n%s\n%s", copied.Stdout, copied.Stderr)
	}
	for _, needle := range []string{
		"Copied cache from",
		"lint/fix results: 3 file entries and 0 verdicts imported",
		"evaluated configs and the source-mode farm rebuild on the next run",
	} {
		if !strings.Contains(copied.Stdout, needle) {
			t.Errorf("copy output should contain %q:\n%s", needle, copied.Stdout)
		}
	}

	// The destination's first lint is served from the copied cache. It runs
	// in JSON-L mode so the cached flags of that very first run — not a later
	// one — are the proof, alongside the marker file staying empty.
	events := dst.run(t, cache, jsonl("lint")...)
	if events.ExitCode != 0 {
		t.Fatalf("the destination's first lint failed:\n%s\n%s", events.Stdout, events.Stderr)
	}
	for i, cached := range cachedFlags(t, events, "alpha") {
		if !cached {
			t.Errorf("tool_run %d of the destination's first lint reports cached=false", i+1)
		}
	}
	if got := runsRecorded(t, dst.p, "alpha"); got != 0 {
		t.Errorf("the destination ran the tool %d times after the copy, want 0", got)
	}

	// A second run in console mode pins the footer the same run would print.
	first := dst.run(t, cache, "lint")
	if first.ExitCode != 0 {
		t.Fatalf("the destination's console lint failed:\n%s\n%s", first.Stdout, first.Stderr)
	}
	if !strings.Contains(first.Stdout, "cache 100%") {
		t.Errorf("the destination's lint should report cache 100%%:\n%s", first.Stdout)
	}
	if got := runsRecorded(t, dst.p, "alpha"); got != 0 {
		t.Errorf("the destination ran the tool %d times after the copy, want 0", got)
	}

	// The source keeps its own cache through all of this, and clearing the
	// destination's takes nothing away from it.
	again := src.run(t, cache, "lint")
	if again.ExitCode != 0 {
		t.Fatalf("the source's second lint failed:\n%s\n%s", again.Stdout, again.Stderr)
	}
	if got := runsRecorded(t, src.p, "alpha"); got != 3 {
		t.Errorf("the source re-ran the tool after the copy (%d recorded lines, want the original 3)", got)
	}
	clearRun := clitest.Run(t, clitest.RunOptions{Dir: dst.p.Dir, CacheDir: cache}, "cache", "clear")
	if clearRun.ExitCode != 0 {
		t.Fatalf("cache clear in the destination failed:\n%s\n%s", clearRun.Stdout, clearRun.Stderr)
	}
	final := src.run(t, cache, "lint")
	if final.ExitCode != 0 {
		t.Fatalf("the source's lint after the destination's clear failed:\n%s\n%s", final.Stdout, final.Stderr)
	}
	if got := runsRecorded(t, src.p, "alpha"); got != 3 {
		t.Errorf("clearing the destination cooled the source (%d recorded lines, want the original 3)", got)
	}
}

// TestCacheCopyChangedFileStillRuns: a copied pass never covers content the
// destination changed — the edited file runs the tool, the untouched ones stay
// cached.
func TestCacheCopyChangedFileStillRuns(t *testing.T) {
	files := map[string]string{"fixture.marker": "", "a.txt": "a\n", "b.txt": "b\n", "c.txt": "c\n"}
	tool := clitest.ShellTool("alpha", passScript,
		clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/*.txt"}, Args: []string{"{file}"}})
	cache := t.TempDir()

	src := newCopyProject(t, files, tool)
	dst := newCopyProject(t, files, tool)

	if res := src.run(t, cache, "lint"); res.ExitCode != 0 {
		t.Fatalf("the source's lint failed:\n%s\n%s", res.Stdout, res.Stderr)
	}
	if res := runCopy(t, cache, dst.p.Dir, src.p.Dir); res.ExitCode != 0 {
		t.Fatalf("cache copy failed:\n%s\n%s", res.Stdout, res.Stderr)
	}

	dst.p.WriteFile("b.txt", "changed after the copy\n")
	res := dst.run(t, cache, jsonl("lint")...)
	if res.ExitCode != 0 {
		t.Fatalf("the destination's lint failed:\n%s\n%s", res.Stdout, res.Stderr)
	}
	ran, lines := dst.p.Marker("alpha")
	if !ran || strings.Count(lines, "\n") != 1 || !strings.Contains(lines, "b.txt") {
		t.Errorf("only b.txt may run after the copy; recorded:\n%s", lines)
	}
	flags := cachedFlags(t, res, "alpha")
	cached := 0
	for _, v := range flags {
		if v {
			cached++
		}
	}
	if cached != 2 || len(flags) != 3 {
		t.Errorf("cached flags = %v, want two cached and one re-run", flags)
	}
}

// TestCacheCopyDifferentConfig: a destination under another configuration
// never reuses the import — its own invalidation key does not match, so its
// first run executes every tool.
func TestCacheCopyDifferentConfig(t *testing.T) {
	files := map[string]string{"fixture.marker": "", "a.txt": "a\n"}
	cache := t.TempDir()

	src := newCopyProject(t, files,
		clitest.ShellTool("alpha", passScript,
			clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/*.txt"}, Args: []string{"{file}"}}))
	// One extra argument is enough to make the destination's configuration —
	// and with it its invalidation key — different from the source's.
	dst := newCopyProject(t, files,
		clitest.ShellTool("alpha", passScript,
			clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/*.txt"}, Args: []string{"{file}", "--strict"}}))

	if res := src.run(t, cache, "lint"); res.ExitCode != 0 {
		t.Fatalf("the source's lint failed:\n%s\n%s", res.Stdout, res.Stderr)
	}
	if res := runCopy(t, cache, dst.p.Dir, src.p.Dir); res.ExitCode != 0 {
		t.Fatalf("cache copy failed:\n%s\n%s", res.Stdout, res.Stderr)
	}

	res := dst.run(t, cache, "lint")
	if res.ExitCode != 0 {
		t.Fatalf("the destination's lint failed:\n%s\n%s", res.Stdout, res.Stderr)
	}
	if got := runsRecorded(t, dst.p, "alpha"); got != 1 {
		t.Errorf("the destination re-used imported results under a different configuration (ran %d times, want 1)", got)
	}
}

// TestCacheCopyFixPass: a fix pass travels with the copy like a lint pass.
func TestCacheCopyFixPass(t *testing.T) {
	files := map[string]string{"fixture.marker": "", "a.txt": "a\n"}
	fixer := clitest.ShellTool("fixer", passScript,
		clitest.ToolOpSpec{Operation: "fix", Scope: "per-file", Globs: []string{"**/*.txt"}, Args: []string{"{file}"}})
	cache := t.TempDir()

	src := newCopyProject(t, files, fixer)
	dst := newCopyProject(t, files, fixer)

	if res := src.run(t, cache, "fix"); res.ExitCode != 0 {
		t.Fatalf("the source's fix failed:\n%s\n%s", res.Stdout, res.Stderr)
	}
	if got := runsRecorded(t, src.p, "fixer"); got != 1 {
		t.Fatalf("the source's fix recorded %d runs, want 1", got)
	}
	if res := runCopy(t, cache, dst.p.Dir, src.p.Dir); res.ExitCode != 0 {
		t.Fatalf("cache copy failed:\n%s\n%s", res.Stdout, res.Stderr)
	}

	res := dst.run(t, cache, "fix")
	if res.ExitCode != 0 {
		t.Fatalf("the destination's fix failed:\n%s\n%s", res.Stdout, res.Stderr)
	}
	if got := runsRecorded(t, dst.p, "fixer"); got != 0 {
		t.Errorf("the destination re-ran the fixer %d times after the copy, want 0", got)
	}
}

// TestCacheCopyNoSourceCache: an existing directory with no cache is a
// friendly no-op, not an error.
func TestCacheCopyNoSourceCache(t *testing.T) {
	clitest.RequireShell(t, "the cache copy contract")
	cache := t.TempDir()
	empty := t.TempDir()
	dst := clitest.NewProject(t)

	res := runCopy(t, cache, dst.Dir, empty)
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0:\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "No datamitsu cache found for "+empty) {
		t.Errorf("the output should name the empty source:\n%s", res.Stdout)
	}
}

// TestCacheCopySameProject: copying a project onto itself is a no-op.
func TestCacheCopySameProject(t *testing.T) {
	clitest.RequireShell(t, "the cache copy contract")
	cache := t.TempDir()
	p := clitest.NewProject(t)

	for _, src := range []string{".", p.Dir} {
		res := runCopy(t, cache, p.Dir, src)
		if res.ExitCode != 0 {
			t.Fatalf("--source %q: exit code = %d, want 0:\n%s\n%s", src, res.ExitCode, res.Stdout, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "Source and destination are the same project") {
			t.Errorf("--source %q: the output should say the projects are the same:\n%s", src, res.Stdout)
		}
	}
}

// TestCacheCopyBadSource: an empty or non-existent source is a caller mistake
// and exits 2.
func TestCacheCopyBadSource(t *testing.T) {
	clitest.RequireShell(t, "the cache copy contract")
	cache := t.TempDir()
	p := clitest.NewProject(t)

	for name, src := range map[string]string{
		"missing": "/nonexistent/cache-copy-source",
		"empty":   "",
	} {
		res := runCopy(t, cache, p.Dir, src)
		if res.ExitCode != 2 {
			t.Fatalf("%s --source: exit code = %d, want 2:\n%s\n%s", name, res.ExitCode, res.Stdout, res.Stderr)
		}
	}
}

// TestCacheCopyMissingFlag: the required flag is enforced with exit 2.
func TestCacheCopyMissingFlag(t *testing.T) {
	clitest.RequireShell(t, "the cache copy contract")
	res := clitest.Run(t, clitest.RunOptions{Dir: clitest.NewProject(t).Dir, CacheDir: t.TempDir()},
		"cache", "copy")
	if res.ExitCode != 2 {
		t.Fatalf("exit code = %d, want 2:\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, `required flag(s) "source" not set`) {
		t.Errorf("stderr should name the missing flag:\n%s", res.Stderr)
	}
}
