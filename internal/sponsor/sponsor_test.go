package sponsor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStaticLine(t *testing.T) {
	line := StaticLine()
	if line == "" {
		t.Fatal("StaticLine() returned empty string")
	}
	if line != "Support datamitsu development: https://datamitsu.com/sponsor" {
		t.Errorf("unexpected static line: %s", line)
	}
}

func TestNew(t *testing.T) {
	m := New("/tmp/test-cache")
	if m == nil {
		t.Fatal("New() returned nil")
	}
	if m.cacheDir != "/tmp/test-cache" {
		t.Errorf("cacheDir = %q, want %q", m.cacheDir, "/tmp/test-cache")
	}
	if m.clock == nil {
		t.Error("clock is nil")
	}
	if m.rnd == nil {
		t.Error("rnd is nil")
	}
}

func TestNewWithClock(t *testing.T) {
	clk := newTestClock(time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC))
	m := NewWithClock("/tmp/test-cache", clk)
	if m == nil {
		t.Fatal("NewWithClock() returned nil")
	}
	if m.cacheDir != "/tmp/test-cache" {
		t.Errorf("cacheDir = %q, want %q", m.cacheDir, "/tmp/test-cache")
	}
	if m.clock != clk {
		t.Error("clock does not match provided clock")
	}
	if m.rnd == nil {
		t.Error("rnd is nil")
	}
}

func setupTestManager(t *testing.T, clk *testClock) (*Manager, string) {
	t.Helper()
	tmpDir := t.TempDir()
	m := NewWithClock(tmpDir, clk)
	return m, tmpDir
}

func readState(t *testing.T, cacheDir string) *State {
	t.Helper()
	path := statePath(cacheDir)
	state, err := loadState(path)
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	return state
}

func TestMaybePrint_ActivationThreshold(t *testing.T) {
	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clk := newTestClock(now)

	t.Setenv("CI", "")
	t.Setenv("DATAMITSU_NO_SPONSOR", "")

	t.Run("29 runs does not activate", func(t *testing.T) {
		m, cacheDir := setupTestManager(t, clk)

		for i := 0; i < 29; i++ {
			m.MaybePrint(false)
		}

		state := readState(t, cacheDir)
		if state.Activated {
			t.Error("should not be activated after 29 runs")
		}
		if state.SuccessfulRuns != 29 {
			t.Errorf("SuccessfulRuns = %d, want 29", state.SuccessfulRuns)
		}
	})

	t.Run("30th run activates", func(t *testing.T) {
		m, cacheDir := setupTestManager(t, clk)

		for i := 0; i < 30; i++ {
			m.MaybePrint(false)
		}

		state := readState(t, cacheDir)
		if !state.Activated {
			t.Error("should be activated after 30 runs")
		}
		if state.SuccessfulRuns != 30 {
			t.Errorf("SuccessfulRuns = %d, want 30", state.SuccessfulRuns)
		}
		if state.LastShown.IsZero() {
			t.Error("LastShown should be set after activation")
		}
	})
}

func TestMaybePrint_ReactivationCycle(t *testing.T) {
	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clk := newTestClock(now)

	t.Setenv("CI", "")
	t.Setenv("DATAMITSU_NO_SPONSOR", "")

	m, cacheDir := setupTestManager(t, clk)

	// Activate first
	for i := 0; i < 30; i++ {
		m.MaybePrint(false)
	}

	state := readState(t, cacheDir)
	if !state.Activated {
		t.Fatal("should be activated")
	}

	t.Run("6 days later stays activated", func(t *testing.T) {
		clk.Set(now.Add(6 * 24 * time.Hour))
		m.MaybePrint(false)

		state := readState(t, cacheDir)
		if !state.Activated {
			t.Error("should still be activated after only 6 days")
		}
	})

	t.Run("7 days later resets state", func(t *testing.T) {
		clk.Set(now.Add(7 * 24 * time.Hour))
		m.MaybePrint(false)

		state := readState(t, cacheDir)
		if state.Activated {
			t.Error("should not be activated after reactivation reset")
		}
		if state.SuccessfulRuns != 0 {
			t.Errorf("SuccessfulRuns = %d, want 0 (should be reset)", state.SuccessfulRuns)
		}
		if !state.LastShown.IsZero() {
			t.Error("LastShown should be zero after reactivation reset")
		}
	})

	t.Run("new accumulation cycle after reactivation", func(t *testing.T) {
		// State was reset above. Now accumulate again.
		clk.Set(now.Add(8 * 24 * time.Hour))

		for i := 0; i < 29; i++ {
			m.MaybePrint(false)
		}

		state := readState(t, cacheDir)
		if state.Activated {
			t.Error("should not be activated after 29 runs in new cycle")
		}
		if state.SuccessfulRuns != 29 {
			t.Errorf("SuccessfulRuns = %d, want 29", state.SuccessfulRuns)
		}

		// 30th run should activate again
		m.MaybePrint(false)

		state = readState(t, cacheDir)
		if !state.Activated {
			t.Error("should be activated after 30 runs in new cycle")
		}
	})
}

func TestMaybePrint_SuppressionJSON(t *testing.T) {
	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clk := newTestClock(now)

	t.Setenv("CI", "")
	t.Setenv("DATAMITSU_NO_SPONSOR", "")

	m, cacheDir := setupTestManager(t, clk)

	for i := 0; i < 35; i++ {
		m.MaybePrint(true)
	}

	state := readState(t, cacheDir)
	if state.SuccessfulRuns != 0 {
		t.Errorf("SuccessfulRuns = %d, want 0 (counter should NOT increment in JSON mode - no telemetry when suppressed)", state.SuccessfulRuns)
	}
	if state.Activated {
		t.Error("should not be activated in JSON mode (message display suppressed)")
	}
}

func TestMaybePrint_SuppressionEnvVar(t *testing.T) {
	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clk := newTestClock(now)

	t.Setenv("CI", "")
	t.Setenv("DATAMITSU_NO_SPONSOR", "1")

	m, cacheDir := setupTestManager(t, clk)

	for i := 0; i < 35; i++ {
		m.MaybePrint(false)
	}

	state := readState(t, cacheDir)
	if state.SuccessfulRuns != 0 {
		t.Errorf("SuccessfulRuns = %d, want 0 (counter should NOT increment with DATAMITSU_NO_SPONSOR - no telemetry when suppressed)", state.SuccessfulRuns)
	}
	if state.Activated {
		t.Error("should not be activated with DATAMITSU_NO_SPONSOR (message display suppressed)")
	}
}

func TestMaybePrint_SuppressionCI(t *testing.T) {
	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clk := newTestClock(now)

	t.Setenv("CI", "true")
	t.Setenv("DATAMITSU_NO_SPONSOR", "")

	m, cacheDir := setupTestManager(t, clk)

	for i := 0; i < 35; i++ {
		m.MaybePrint(false)
	}

	state := readState(t, cacheDir)
	if state.SuccessfulRuns != 0 {
		t.Errorf("SuccessfulRuns = %d, want 0 (counter should NOT increment in CI mode - no telemetry when suppressed)", state.SuccessfulRuns)
	}
	if state.Activated {
		t.Error("should not be activated in CI mode (message display suppressed)")
	}
}

func TestMaybePrint_PanicRecovery(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("DATAMITSU_NO_SPONSOR", "")

	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clk := newTestClock(now)
	m, cacheDir := setupTestManager(t, clk)

	// Pre-populate state to 29 runs so the next MaybePrint hits the activation
	// path which calls printMessage -> selectRandomMessage(m.rnd)
	path := statePath(cacheDir)
	preState := &State{SuccessfulRuns: 29}
	if err := saveState(path, preState); err != nil {
		t.Fatal(err)
	}

	// Set rnd to nil to trigger a panic in selectRandomMessage
	m.rnd = nil

	// Should not panic - defer recover handles it
	m.MaybePrint(false)
}

func TestMaybePrint_CorruptStateRecovers(t *testing.T) {
	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clk := newTestClock(now)

	t.Setenv("CI", "")
	t.Setenv("DATAMITSU_NO_SPONSOR", "")

	m, cacheDir := setupTestManager(t, clk)

	// Write corrupt state
	path := statePath(cacheDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	// Should not panic, should start from empty state
	m.MaybePrint(false)

	state := readState(t, cacheDir)
	if state.SuccessfulRuns != 1 {
		t.Errorf("SuccessfulRuns = %d, want 1 (should recover from corrupt state)", state.SuccessfulRuns)
	}
}


func TestPrintMessage(t *testing.T) {
	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clk := newTestClock(now)
	m, _ := setupTestManager(t, clk)

	// Just verify it doesn't panic
	m.printMessage()
}
