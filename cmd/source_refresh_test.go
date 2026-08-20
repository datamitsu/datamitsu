package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

// TestSummarizeRefreshCounts asserts the one line a refresh prints reports what
// the user needs to decide whether anything is wrong: how many names the farm
// now provides, how many of them still have to be downloaded, and how many
// declared names were refused.
func TestSummarizeRefreshCounts(t *testing.T) {
	var buf bytes.Buffer
	summarizeRefresh(&buf, bakeResult{Plan: statusPlan(t)}, sourceTarget{Origin: sourcefarm.OriginGitRoot, Root: "/repo"})

	line := buf.String()
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("summary is not exactly one line:\n%s", line)
	}
	for _, want := range []string{"baked 2 tool(s)", "/repo", "1 not downloaded yet", "2 excluded"} {
		if !strings.Contains(line, want) {
			t.Errorf("summary is missing %q:\n%s", want, line)
		}
	}
}

// TestSummarizeRefreshEmptyFarm asserts a project declaring nothing still gets a
// summary. Silence would be indistinguishable from the command not running.
func TestSummarizeRefreshEmptyFarm(t *testing.T) {
	var buf bytes.Buffer
	summarizeRefresh(&buf, bakeResult{Plan: sourcefarm.Plan{Root: "/repo"}}, sourceTarget{Origin: sourcefarm.OriginGitRoot, Root: "/repo"})

	if !strings.Contains(buf.String(), "baked 0 tool(s)") {
		t.Errorf("empty farm produced no usable summary:\n%s", buf.String())
	}
}

// TestSummarizeRefreshReportsFailedBake asserts a refresh whose materialization
// failed does not claim to have baked anything. The previous farm is still what
// is on PATH, and a success line here would send the user away believing the
// repair they just ran had taken effect.
func TestSummarizeRefreshReportsFailedBake(t *testing.T) {
	var buf bytes.Buffer
	summarizeRefresh(&buf, bakeResult{
		Plan:           statusPlan(t),
		MaterializeErr: errors.New("no space left on device"),
	}, sourceTarget{Origin: sourcefarm.OriginGitRoot, Root: "/repo"})

	out := buf.String()
	if strings.Contains(out, "tool(s)") {
		t.Errorf("a failed bake reported a bake count:\n%s", out)
	}
	// The error text itself is deliberately absent: sourcefarm already put it on
	// stderr and runSourceRefresh returns it for the process to print. What this
	// line owns is which farm the user is left with.
	if strings.Contains(out, "no space left on device") {
		t.Errorf("the summary repeats an error reported twice already:\n%s", out)
	}
	for _, want := range []string{"was not re-baked", "previous one is left in place", "/repo"} {
		if !strings.Contains(out, want) {
			t.Errorf("failure summary is missing %q:\n%s", want, out)
		}
	}
}

// TestSourceFarmIsFresh covers the check that decides whether a plain `refresh`
// is a no-op, across all four manifest states.
func TestSourceFarmIsFresh(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)

	root := t.TempDir()
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		t.Fatalf("GetProjectManifestPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatalf("create per-root directory: %v", err)
	}

	// Nothing baked here yet.
	if sourceFarmIsFresh(gitRootTarget(t, root)) {
		t.Error("an unbaked root reported fresh")
	}

	// A file that will not decode must not be reported fresh either — it is the
	// state a refresh most needs to be able to repair.
	if err := os.WriteFile(manifestPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if sourceFarmIsFresh(gitRootTarget(t, root)) {
		t.Error("an unreadable manifest reported fresh")
	}

	// A manifest whose watch set still matches the tree, with the farm it
	// describes still on disk, is the no-op case.
	farmDir, err := env.GetProjectBinPath(root)
	if err != nil {
		t.Fatalf("GetProjectBinPath() error = %v", err)
	}
	if err := os.MkdirAll(farmDir, 0o700); err != nil {
		t.Fatalf("create farm directory: %v", err)
	}
	watched := filepath.Join(root, "datamitsu.config.js")
	if err := os.WriteFile(watched, []byte("//\n"), 0o600); err != nil {
		t.Fatalf("write watched file: %v", err)
	}
	m := sourcefarm.BuildManifest(sourcefarm.Plan{Root: root, FarmDir: farmDir}, sourcefarm.OriginGitRoot,
		sourcefarm.WatchSet(sourcefarm.WatchPaths(root, []string{watched})))
	data, err := sourcefarm.Encode(m)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if !sourceFarmIsFresh(gitRootTarget(t, root)) {
		t.Error("a manifest matching the tree reported stale")
	}

	// A farm deleted out from under an otherwise perfect manifest is the state a
	// refresh most needs to repair: every declared tool has fallen through to
	// whatever the system has, and answering "already up to date" would refuse
	// the repair.
	if err := os.RemoveAll(farmDir); err != nil {
		t.Fatalf("remove farm directory: %v", err)
	}
	if sourceFarmIsFresh(gitRootTarget(t, root)) {
		t.Error("a manifest whose farm was deleted reported fresh")
	}
	if err := os.MkdirAll(farmDir, 0o700); err != nil {
		t.Fatalf("recreate farm directory: %v", err)
	}

	// Touching a watched file is exactly the transition --force exists to
	// substitute for when the watch set cannot see the change.
	if err := os.WriteFile(watched, []byte("// changed\n"), 0o600); err != nil {
		t.Fatalf("rewrite watched file: %v", err)
	}
	if sourceFarmIsFresh(gitRootTarget(t, root)) {
		t.Error("a changed config reported fresh")
	}
}
