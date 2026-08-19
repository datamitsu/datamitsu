package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/engine"
	"github.com/datamitsu/datamitsu/internal/facts"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/timing"
)

// writeTimingFixtureConfig writes a minimal valid config at path.
func writeTimingFixtureConfig(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { apps: {} }; }
`)
}

// enableStartupTimings turns startup instrumentation on for one test and
// guarantees the process-global recorder is empty before and after it.
func enableStartupTimings(t *testing.T) {
	t.Helper()
	t.Setenv("DATAMITSU_STARTUP_TIMINGS", "1")
	timing.ResetStartupPhases()
	t.Cleanup(timing.ResetStartupPhases)
}

// phaseCounts collapses the recorded phases into name -> count, failing if a
// name was recorded under two separate entries (which would mean the aggregate
// keyed by name drifted).
func phaseCounts(t *testing.T) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	for _, p := range timing.StartupPhases() {
		if _, dup := counts[p.Name]; dup {
			t.Fatalf("phase %q appears in more than one entry", p.Name)
		}
		counts[p.Name] = p.Count
	}
	return counts
}

// TestStartupTimingsSilentWhenDisabled asserts a full config load records
// nothing at all unless the env var opts in — the instrumentation must not
// exist on the default path.
func TestStartupTimingsSilentWhenDisabled(t *testing.T) {
	t.Setenv("DATAMITSU_STARTUP_TIMINGS", "")
	timing.ResetStartupPhases()
	t.Cleanup(timing.ResetStartupPhases)

	root := setupGitRoot(t)
	writeTimingFixtureConfig(t, filepath.Join(root, ldflags.PackageName+".config.js"))

	if _, _, _, err := loadConfigWithPaths(context.Background(), nil, false, nil); err != nil {
		t.Fatalf("loadConfigWithPaths() error = %v", err)
	}

	if got := timing.StartupPhases(); len(got) != 0 {
		t.Fatalf("recorded %d phases with instrumentation disabled, want 0: %+v", len(got), got)
	}
}

// TestStartupTimingsPhasesPerLoad pins the recorded vocabulary and the number of
// observations each phase gets for a known config layout. It is the guard the
// later optimisation tasks are measured against: dropping a redundant
// engine.New or StripTypes call must show up here as a smaller count.
func TestStartupTimingsPhasesPerLoad(t *testing.T) {
	enableStartupTimings(t)

	root := setupGitRoot(t)
	writeTimingFixtureConfig(t, filepath.Join(root, ldflags.PackageName+".config.js"))

	if _, _, _, err := loadConfigWithPaths(context.Background(), nil, false, nil); err != nil {
		t.Fatalf("loadConfigWithPaths() error = %v", err)
	}

	counts := phaseCounts(t)

	// One load, one git-root lookup from the loader, one before-config pre-pass.
	for _, name := range []string{
		timing.PhaseLoadConfig,
		timing.PhaseGitRoot,
		timing.PhaseDiscoverBeforeConfigs,
	} {
		if counts[name] != 1 {
			t.Errorf("phase %q recorded %d times, want exactly 1", name, counts[name])
		}
	}

	// The auto config is the only file source, but it is read and stripped
	// twice: once by the getBeforeConfigs pre-pass and once as a config source.
	// The embedded default config is not routed through this seam.
	if counts[timing.PhaseStripTypes] != 2 {
		t.Errorf("phase %q recorded %d times, want 2", timing.PhaseStripTypes, counts[timing.PhaseStripTypes])
	}

	// Two sources (default + auto) each get their own VM and getConfig() call,
	// plus one engine for the discoverBeforeConfigs pre-pass.
	if counts[timing.PhaseGetConfig] != 2 {
		t.Errorf("phase %q recorded %d times, want 2", timing.PhaseGetConfig, counts[timing.PhaseGetConfig])
	}
	if counts[timing.PhaseEngineNew] != 3 {
		t.Errorf("phase %q recorded %d times, want 3", timing.PhaseEngineNew, counts[timing.PhaseEngineNew])
	}
}

// TestStartupTimingsNoAutoConfig asserts the phases that belong to git
// discovery are absent when there is nothing to discover — the instrumentation
// reports what actually ran, not a fixed template.
func TestStartupTimingsNoAutoConfig(t *testing.T) {
	enableStartupTimings(t)

	if _, _, _, err := loadConfigWithPaths(context.Background(), nil, true, nil); err != nil {
		t.Fatalf("loadConfigWithPaths() error = %v", err)
	}

	counts := phaseCounts(t)

	if counts[timing.PhaseLoadConfig] != 1 {
		t.Errorf("phase %q recorded %d times, want 1", timing.PhaseLoadConfig, counts[timing.PhaseLoadConfig])
	}
	for _, name := range []string{
		timing.PhaseGitRoot,
		timing.PhaseDiscoverBeforeConfigs,
		timing.PhaseStripTypes,
	} {
		if counts[name] != 0 {
			t.Errorf("phase %q recorded %d times with --no-auto-config, want 0", name, counts[name])
		}
	}
}

// BenchmarkLoadConfig exercises the same config-load path `exec` uses: git-root
// discovery, the getBeforeConfigs pre-pass, and one engine + getConfig per
// source. It is the headline number for Tasks 3, 4 and 5.
func BenchmarkLoadConfig(b *testing.B) {
	root := benchGitRoot(b)
	writeBenchFile(b, filepath.Join(root, ldflags.PackageName+".config.js"), `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { apps: {} }; }
`)

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, _, _, err := loadConfigWithPaths(ctx, nil, false, nil); err != nil {
			b.Fatalf("loadConfigWithPaths() error = %v", err)
		}
	}
}

// BenchmarkGetGitRoot is the direct target for Tasks 3 and 6: today every call
// forks at least two git processes.
func BenchmarkGetGitRoot(b *testing.B) {
	benchGitRoot(b)

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := facts.GetGitRoot(ctx); err != nil {
			b.Fatalf("GetGitRoot() error = %v", err)
		}
	}
}

// BenchmarkEngineNew is the direct target for Task 4: four engines are built per
// `exec`, each collecting the same facts.
func BenchmarkEngineNew(b *testing.B) {
	benchGitRoot(b)

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := engine.New(ctx, ""); err != nil {
			b.Fatalf("engine.New() error = %v", err)
		}
	}
}

// benchGitRoot is setupGitRoot for benchmarks: an isolated git repo, entered via
// b.Chdir so the measured path resolves against a fixture rather than this repo.
func benchGitRoot(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		b.Fatalf("EvalSymlinks: %v", err)
	}
	if err := runGitInit(resolved); err != nil {
		b.Skipf("git is not available: %v", err)
	}
	b.Chdir(resolved)
	return resolved
}

// runGitInit creates a git repository in dir, returning git's error verbatim so
// callers can skip when git is unavailable.
func runGitInit(dir string) error {
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	return cmd.Run()
}

func writeBenchFile(b *testing.B, path, content string) {
	b.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
}
