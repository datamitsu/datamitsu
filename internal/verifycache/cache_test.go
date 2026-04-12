package verifycache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStatePath(t *testing.T) {
	t.Run("returns correct path structure", func(t *testing.T) {
		got := StatePath("/cache", "/home/user/project")
		if !strings.HasPrefix(got, "/cache/.verify-state/") {
			t.Errorf("StatePath() = %q, want prefix /cache/.verify-state/", got)
		}
		if !strings.HasSuffix(got, ".json") {
			t.Errorf("StatePath() = %q, want .json suffix", got)
		}
	})

	t.Run("deterministic for same cwd", func(t *testing.T) {
		p1 := StatePath("/cache", "/home/user/project")
		p2 := StatePath("/cache", "/home/user/project")
		if p1 != p2 {
			t.Errorf("StatePath not deterministic: %q != %q", p1, p2)
		}
	})

	t.Run("different cwd produces different path", func(t *testing.T) {
		p1 := StatePath("/cache", "/home/user/project-a")
		p2 := StatePath("/cache", "/home/user/project-b")
		if p1 == p2 {
			t.Errorf("StatePath should differ for different cwds")
		}
	})

	t.Run("filename is 32 hex chars", func(t *testing.T) {
		got := StatePath("/cache", "/home/user/project")
		base := filepath.Base(got)
		name := strings.TrimSuffix(base, ".json")
		if len(name) != 32 {
			t.Errorf("filename length = %d, want 32 (xxh3-128 hex)", len(name))
		}
	})
}

func TestLoadState(t *testing.T) {
	t.Run("returns empty state when file missing", func(t *testing.T) {
		state, err := LoadState("/nonexistent/path/file.json")
		if err != nil {
			t.Fatalf("LoadState() error = %v, want nil for missing file", err)
		}
		if state == nil {
			t.Fatal("LoadState() returned nil state")
		}
		if len(state.Entries) != 0 {
			t.Errorf("LoadState() entries = %d, want 0", len(state.Entries))
		}
	})

	t.Run("returns error on corrupt JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "corrupt.json")
		if err := os.WriteFile(path, []byte("not json{{{"), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		_, err := LoadState(path)
		if err == nil {
			t.Error("LoadState() expected error for corrupt JSON, got nil")
		}
	})

	t.Run("loads valid state file", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")
		data := `{
			"version": 1,
			"cwd": "/test/project",
			"lastRun": "2026-03-04T15:30:45Z",
			"entries": {
				"binary:lefthook:darwin:arm64": {
					"fp": "abc123",
					"status": "ok",
					"ts": "2026-03-04T15:30:45Z"
				}
			}
		}`
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		state, err := LoadState(path)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}
		if state.Version != 1 {
			t.Errorf("Version = %d, want 1", state.Version)
		}
		if state.CWD != "/test/project" {
			t.Errorf("CWD = %q, want %q", state.CWD, "/test/project")
		}
		entry, ok := state.Entries["binary:lefthook:darwin:arm64"]
		if !ok {
			t.Fatal("expected entry for binary:lefthook:darwin:arm64")
		}
		if entry.Fingerprint != "abc123" {
			t.Errorf("Fingerprint = %q, want %q", entry.Fingerprint, "abc123")
		}
		if entry.Status != "ok" {
			t.Errorf("Status = %q, want %q", entry.Status, "ok")
		}
	})
}

func TestSaveState(t *testing.T) {
	t.Run("writes valid JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		state := &VerifyState{
			Version: 1,
			CWD:     "/test/project",
			LastRun: time.Date(2026, 3, 4, 15, 30, 45, 0, time.UTC),
			Entries: map[string]VerifyEntry{
				"binary:lefthook:darwin:arm64": {
					Fingerprint: "abc123",
					Status:      "ok",
					Timestamp:   time.Date(2026, 3, 4, 15, 30, 45, 0, time.UTC),
				},
			},
		}

		if err := SaveState(path, state); err != nil {
			t.Fatalf("SaveState() error = %v", err)
		}

		loaded, err := LoadState(path)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}
		if loaded.Version != 1 {
			t.Errorf("Version = %d, want 1", loaded.Version)
		}
		if loaded.CWD != "/test/project" {
			t.Errorf("CWD = %q, want %q", loaded.CWD, "/test/project")
		}
		entry := loaded.Entries["binary:lefthook:darwin:arm64"]
		if entry.Fingerprint != "abc123" {
			t.Errorf("Fingerprint = %q, want %q", entry.Fingerprint, "abc123")
		}
	})

	t.Run("creates parent directories", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "subdir", "nested", "state.json")

		state := &VerifyState{
			Version: 1,
			Entries: map[string]VerifyEntry{},
		}

		if err := SaveState(path, state); err != nil {
			t.Fatalf("SaveState() error = %v", err)
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Error("SaveState() did not create file")
		}
	})

	t.Run("entries sorted alphabetically in output", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		state := &VerifyState{
			Version: 1,
			Entries: map[string]VerifyEntry{
				"z-entry": {Fingerprint: "z", Status: "ok"},
				"a-entry": {Fingerprint: "a", Status: "ok"},
				"m-entry": {Fingerprint: "m", Status: "ok"},
			},
		}

		if err := SaveState(path, state); err != nil {
			t.Fatalf("SaveState() error = %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		content := string(data)
		aIdx := strings.Index(content, "a-entry")
		mIdx := strings.Index(content, "m-entry")
		zIdx := strings.Index(content, "z-entry")

		if aIdx == -1 || mIdx == -1 || zIdx == -1 {
			t.Fatal("not all entries found in output")
		}
		if aIdx >= mIdx || mIdx >= zIdx {
			t.Errorf("entries not sorted alphabetically: a=%d, m=%d, z=%d", aIdx, mIdx, zIdx)
		}
	})

	t.Run("round-trips through save and load", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		original := &VerifyState{
			Version: 1,
			Entries: map[string]VerifyEntry{
				"existing": {Fingerprint: "orig", Status: "ok"},
			},
		}
		if err := SaveState(path, original); err != nil {
			t.Fatalf("SaveState() error = %v", err)
		}

		loaded, err := LoadState(path)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}
		if loaded.Entries["existing"].Fingerprint != "orig" {
			t.Error("original data not preserved")
		}
	})
}

func TestSaveStateJSONTags(t *testing.T) {
	t.Run("uses short JSON tags", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		ts := time.Date(2026, 3, 4, 15, 30, 45, 0, time.UTC)
		state := &VerifyState{
			Version: 1,
			CWD:     "/test",
			LastRun: ts,
			Entries: map[string]VerifyEntry{
				"test-key": {
					Fingerprint: "fp123",
					Status:      "failed",
					Timestamp:   ts,
					Error:       "hash mismatch",
				},
			},
		}

		if err := SaveState(path, state); err != nil {
			t.Fatalf("SaveState() error = %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		// Verify short tags are used
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		for _, key := range []string{"version", "cwd", "lastRun", "entries"} {
			if _, ok := raw[key]; !ok {
				t.Errorf("expected top-level key %q in JSON", key)
			}
		}

		// Check entry tags
		var parsed VerifyState
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("failed to unmarshal to VerifyState: %v", err)
		}
		entry := parsed.Entries["test-key"]
		if entry.Fingerprint != "fp123" {
			t.Errorf("Fingerprint = %q, want %q", entry.Fingerprint, "fp123")
		}
		if entry.Error != "hash mismatch" {
			t.Errorf("Error = %q, want %q", entry.Error, "hash mismatch")
		}

		content := string(data)
		if !strings.Contains(content, `"fp"`) {
			t.Error("expected short tag 'fp' in JSON output")
		}
		if !strings.Contains(content, `"ts"`) {
			t.Error("expected short tag 'ts' in JSON output")
		}
		if !strings.Contains(content, `"err"`) {
			t.Error("expected short tag 'err' in JSON output")
		}
	})

	t.Run("omits err when empty", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		state := &VerifyState{
			Version: 1,
			Entries: map[string]VerifyEntry{
				"test-key": {
					Fingerprint: "fp123",
					Status:      "ok",
					Timestamp:   time.Now(),
				},
			},
		}

		if err := SaveState(path, state); err != nil {
			t.Fatalf("SaveState() error = %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		content := string(data)
		if strings.Contains(content, `"err"`) {
			t.Error("expected 'err' to be omitted when empty")
		}
	})
}

func TestStateManagerShouldSkip(t *testing.T) {
	t.Run("returns false when no entries", func(t *testing.T) {
		sm := NewStateManager(&VerifyState{
			Version: 1,
			Entries: map[string]VerifyEntry{},
		}, "")
		if sm.ShouldSkip("binary:foo:linux:amd64", "fp123") {
			t.Error("ShouldSkip() = true, want false for empty state")
		}
	})

	t.Run("returns false when key missing", func(t *testing.T) {
		sm := NewStateManager(&VerifyState{
			Version: 1,
			Entries: map[string]VerifyEntry{
				"binary:other:linux:amd64": {Fingerprint: "fp123", Status: "ok"},
			},
		}, "")
		if sm.ShouldSkip("binary:foo:linux:amd64", "fp123") {
			t.Error("ShouldSkip() = true, want false for missing key")
		}
	})

	t.Run("returns false when fingerprint differs", func(t *testing.T) {
		sm := NewStateManager(&VerifyState{
			Version: 1,
			Entries: map[string]VerifyEntry{
				"binary:foo:linux:amd64": {Fingerprint: "old-fp", Status: "ok"},
			},
		}, "")
		if sm.ShouldSkip("binary:foo:linux:amd64", "new-fp") {
			t.Error("ShouldSkip() = true, want false for different fingerprint")
		}
	})

	t.Run("returns false when status is failed", func(t *testing.T) {
		sm := NewStateManager(&VerifyState{
			Version: 1,
			Entries: map[string]VerifyEntry{
				"binary:foo:linux:amd64": {Fingerprint: "fp123", Status: "failed"},
			},
		}, "")
		if sm.ShouldSkip("binary:foo:linux:amd64", "fp123") {
			t.Error("ShouldSkip() = true, want false for failed status")
		}
	})

	t.Run("returns true when fingerprint matches and status ok", func(t *testing.T) {
		sm := NewStateManager(&VerifyState{
			Version: 1,
			Entries: map[string]VerifyEntry{
				"binary:foo:linux:amd64": {Fingerprint: "fp123", Status: "ok"},
			},
		}, "")
		if !sm.ShouldSkip("binary:foo:linux:amd64", "fp123") {
			t.Error("ShouldSkip() = false, want true for matching fingerprint and ok status")
		}
	})
}

func TestStateManagerRecord(t *testing.T) {
	t.Run("updates in-memory state", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		sm := NewStateManager(&VerifyState{
			Version: 1,
			CWD:     "/test",
			Entries: map[string]VerifyEntry{},
		}, path)

		err := sm.Record("binary:foo:linux:amd64", "fp123", "ok", "")
		if err != nil {
			t.Fatalf("Record() error = %v", err)
		}

		if !sm.ShouldSkip("binary:foo:linux:amd64", "fp123") {
			t.Error("after Record, ShouldSkip should return true")
		}
	})

	t.Run("persists to disk", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		sm := NewStateManager(&VerifyState{
			Version: 1,
			CWD:     "/test",
			Entries: map[string]VerifyEntry{},
		}, path)

		if err := sm.Record("binary:foo:linux:amd64", "fp123", "ok", ""); err != nil {
			t.Fatalf("Record() error = %v", err)
		}

		loaded, err := LoadState(path)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}

		entry, ok := loaded.Entries["binary:foo:linux:amd64"]
		if !ok {
			t.Fatal("entry not found in persisted state")
		}
		if entry.Fingerprint != "fp123" {
			t.Errorf("Fingerprint = %q, want %q", entry.Fingerprint, "fp123")
		}
		if entry.Status != "ok" {
			t.Errorf("Status = %q, want %q", entry.Status, "ok")
		}
	})

	t.Run("records error message", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		sm := NewStateManager(&VerifyState{
			Version: 1,
			CWD:     "/test",
			Entries: map[string]VerifyEntry{},
		}, path)

		if err := sm.Record("binary:foo:linux:amd64", "fp123", "failed", "hash mismatch"); err != nil {
			t.Fatalf("Record() error = %v", err)
		}

		loaded, err := LoadState(path)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}

		entry := loaded.Entries["binary:foo:linux:amd64"]
		if entry.Error != "hash mismatch" {
			t.Errorf("Error = %q, want %q", entry.Error, "hash mismatch")
		}
	})

	t.Run("overwrites existing entry", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		sm := NewStateManager(&VerifyState{
			Version: 1,
			CWD:     "/test",
			Entries: map[string]VerifyEntry{
				"binary:foo:linux:amd64": {Fingerprint: "old-fp", Status: "failed", Error: "old error"},
			},
		}, path)

		if err := sm.Record("binary:foo:linux:amd64", "new-fp", "ok", ""); err != nil {
			t.Fatalf("Record() error = %v", err)
		}

		if !sm.ShouldSkip("binary:foo:linux:amd64", "new-fp") {
			t.Error("ShouldSkip should return true after overwriting with ok status")
		}

		loaded, err := LoadState(path)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}
		entry := loaded.Entries["binary:foo:linux:amd64"]
		if entry.Fingerprint != "new-fp" {
			t.Errorf("Fingerprint = %q, want %q", entry.Fingerprint, "new-fp")
		}
		if entry.Error != "" {
			t.Errorf("Error = %q, want empty", entry.Error)
		}
	})

	t.Run("updates lastRun timestamp", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		before := time.Now().UTC().Add(-time.Second)
		sm := NewStateManager(&VerifyState{
			Version: 1,
			CWD:     "/test",
			Entries: map[string]VerifyEntry{},
		}, path)

		if err := sm.Record("binary:foo:linux:amd64", "fp123", "ok", ""); err != nil {
			t.Fatalf("Record() error = %v", err)
		}

		loaded, err := LoadState(path)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}
		if loaded.LastRun.Before(before) {
			t.Errorf("LastRun = %v, expected after %v", loaded.LastRun, before)
		}
	})

	t.Run("concurrent records are safe", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		sm := NewStateManager(&VerifyState{
			Version: 1,
			CWD:     "/test",
			Entries: map[string]VerifyEntry{},
		}, path)

		const n = 20
		errs := make(chan error, n)
		var wg sync.WaitGroup
		wg.Add(n)

		for i := 0; i < n; i++ {
			go func(idx int) {
				defer wg.Done()
				key := fmt.Sprintf("binary:app%d:linux:amd64", idx)
				if err := sm.Record(key, "fp", "ok", ""); err != nil {
					errs <- err
				}
			}(i)
		}

		wg.Wait()
		close(errs)

		for err := range errs {
			t.Errorf("Record() error = %v", err)
		}

		// Verify state is consistent
		loaded, err := LoadState(path)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}
		if len(loaded.Entries) != n {
			t.Errorf("entries count = %d, want %d", len(loaded.Entries), n)
		}
	})
}

func TestStateManagerReset(t *testing.T) {
	t.Run("clears all entries", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")

		sm := NewStateManager(&VerifyState{
			Version: 1,
			CWD:     "/test",
			Entries: map[string]VerifyEntry{
				"binary:foo:linux:amd64": {Fingerprint: "fp123", Status: "ok"},
				"binary:bar:linux:amd64": {Fingerprint: "fp456", Status: "ok"},
			},
		}, path)

		if err := sm.Reset(); err != nil {
			t.Fatalf("Reset() error = %v", err)
		}

		if sm.ShouldSkip("binary:foo:linux:amd64", "fp123") {
			t.Error("ShouldSkip should return false after Reset")
		}

		loaded, err := LoadState(path)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}
		if len(loaded.Entries) != 0 {
			t.Errorf("entries count = %d, want 0 after Reset", len(loaded.Entries))
		}
	})
}
