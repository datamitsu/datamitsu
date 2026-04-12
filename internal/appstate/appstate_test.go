package appstate

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Run("valid JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "test.json")

		testState := &State{
			Apps: map[string]*AppMetadata{
				"testapp": {
					Owner: "owner",
					Repo:  "repo",
					Tag:   "v1.0.0",
				},
			},
			Binaries: map[string]*BinariesEntry{
				"testbin": {
					ConfigHash: "abc123",
					Binaries:   binmanager.MapOfBinaries{},
				},
			},
		}

		data, err := json.Marshal(testState)
		if err != nil {
			t.Fatalf("failed to marshal test state: %v", err)
		}

		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		state, err := Load(path)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if len(state.Apps) != 1 {
			t.Errorf("expected 1 app, got %d", len(state.Apps))
		}

		if state.Apps["testapp"].Owner != "owner" {
			t.Errorf("expected owner 'owner', got '%s'", state.Apps["testapp"].Owner)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "invalid.json")

		if err := os.WriteFile(path, []byte("{invalid json}"), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		_, err := Load(path)
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "nonexistent.json")

		_, err := Load(path)
		if err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})

	t.Run("empty file", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "empty.json")

		if err := os.WriteFile(path, []byte(""), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		_, err := Load(path)
		if err == nil {
			t.Error("expected error for empty file, got nil")
		}
	})

	t.Run("initializes nil maps", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "empty-maps.json")

		if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		state, err := Load(path)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if state.Apps == nil {
			t.Error("Apps map should be initialized, got nil")
		}

		if state.Binaries == nil {
			t.Error("Binaries map should be initialized, got nil")
		}
	})
}

func TestSave(t *testing.T) {
	t.Run("successful save", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "test.json")

		testState := &State{
			Apps: map[string]*AppMetadata{
				"testapp": {
					Owner: "owner",
					Repo:  "repo",
					Tag:   "v1.0.0",
				},
			},
			Binaries: map[string]*BinariesEntry{
				"testbin": {
					ConfigHash: "abc123",
					Binaries:   binmanager.MapOfBinaries{},
				},
			},
		}

		err := Save(path, testState)
		if err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Error("saved file does not exist")
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read saved file: %v", err)
		}

		if len(data) == 0 {
			t.Error("saved file is empty")
		}

		var loadedState State
		if err := json.Unmarshal(data, &loadedState); err != nil {
			t.Fatalf("failed to unmarshal saved file: %v", err)
		}

		if len(loadedState.Apps) != 1 {
			t.Errorf("expected 1 app, got %d", len(loadedState.Apps))
		}
	})

	t.Run("proper formatting with indentation", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "formatted.json")

		testState := &State{
			Apps: map[string]*AppMetadata{
				"testapp": {
					Owner: "owner",
					Repo:  "repo",
					Tag:   "v1.0.0",
				},
			},
			Binaries: map[string]*BinariesEntry{},
		}

		err := Save(path, testState)
		if err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read saved file: %v", err)
		}

		content := string(data)
		if content[0] != '{' {
			t.Error("JSON should start with {")
		}

		if content[len(content)-1] != '\n' {
			t.Error("JSON should end with newline")
		}

		if !contains(content, "  ") {
			t.Error("JSON should be indented with 2 spaces")
		}
	})

	t.Run("trailing newline", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "newline.json")

		testState := &State{
			Apps:     map[string]*AppMetadata{},
			Binaries: map[string]*BinariesEntry{},
		}

		err := Save(path, testState)
		if err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read saved file: %v", err)
		}

		if len(data) == 0 || data[len(data)-1] != '\n' {
			t.Error("file should end with newline")
		}
	})

	t.Run("invalid directory", func(t *testing.T) {
		path := "/nonexistent/directory/test.json"

		testState := &State{
			Apps:     map[string]*AppMetadata{},
			Binaries: map[string]*BinariesEntry{},
		}

		err := Save(path, testState)
		if err == nil {
			t.Error("expected error for invalid directory, got nil")
		}
	})
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name        string
		appName     string
		metadata    *AppMetadata
		expectError bool
	}{
		{
			name:    "valid metadata",
			appName: "testapp",
			metadata: &AppMetadata{
				Owner: "owner",
				Repo:  "repo",
				Tag:   "v1.0.0",
			},
			expectError: false,
		},
		{
			name:        "nil metadata",
			appName:     "testapp",
			metadata:    nil,
			expectError: true,
		},
		{
			name:    "missing owner",
			appName: "testapp",
			metadata: &AppMetadata{
				Owner: "",
				Repo:  "repo",
				Tag:   "v1.0.0",
			},
			expectError: true,
		},
		{
			name:    "missing repo",
			appName: "testapp",
			metadata: &AppMetadata{
				Owner: "owner",
				Repo:  "",
				Tag:   "v1.0.0",
			},
			expectError: true,
		},
		{
			name:    "missing tag",
			appName: "testapp",
			metadata: &AppMetadata{
				Owner: "owner",
				Repo:  "repo",
				Tag:   "",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.appName, tt.metadata)
			if (err != nil) != tt.expectError {
				t.Errorf("Validate() error = %v, expectError %v", err, tt.expectError)
			}
		})
	}
}

func TestComputeConfigHash(t *testing.T) {
	t.Run("consistent hash", func(t *testing.T) {
		metadata := &AppMetadata{
			Owner: "owner",
			Repo:  "repo",
			Tag:   "v1.0.0",
		}

		hash1 := ComputeConfigHash(metadata)
		hash2 := ComputeConfigHash(metadata)

		if hash1 != hash2 {
			t.Errorf("hash should be consistent: %s != %s", hash1, hash2)
		}
	})

	t.Run("different metadata produces different hash", func(t *testing.T) {
		metadata1 := &AppMetadata{
			Owner: "owner1",
			Repo:  "repo",
			Tag:   "v1.0.0",
		}

		metadata2 := &AppMetadata{
			Owner: "owner2",
			Repo:  "repo",
			Tag:   "v1.0.0",
		}

		hash1 := ComputeConfigHash(metadata1)
		hash2 := ComputeConfigHash(metadata2)

		if hash1 == hash2 {
			t.Error("different metadata should produce different hashes")
		}
	})

	t.Run("hash is hex encoded", func(t *testing.T) {
		metadata := &AppMetadata{
			Owner: "owner",
			Repo:  "repo",
			Tag:   "v1.0.0",
		}

		hash := ComputeConfigHash(metadata)

		if len(hash) != 32 {
			t.Errorf("XXH3-128 hex should be 32 characters, got %d", len(hash))
		}

		for _, c := range hash {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Errorf("hash contains invalid hex character: %c", c)
			}
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr)))
}
