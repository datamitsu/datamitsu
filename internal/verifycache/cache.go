// Package verifycache tracks per-CWD verification state so already-verified
// binaries, runtimes and bundles can be skipped on subsequent runs.
package verifycache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/utils"
)

// VerifyEntry records the result of verifying a single keyed item.
type VerifyEntry struct {
	Fingerprint string    `json:"fp"`
	Status      string    `json:"status"`
	Timestamp   time.Time `json:"ts"`
	Error       string    `json:"err,omitempty"`
}

// VerifyState is the persisted verification state for a single CWD.
type VerifyState struct {
	Version int                    `json:"version"`
	CWD     string                 `json:"cwd"`
	LastRun time.Time              `json:"lastRun"`
	Entries map[string]VerifyEntry `json:"entries"`
}

// StatePath returns the state file path for the given cache directory and CWD.
func StatePath(cacheDir, cwd string) string {
	hash := hashutil.XXH3Hex([]byte(cwd))
	return filepath.Join(cacheDir, ".verify-state", hash+".json")
}

// LoadState reads the verification state from path, returning a fresh empty
// state when the file does not yet exist.
func LoadState(path string) (*VerifyState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &VerifyState{
				Version: 1,
				Entries: map[string]VerifyEntry{},
			}, nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state VerifyState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}
	if state.Entries == nil {
		state.Entries = map[string]VerifyEntry{}
	}
	return &state, nil
}

// SaveState atomically writes state to path via a temp file and rename.
func SaveState(path string, state *VerifyState) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	data = append(data, '\n')

	tmpFile, err := os.CreateTemp(dir, ".verify-state-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := utils.RenameReplace(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// StateManager provides concurrency-safe access to a VerifyState and persists
// changes to disk.
type StateManager struct {
	mu    sync.RWMutex
	state *VerifyState
	path  string
}

// NewStateManager wraps state with a manager that persists to path.
func NewStateManager(state *VerifyState, path string) *StateManager {
	return &StateManager{
		state: state,
		path:  path,
	}
}

// ShouldSkip reports whether the item for key was already verified ok with a
// matching fingerprint.
func (sm *StateManager) ShouldSkip(key, fingerprint string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	entry, ok := sm.state.Entries[key]
	if !ok {
		return false
	}
	return entry.Fingerprint == fingerprint && entry.Status == "ok"
}

// Record stores the verification result for key and persists the state.
func (sm *StateManager) Record(key, fingerprint, status, errMsg string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now().UTC()
	sm.state.Entries[key] = VerifyEntry{
		Fingerprint: fingerprint,
		Status:      status,
		Timestamp:   now,
		Error:       errMsg,
	}
	sm.state.LastRun = now

	if sm.path == "" {
		return nil
	}
	return SaveState(sm.path, sm.state)
}

// Reset clears all recorded entries and persists the empty state.
func (sm *StateManager) Reset() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.state.Entries = map[string]VerifyEntry{}
	sm.state.LastRun = time.Now().UTC()

	if sm.path == "" {
		return nil
	}
	return SaveState(sm.path, sm.state)
}
