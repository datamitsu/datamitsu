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

type VerifyEntry struct {
	Fingerprint string    `json:"fp"`
	Status      string    `json:"status"`
	Timestamp   time.Time `json:"ts"`
	Error       string    `json:"err,omitempty"`
}

type VerifyState struct {
	Version int                    `json:"version"`
	CWD     string                 `json:"cwd"`
	LastRun time.Time              `json:"lastRun"`
	Entries map[string]VerifyEntry `json:"entries"`
}

func StatePath(cacheDir, cwd string) string {
	hash := hashutil.XXH3Hex([]byte(cwd))
	return filepath.Join(cacheDir, ".verify-state", hash+".json")
}

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

func SaveState(path string, state *VerifyState) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
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

type StateManager struct {
	mu    sync.RWMutex
	state *VerifyState
	path  string
}

func NewStateManager(state *VerifyState, path string) *StateManager {
	return &StateManager{
		state: state,
		path:  path,
	}
}

func (sm *StateManager) ShouldSkip(key, fingerprint string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	entry, ok := sm.state.Entries[key]
	if !ok {
		return false
	}
	return entry.Fingerprint == fingerprint && entry.Status == "ok"
}

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
