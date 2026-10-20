package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadNodeAppsJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nodeApps.json")

	content := `{
  "cspell": {
    "packageName": "cspell",
    "version": "9.7.0"
  },
  "mmdc": {
    "packageName": "@mermaid-js/mermaid-cli",
    "version": "11.12.0"
  }
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	apps, err := readNodeAppsJSON(path)
	if err != nil {
		t.Fatalf("readNodeAppsJSON failed: %v", err)
	}

	if len(apps) != 2 {
		t.Fatalf("expected 2 apps, got %d", len(apps))
	}

	if apps["cspell"].PackageName != "cspell" {
		t.Errorf("expected packageName 'cspell', got %q", apps["cspell"].PackageName)
	}
	if apps["cspell"].Version != "9.7.0" {
		t.Errorf("expected version '9.7.0', got %q", apps["cspell"].Version)
	}
	if apps["mmdc"].PackageName != "@mermaid-js/mermaid-cli" {
		t.Errorf("expected packageName '@mermaid-js/mermaid-cli', got %q", apps["mmdc"].PackageName)
	}
}

func TestReadNodeAppsJSON_FileNotFound(t *testing.T) {
	_, err := readNodeAppsJSON("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestReadNodeAppsJSON_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := readNodeAppsJSON(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWriteNodeAppsJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nodeApps.json")

	apps := nodeAppsJSON{
		"cspell": {PackageName: "cspell", Version: "9.8.0"},
		"mmdc":   {PackageName: "@mermaid-js/mermaid-cli", Version: "12.0.0"},
	}

	if err := writeNodeAppsJSON(path, apps); err != nil {
		t.Fatalf("writeNodeAppsJSON failed: %v", err)
	}

	readBack, err := readNodeAppsJSON(path)
	if err != nil {
		t.Fatalf("readNodeAppsJSON failed: %v", err)
	}

	if readBack["cspell"].Version != "9.8.0" {
		t.Errorf("expected version '9.8.0', got %q", readBack["cspell"].Version)
	}
	if readBack["mmdc"].Version != "12.0.0" {
		t.Errorf("expected version '12.0.0', got %q", readBack["mmdc"].Version)
	}
}

func TestPullNodeCommand_RequiresExactlyOneArg(t *testing.T) {
	if pullNodeCmd.Args == nil {
		t.Fatal("expected Args validator to be set (cobra.ExactArgs(1))")
	}
	err := pullNodeCmd.Args(pullNodeCmd, []string{})
	if err == nil {
		t.Fatal("expected error when no file argument provided")
	}
	err = pullNodeCmd.Args(pullNodeCmd, []string{"file.json"})
	if err != nil {
		t.Fatalf("expected no error with one argument, got: %v", err)
	}
}

func TestPullNodeCommand_FileDoesNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	if err := ensureNodeAppsJSONExists(path); err != nil {
		t.Fatalf("ensureNodeAppsJSONExists failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read created file: %v", err)
	}
	if string(data) != "{}\n" {
		t.Errorf("expected empty JSON object, got %q", string(data))
	}
}
