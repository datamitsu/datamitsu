package appstate

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"encoding/json"
	"fmt"
	"os"
)

// AppMetadata represents GitHub app metadata
type AppMetadata struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Tag   string `json:"tag"`
}

// BinariesEntry represents binaries for a single app with metadata
type BinariesEntry struct {
	ConfigHash  string                       `json:"configHash,omitempty"` // Hash of owner:repo:tag
	Description string                       `json:"description,omitempty"`
	Binaries    binmanager.MapOfBinaries     `json:"binaries"`
}

// State represents the githubApps.json structure
type State struct {
	Apps     map[string]*AppMetadata      `json:"apps"`
	Binaries map[string]*BinariesEntry    `json:"binaries"`
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

	if err := os.WriteFile(path, data, 0644); err != nil {
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
