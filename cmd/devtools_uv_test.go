package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadUVAppsJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uvApps.json")

	content := `{
  "pycowsay": {
    "packageName": "pycowsay",
    "version": "0.0.0.2"
  },
  "yamllint": {
    "packageName": "yamllint",
    "version": "1.38.0"
  }
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	apps, err := readUVAppsJSON(path)
	if err != nil {
		t.Fatalf("readUVAppsJSON failed: %v", err)
	}

	if len(apps) != 2 {
		t.Fatalf("expected 2 apps, got %d", len(apps))
	}

	if apps["pycowsay"].PackageName != "pycowsay" {
		t.Errorf("expected packageName 'pycowsay', got %q", apps["pycowsay"].PackageName)
	}
	if apps["pycowsay"].Version != "0.0.0.2" {
		t.Errorf("expected version '0.0.0.2', got %q", apps["pycowsay"].Version)
	}
	if apps["yamllint"].Version != "1.38.0" {
		t.Errorf("expected version '1.38.0', got %q", apps["yamllint"].Version)
	}
}

func TestReadUVAppsJSON_FileNotFound(t *testing.T) {
	_, err := readUVAppsJSON("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestReadUVAppsJSON_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := readUVAppsJSON(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWriteUVAppsJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uvApps.json")

	apps := uvAppsJSON{
		"pycowsay": {PackageName: "pycowsay", Version: "1.0.0"},
		"yamllint": {PackageName: "yamllint", Version: "2.0.0"},
	}

	if err := writeUVAppsJSON(path, apps); err != nil {
		t.Fatalf("writeUVAppsJSON failed: %v", err)
	}

	readBack, err := readUVAppsJSON(path)
	if err != nil {
		t.Fatalf("readUVAppsJSON failed: %v", err)
	}

	if readBack["pycowsay"].Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", readBack["pycowsay"].Version)
	}
	if readBack["yamllint"].Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", readBack["yamllint"].Version)
	}
}

func TestUpdateUVAppsJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uvApps.json")

	initial := uvAppsJSON{
		"pycowsay": {PackageName: "pycowsay", Version: "0.0.0.2"},
		"yamllint": {PackageName: "yamllint", Version: "1.38.0"},
	}
	if err := writeUVAppsJSON(path, initial); err != nil {
		t.Fatal(err)
	}

	results := []pypiVersionResult{
		{Name: "pycowsay", PackageName: "pycowsay", CurrentVersion: "0.0.0.2", LatestVersion: "0.0.0.2", UpdateNeeded: false},
		{Name: "yamllint", PackageName: "yamllint", CurrentVersion: "1.38.0", LatestVersion: "1.39.0", UpdateNeeded: true},
	}

	if err := updateUVAppsJSON(path, results); err != nil {
		t.Fatalf("updateUVAppsJSON failed: %v", err)
	}

	updated, err := readUVAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}

	if updated["pycowsay"].Version != "0.0.0.2" {
		t.Errorf("expected pycowsay version '0.0.0.2' (unchanged), got %q", updated["pycowsay"].Version)
	}
	if updated["yamllint"].Version != "1.39.0" {
		t.Errorf("expected yamllint version '1.39.0', got %q", updated["yamllint"].Version)
	}
}

func TestUpdateUVAppsJSON_NoUpdates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uvApps.json")

	initial := uvAppsJSON{
		"yamllint": {PackageName: "yamllint", Version: "1.38.0"},
	}
	if err := writeUVAppsJSON(path, initial); err != nil {
		t.Fatal(err)
	}

	results := []pypiVersionResult{
		{Name: "yamllint", PackageName: "yamllint", CurrentVersion: "1.38.0", LatestVersion: "1.38.0", UpdateNeeded: false},
	}

	if err := updateUVAppsJSON(path, results); err != nil {
		t.Fatalf("updateUVAppsJSON failed: %v", err)
	}

	updated, err := readUVAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}

	if updated["yamllint"].Version != "1.38.0" {
		t.Errorf("expected version unchanged '1.38.0', got %q", updated["yamllint"].Version)
	}
}

func TestPullUVCommand_RequiresExactlyOneArg(t *testing.T) {
	if pullUVCmd.Args == nil {
		t.Fatal("expected Args validator to be set (cobra.ExactArgs(1))")
	}
	err := pullUVCmd.Args(pullUVCmd, []string{})
	if err == nil {
		t.Fatal("expected error when no file argument provided")
	}
	err = pullUVCmd.Args(pullUVCmd, []string{"file.json"})
	if err != nil {
		t.Fatalf("expected no error with one argument, got: %v", err)
	}
}

func TestPullUVCommand_FileDoesNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	if err := ensureUVAppsJSONExists(path); err != nil {
		t.Fatalf("ensureUVAppsJSONExists failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read created file: %v", err)
	}
	if string(data) != "{}\n" {
		t.Errorf("expected empty JSON object, got %q", string(data))
	}
}

func TestPullUVCommand_AlwaysFetchDescriptions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uvApps.json")

	if err := ensureUVAppsJSONExists(path); err != nil {
		t.Fatalf("ensureUVAppsJSONExists failed: %v", err)
	}

	results := []pypiVersionResult{
		{
			Name:           "yamllint",
			PackageName:    "yamllint",
			CurrentVersion: "1.38.0",
			LatestVersion:  "1.39.0",
			UpdateNeeded:   true,
			Description:    "A linter for YAML files",
		},
	}

	if err := updateUVAppsJSON(path, results); err != nil {
		t.Fatalf("updateUVAppsJSON failed: %v", err)
	}

	apps, err := readUVAppsJSON(path)
	if err != nil {
		t.Fatalf("readUVAppsJSON failed: %v", err)
	}

	if apps["yamllint"].Description != "A linter for YAML files" {
		t.Errorf("expected description 'A linter for YAML files', got %q", apps["yamllint"].Description)
	}
}

func TestUpdateUVAppsJSON_WithErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uvApps.json")

	initial := uvAppsJSON{
		"yamllint": {PackageName: "yamllint", Version: "1.38.0"},
	}
	if err := writeUVAppsJSON(path, initial); err != nil {
		t.Fatal(err)
	}

	results := []pypiVersionResult{
		{Name: "yamllint", PackageName: "yamllint", CurrentVersion: "1.38.0", LatestVersion: "1.39.0", UpdateNeeded: true, Error: "network error"},
	}

	if err := updateUVAppsJSON(path, results); err != nil {
		t.Fatalf("updateUVAppsJSON failed: %v", err)
	}

	updated, err := readUVAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}

	// Should not update when there's an error
	if updated["yamllint"].Version != "1.38.0" {
		t.Errorf("expected version unchanged '1.38.0' (error skipped), got %q", updated["yamllint"].Version)
	}
}
