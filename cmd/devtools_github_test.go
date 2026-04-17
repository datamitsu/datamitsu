package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPullGitHubCommand_RequiresExactlyOneArg(t *testing.T) {
	if pullGithubCmd.Args == nil {
		t.Fatal("expected Args validator to be set (cobra.ExactArgs(1))")
	}
	err := pullGithubCmd.Args(pullGithubCmd, []string{})
	if err == nil {
		t.Fatal("expected error when no file argument provided")
	}
	err = pullGithubCmd.Args(pullGithubCmd, []string{"file.json"})
	if err != nil {
		t.Fatalf("expected no error with one argument, got: %v", err)
	}
}

func TestPullGitHubCommand_FileDoesNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	if err := ensureGitHubAppsJSONExists(path); err != nil {
		t.Fatalf("ensureGitHubAppsJSONExists failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read created file: %v", err)
	}

	// Should be a valid appstate structure
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("created file is not valid JSON: %v", err)
	}

	// Should have apps and binaries keys
	if _, ok := parsed["apps"]; !ok {
		t.Error("expected 'apps' key in created file")
	}
	if _, ok := parsed["binaries"]; !ok {
		t.Error("expected 'binaries' key in created file")
	}
}

func TestPullGitHubCommand_FileAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.json")

	original := `{"apps":{"test":{"owner":"foo","repo":"bar","tag":"v1.0"}},"binaries":{}}` + "\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ensureGitHubAppsJSONExists(path); err != nil {
		t.Fatalf("ensureGitHubAppsJSONExists failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	// Should not modify existing file
	if string(data) != original {
		t.Errorf("expected file to remain unchanged, got %q", string(data))
	}
}

