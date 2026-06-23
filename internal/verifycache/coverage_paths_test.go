package verifycache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveState_MkdirError(t *testing.T) {
	dir := t.TempDir()
	fileAsDir := filepath.Join(dir, "blocker")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(fileAsDir, "sub", "state.json")
	state := &VerifyState{Version: 1, Entries: map[string]VerifyEntry{}}
	if err := SaveState(path, state); err == nil {
		t.Error("SaveState() expected error when parent path is a file, got nil")
	}
}

func TestLoadState_ParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(path); err == nil {
		t.Error("LoadState() expected parse error, got nil")
	}
}

func TestStateManager_EmptyPath_NoPersist(t *testing.T) {
	sm := NewStateManager(&VerifyState{Version: 1, Entries: map[string]VerifyEntry{}}, "")

	if err := sm.Record("k", "fp", "ok", ""); err != nil {
		t.Fatalf("Record() with empty path error = %v", err)
	}
	if !sm.ShouldSkip("k", "fp") {
		t.Error("ShouldSkip() = false after recording ok entry")
	}
	if err := sm.Reset(); err != nil {
		t.Fatalf("Reset() with empty path error = %v", err)
	}
	if sm.ShouldSkip("k", "fp") {
		t.Error("ShouldSkip() = true after Reset()")
	}
}

func TestStateManager_Record_SaveError(t *testing.T) {
	dir := t.TempDir()
	fileAsDir := filepath.Join(dir, "blocker")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(fileAsDir, "sub", "state.json")

	sm := NewStateManager(&VerifyState{Version: 1, Entries: map[string]VerifyEntry{}}, badPath)
	if err := sm.Record("k", "fp", "ok", ""); err == nil {
		t.Error("Record() expected save error with read-only path, got nil")
	}
	if err := sm.Reset(); err == nil {
		t.Error("Reset() expected save error with read-only path, got nil")
	}
}

func TestStateManager_Record_PersistsAndLoads(t *testing.T) {
	dir := t.TempDir()
	path := StatePath(dir, "/some/cwd")

	sm := NewStateManager(&VerifyState{Version: 1, Entries: map[string]VerifyEntry{}}, path)
	if err := sm.Record("key", "fp1", "ok", ""); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	entry, ok := loaded.Entries["key"]
	if !ok || entry.Fingerprint != "fp1" || entry.Status != "ok" {
		t.Errorf("persisted entry = %+v, want fp1/ok", entry)
	}
}
