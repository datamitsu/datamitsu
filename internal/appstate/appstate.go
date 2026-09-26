// Package appstate loads, validates and persists the githubApps.json state
// that tracks managed GitHub app metadata and their installed binaries.
package appstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/hashutil"
)

// AppMetadata represents GitHub app metadata
type AppMetadata struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Tag   string `json:"tag"`
}

// BinariesEntry represents binaries for a single app with metadata
type BinariesEntry struct {
	ConfigHash  string                   `json:"configHash,omitempty"` // Hash of owner:repo:tag
	Description string                   `json:"description,omitempty"`
	Binaries    binmanager.MapOfBinaries `json:"binaries"`
}

// State represents the githubApps.json structure
type State struct {
	Apps     map[string]*AppMetadata   `json:"apps"`
	Binaries map[string]*BinariesEntry `json:"binaries"`
}

// Load reads and parses githubApps.json
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read githubApps.json: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse githubApps.json: %w", err)
	}

	// Initialize maps if nil
	if state.Apps == nil {
		state.Apps = make(map[string]*AppMetadata)
	}
	if state.Binaries == nil {
		state.Binaries = make(map[string]*BinariesEntry)
	}

	return &state, nil
}

// Save writes the state to githubApps.json with proper formatting
func Save(path string, state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Add trailing newline
	data = append(data, '\n')

	// Written beside the file and renamed into place: pull-github saves after
	// every app, and a reader must find either the previous file or the whole
	// new one, never a truncated one from a write that failed part-way. A
	// symlink to a shared registry is followed, so the registry is what gets
	// updated and the link survives.
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("failed to write githubApps.json: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write githubApps.json: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write githubApps.json: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write githubApps.json: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write githubApps.json: %w", err)
	}

	return nil
}

// Validate ensures the app metadata has required fields
func Validate(appName string, metadata *AppMetadata) error {
	if metadata == nil {
		return fmt.Errorf("app '%s' not found in githubApps.json", appName)
	}

	if metadata.Owner == "" || metadata.Repo == "" {
		return fmt.Errorf("app '%s' is missing 'owner' or 'repo' field", appName)
	}

	if metadata.Tag == "" {
		return fmt.Errorf("app '%s' is missing 'tag' field", appName)
	}

	return nil
}

// ComputeConfigHash computes an XXH3-128 hash for app configuration (owner:repo:tag).
func ComputeConfigHash(metadata *AppMetadata) string {
	return hashutil.XXH3Multi(
		[]byte(metadata.Owner),
		[]byte(metadata.Repo),
		[]byte(metadata.Tag),
	)
}
