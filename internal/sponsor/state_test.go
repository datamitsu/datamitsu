package sponsor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatePath(t *testing.T) {
	got := statePath("/home/user/.cache/datamitsu")
	want := filepath.Join("/home/user/.cache/datamitsu", ".sponsor", "state.json")
	if got != want {
		t.Errorf("statePath() = %q, want %q", got, want)
	}
}

func TestLoadState_MissingFile(t *testing.T) {
	state, err := loadState("/nonexistent/path/state.json")
	if err != nil {
		t.Fatalf("loadState() returned error for missing file: %v", err)
	}
	if state.Activated {
		t.Error("expected Activated to be false")
	}
	if state.SuccessfulRuns != 0 {
		t.Errorf("expected SuccessfulRuns to be 0, got %d", state.SuccessfulRuns)
	}
	if !state.LastShown.IsZero() {
		t.Errorf("expected LastShown to be zero, got %v", state.LastShown)
	}
}

func TestLoadState_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	ts := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	data := `{
  "activated": true,
  "successful_runs": 42,
  "last_shown": "2026-04-05T12:00:00Z"
}
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	state, err := loadState(path)
	if err != nil {
		t.Fatalf("loadState() returned error: %v", err)
	}
	if !state.Activated {
		t.Error("expected Activated to be true")
	}
	if state.SuccessfulRuns != 42 {
		t.Errorf("expected SuccessfulRuns to be 42, got %d", state.SuccessfulRuns)
	}
	if !state.LastShown.Equal(ts) {
		t.Errorf("expected LastShown to be %v, got %v", ts, state.LastShown)
	}
}

func TestLoadState_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := os.WriteFile(path, []byte("not valid json{{{"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := loadState(path)
	if err == nil {
		t.Fatal("loadState() should return error for corrupt JSON")
	}
}

func TestSaveState_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".sponsor", "state.json")

	ts := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	state := &State{
		Activated:      true,
		SuccessfulRuns: 30,
		LastShown:      ts,
	}

	if err := saveState(path, state); err != nil {
		t.Fatalf("saveState() returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read saved state: %v", err)
	}

	var loaded State
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("failed to unmarshal saved state: %v", err)
	}
	if !loaded.Activated {
		t.Error("expected Activated to be true")
	}
	if loaded.SuccessfulRuns != 30 {
		t.Errorf("expected SuccessfulRuns to be 30, got %d", loaded.SuccessfulRuns)
	}
	if !loaded.LastShown.Equal(ts) {
		t.Errorf("expected LastShown to be %v, got %v", ts, loaded.LastShown)
	}
}

func TestSaveState_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dir", "state.json")

	state := &State{SuccessfulRuns: 5}
	if err := saveState(path, state); err != nil {
		t.Fatalf("saveState() returned error: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file was not created: %v", err)
	}
}

func TestSaveState_AtomicOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	first := &State{SuccessfulRuns: 10}
	if err := saveState(path, first); err != nil {
		t.Fatalf("first saveState() returned error: %v", err)
	}

	second := &State{SuccessfulRuns: 20, Activated: true}
	if err := saveState(path, second); err != nil {
		t.Fatalf("second saveState() returned error: %v", err)
	}

	loaded, err := loadState(path)
	if err != nil {
		t.Fatalf("loadState() returned error: %v", err)
	}
	if loaded.SuccessfulRuns != 20 {
		t.Errorf("expected SuccessfulRuns to be 20, got %d", loaded.SuccessfulRuns)
	}
	if !loaded.Activated {
		t.Error("expected Activated to be true after overwrite")
	}
}

func TestSaveState_NoTempFileLeftOver(t *testing.T) {
	dir := t.TempDir()
	sponsorDir := filepath.Join(dir, ".sponsor")
	path := filepath.Join(sponsorDir, "state.json")

	state := &State{SuccessfulRuns: 1}
	if err := saveState(path, state); err != nil {
		t.Fatalf("saveState() returned error: %v", err)
	}

	entries, err := os.ReadDir(sponsorDir)
	if err != nil {
		t.Fatalf("failed to read directory: %v", err)
	}

	for _, e := range entries {
		if e.Name() != "state.json" {
			t.Errorf("unexpected file left in directory: %s", e.Name())
		}
	}
}
