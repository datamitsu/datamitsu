// Package appstate loads, validates and persists the binaryApps.json state
// that tracks managed GitHub app metadata and their installed binaries.
package appstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/jsonsort"
	"github.com/datamitsu/datamitsu/internal/releaseprovider"
)

// AppMetadata represents GitHub app metadata
type AppMetadata struct {
	Source     string            `json:"source"`
	Repository string            `json:"repository"`
	Tag        string            `json:"tag"`
	Hashes     map[string]string `json:"hashes,omitempty"`
	Checksums  map[string]string `json:"checksums,omitempty"`
}

// Origin records the instance and repository that produced a binary entry.
type Origin struct {
	APIURL     string `json:"apiUrl,omitempty"`
	Type       string `json:"type"`
	URL        string `json:"url"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
}

// BinariesEntry represents binaries for a single app with metadata
type BinariesEntry struct {
	Source      *Origin                  `json:"source,omitempty"`
	ConfigHash  string                   `json:"configHash,omitempty"` // Hash of app metadata and target platforms
	Description string                   `json:"description,omitempty"`
	Binaries    binmanager.MapOfBinaries `json:"binaries"`
}

// State represents the binaryApps.json structure
type State struct {
	Sources   map[string]releaseprovider.Source `json:"sources"`
	Platforms []string                          `json:"platforms,omitempty"`
	Apps      map[string]*AppMetadata           `json:"apps"`
	Binaries  map[string]*BinariesEntry         `json:"binaries"`
}

// Load reads and parses binaryApps.json
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read binaryApps.json: %w", err)
	}

	var state State
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("failed to parse binaryApps.json: %w", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("failed to parse manifest fields: %w", err)
	}
	if raw, present := fields["platforms"]; present && (string(raw) == "null" || len(state.Platforms) == 0) {
		return nil, errors.New("platforms must be a non-empty array of supported platform identifiers")
	}
	if err := state.ValidateSources(); err != nil {
		return nil, err
	}
	if err := ValidatePlatforms(state.Platforms); err != nil {
		return nil, err
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

// Save writes the state to binaryApps.json with proper formatting
func Save(path string, state *State) error {
	// Keys sorted, so a pull's diff shows what changed and nothing else.
	data, err := jsonsort.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Add trailing newline
	data = append(data, '\n')

	// Written beside the file and renamed into place: pull-releases saves after
	// every app, and a reader must find either the previous file or the whole
	// new one, never a truncated one from a write that failed part-way. A
	// symlink to a shared registry is followed, so the registry is what gets
	// updated and the link survives.
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("failed to write binaryApps.json: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write binaryApps.json: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write binaryApps.json: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write binaryApps.json: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write binaryApps.json: %w", err)
	}

	return nil
}

// Validate ensures the app metadata has required fields
func Validate(appName string, metadata *AppMetadata) error {
	if metadata == nil {
		return fmt.Errorf("app '%s' not found in binaryApps.json", appName)
	}

	if metadata.Source == "" || metadata.Repository == "" {
		return fmt.Errorf("app '%s' is missing 'source' or 'repository' field", appName)
	}

	if err := releaseprovider.ValidateRepository("gitlab", metadata.Repository); err != nil {
		return err
	}

	if err := releaseprovider.ValidateTag(metadata.Tag); err != nil {
		return fmt.Errorf("app %q tag: %w", appName, err)
	}

	return nil
}

// ComputeConfigHash includes source identity, integrity pins and a sorted unique selection.
func ComputeConfigHash(metadata *AppMetadata, selections []string, sources ...releaseprovider.Source) string {
	source := releaseprovider.Source{Type: "github", URL: "https://github.com"}
	if len(sources) > 0 {
		source = sources[0]
	}
	input := struct {
		App       *AppMetadata           `json:"app"`
		Source    releaseprovider.Source `json:"source"`
		Platforms []string               `json:"platforms"`
	}{metadata, source, slices.Clone(selections)}
	if input.Platforms != nil {
		slices.Sort(input.Platforms)
		input.Platforms = slices.Compact(input.Platforms)
	}
	data, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}
	return hashutil.XXH3Hex(data)
}

// ValidateSources rejects malformed manifests before pruning or networking.
func (s *State) ValidateSources() error {
	if s.Sources == nil {
		s.Sources = make(map[string]releaseprovider.Source)
	}
	for name, source := range s.Sources {
		if name == "" {
			return errors.New("source alias must not be empty")
		}
		if err := source.Validate(); err != nil {
			return fmt.Errorf("source %q: %w", name, err)
		}
	}
	for name, app := range s.Apps {
		if err := Validate(name, app); err != nil {
			return err
		}
		source, ok := s.Sources[app.Source]
		if !ok {
			return fmt.Errorf("app %q references unknown source %q", name, app.Source)
		}
		if err := releaseprovider.ValidateRepository(source.Type, app.Repository); err != nil {
			return fmt.Errorf("app %q: %w", name, err)
		}
		if err := releaseprovider.ValidateHashPins(app.Hashes, app.Checksums); err != nil {
			return fmt.Errorf("app %q: %w", name, err)
		}
	}
	return nil
}
