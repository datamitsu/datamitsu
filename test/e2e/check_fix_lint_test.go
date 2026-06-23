//go:build e2e_oci

package e2e_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// formatterSpec describes how to drive one fast, native-binary formatter through
// the check/fix/lint pipeline: a fixture file with a deliberate formatting issue,
// the glob its operations match, and the fix/lint args. lintArgs MUST exit
// non-zero when the file needs formatting (a "check"/diff mode); fixArgs MUST
// rewrite the file in place. {file} is expanded per-file by the executor.
type formatterSpec struct {
	app      string   // backing app name in the bundle (must be covered for this host)
	fixture  string   // fixture file name, relative to the project root
	bad      string   // deliberately-unformatted content the formatter will change
	glob     string   // glob selecting the fixture for both operations
	fixArgs  []string // args for the fix operation (rewrites in place)
	lintArgs []string // args for the lint operation (non-zero when unformatted)
}

// formatters lists the candidate fast formatters in preference order. Each is a
// single static binary in the bundle with a real diff/check lint mode, so the
// pipeline stays fast and the lint exit code is meaningful. The first one the
// bundle covers for this host is used.
var formatters = []formatterSpec{
	{
		app:     "shfmt",
		fixture: "bad.sh",
		// Unindented if-body: shfmt re-indents it, so -d reports a diff.
		bad:      "#!/usr/bin/env bash\nif true; then\necho hi\nfi\n",
		glob:     "**/*.sh",
		fixArgs:  []string{"-w", "{file}"},
		lintArgs: []string{"-d", "{file}"},
	},
	{
		app:     "yamlfmt",
		fixture: "bad.yaml",
		// Extra spaces after the colon: yamlfmt collapses them, so -lint fails.
		bad:      "a:   1\nb:   2\n",
		glob:     "**/*.yaml",
		fixArgs:  []string{"{file}"},
		lintArgs: []string{"-lint", "{file}"},
	},
}

// coveredApps returns the set of apps the bundle covers for this host, read from
// `store status --json` against the intact inherited config.
func coveredApps(t *testing.T, cacheDir string) map[string]bool {
	t.Helper()
	probe := newOverlayProject(t, "return config;")
	covered := map[string]bool{}
	for _, a := range statusJSON(t, probe, cacheDir).Apps {
		if a.Covered {
			covered[a.App] = true
		}
	}
	return covered
}

// discoverFormatter picks the first formatter the bundle covers for this host.
// If none is covered the test is skipped (the bundle contents are the variable).
func discoverFormatter(t *testing.T, cacheDir string) formatterSpec {
	t.Helper()
	covered := coveredApps(t, cacheDir)
	for _, f := range formatters {
		if covered[f.app] {
			return f
		}
	}
	t.Skip("no covered fast formatter (shfmt/yamlfmt) found in the bundle")
	return formatterSpec{}
}

// formatterOverlayJS builds the getConfig body that keeps the inherited apps but
// replaces the tool set with a single tool ("formatter") whose fix and lint
// operations drive the chosen formatter per-file on its glob. Caching is disabled
// so each phase re-runs the tool against the current file content.
func formatterOverlayJS(t *testing.T, f formatterSpec) string {
	t.Helper()
	fixArgs, err := json.Marshal(f.fixArgs)
	if err != nil {
		t.Fatalf("marshal fixArgs: %v", err)
	}
	lintArgs, err := json.Marshal(f.lintArgs)
	if err != nil {
		t.Fatalf("marshal lintArgs: %v", err)
	}
	globs, err := json.Marshal([]string{f.glob})
	if err != nil {
		t.Fatalf("marshal globs: %v", err)
	}
	return `return Object.assign({}, config, { tools: { "formatter": { name: "formatter", operations: {` +
		`fix: { app: ` + jsQuote(f.app) + `, args: ` + string(fixArgs) + `, scope: "per-file", globs: ` + string(globs) + `, cache: false },` +
		`lint: { app: ` + jsQuote(f.app) + `, args: ` + string(lintArgs) + `, scope: "per-file", globs: ` + string(globs) + `, cache: false }` +
		`} } } });`
}

// jsQuote renders s as a JSON string literal for embedding in generated JS.
func jsQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestOCICheckFixLint exercises the real check/fix/lint pipeline against one fast
// formatter from the digest-pinned bundle. It asserts behavior — exit codes and
// file diffs — not byte-exact tool output, since tool versions vary. Gated by
// e2e_oci + DATAMITSU_TEST_OCI=1.
func TestOCICheckFixLint(t *testing.T) {
	RequireOCIE2E(t)

	cacheDir := testCacheDir(t)
	f := discoverFormatter(t, cacheDir)
	t.Logf("formatter: %s on %s", f.app, f.glob)

	overlay := formatterOverlayJS(t, f)

	readFixture := func(p *clitest.Project) string {
		t.Helper()
		data, err := os.ReadFile(p.Dir + "/" + f.fixture)
		if err != nil {
			t.Fatalf("read fixture %s: %v", f.fixture, err)
		}
		return string(data)
	}

	// --- lint reports the issue (non-zero) and does not modify the file -------
	t.Run("lint-reports-issue", func(t *testing.T) {
		p := newOverlayProject(t, overlay)
		p.WriteFile(f.fixture, f.bad)

		res := runOnline(t, p.Dir, cacheDir, "lint")
		if res.ExitCode == 0 {
			t.Fatalf("lint exit = 0 on an unformatted file, want non-zero\nstdout:\n%s\nstderr:\n%s",
				res.Stdout, res.Stderr)
		}
		if got := readFixture(p); got != f.bad {
			t.Errorf("lint modified the file; it must only report\n--- before ---\n%q\n--- after ---\n%q", f.bad, got)
		}
	})

	// --- fix repairs the file (content changes) ------------------------------
	t.Run("fix-repairs-file", func(t *testing.T) {
		p := newOverlayProject(t, overlay)
		p.WriteFile(f.fixture, f.bad)

		res := runOnline(t, p.Dir, cacheDir, "fix")
		if res.ExitCode != 0 {
			t.Fatalf("fix exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
				res.ExitCode, res.Stdout, res.Stderr)
		}
		if got := readFixture(p); got == f.bad {
			t.Errorf("fix did not change the file\ncontent stayed:\n%q", got)
		}

		// A second lint over the now-formatted file must pass (the fix was real).
		lintAgain := runOnline(t, p.Dir, cacheDir, "lint")
		if lintAgain.ExitCode != 0 {
			t.Errorf("lint still fails after fix: exit %d\nstdout:\n%s\nstderr:\n%s",
				lintAgain.ExitCode, lintAgain.Stdout, lintAgain.Stderr)
		}
	})

	// --- check runs fix-then-lint: repairs then verifies, ending clean -------
	t.Run("check-fix-then-lint", func(t *testing.T) {
		p := newOverlayProject(t, overlay)
		p.WriteFile(f.fixture, f.bad)

		res := runOnline(t, p.Dir, cacheDir, "check")
		if res.ExitCode != 0 {
			t.Fatalf("check exit = %d, want 0 (fix should repair, then lint passes)\nstdout:\n%s\nstderr:\n%s",
				res.ExitCode, res.Stdout, res.Stderr)
		}
		if got := readFixture(p); got == f.bad {
			t.Errorf("check did not format the file (fix phase produced no change)\ncontent stayed:\n%q", got)
		}
	})
}
