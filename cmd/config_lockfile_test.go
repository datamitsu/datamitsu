package cmd

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigLockfileCommandRegistered(t *testing.T) {
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use != "config" {
			continue
		}
		for _, sub := range cmd.Commands() {
			if sub.Use == "lockfile [appName]" {
				return
			}
		}
		t.Error("lockfile subcommand not found under config command")
		return
	}
	t.Error("config command not registered with rootCmd")
}

func TestConfigLockfileAcceptsZeroOrOneArgs(t *testing.T) {
	cmd := configLockfileCmd
	if cmd.Args == nil {
		t.Fatal("expected Args validator to be set")
	}

	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("expected no error with zero args, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"myapp"}); err != nil {
		t.Errorf("expected no error with one arg, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("expected error with two args")
	}
}

func TestPrintAppInfo_FNM(t *testing.T) {
	app := binmanager.App{
		Fnm: &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",
			BinPath:     "node_modules/.bin/mmdc",
			Dependencies: map[string]string{
				"puppeteer": "23.11.1",
			},
		},
	}

	// Capture stderr output
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	printAppInfo("mermaid", app)

	_ = w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "App: mermaid") {
		t.Errorf("missing app name in output: %s", output)
	}
	if !strings.Contains(output, "Runtime:      fnm") {
		t.Errorf("missing runtime in output: %s", output)
	}
	if !strings.Contains(output, "Version:      11.4.2") {
		t.Errorf("missing version in output: %s", output)
	}
	if !strings.Contains(output, "puppeteer: 23.11.1") {
		t.Errorf("missing dependencies in output: %s", output)
	}
}

func TestPrintAppInfo_UV(t *testing.T) {
	app := binmanager.App{
		Uv: &binmanager.AppConfigUV{
			PackageName: "yamllint",
			Version:     "1.38.0",
		},
	}

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	printAppInfo("yamllint", app)

	_ = w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "Runtime:      uv") {
		t.Errorf("missing runtime in output: %s", output)
	}
	if !strings.Contains(output, "Package:      yamllint") {
		t.Errorf("missing package name in output: %s", output)
	}
}

func TestPrintAppInfo_Binary(t *testing.T) {
	app := binmanager.App{
		Binary: &binmanager.AppConfigBinary{},
	}

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	printAppInfo("golangci-lint", app)

	_ = w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "Runtime:      binary") {
		t.Errorf("missing runtime in output: %s", output)
	}
}

func TestPrintAppInfo_Shell(t *testing.T) {
	app := binmanager.App{
		Shell: &binmanager.AppConfigShell{
			Name: "go",
			Args: []string{"vet"},
		},
	}

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	printAppInfo("govet", app)

	_ = w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "Runtime:      shell") {
		t.Errorf("missing runtime in output: %s", output)
	}
	if !strings.Contains(output, "Command:      go") {
		t.Errorf("missing command in output: %s", output)
	}
}

func TestListLockfileApps(t *testing.T) {
	apps := binmanager.MapOfApps{
		"mermaid": {
			Fnm: &binmanager.AppConfigFNM{
				PackageName: "@mermaid-js/mermaid-cli",
				Version:     "11.4.2",
			},
		},
		"eslint": {
			Fnm: &binmanager.AppConfigFNM{
				PackageName: "eslint",
				Version:     "9.0.0",
			},
		},
		"yamllint": {
			Uv: &binmanager.AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.38.0",
			},
		},
		"golangci-lint": {
			Binary: &binmanager.AppConfigBinary{},
		},
		"govet": {
			Shell: &binmanager.AppConfigShell{Name: "go"},
		},
	}

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	listLockfileApps(apps)

	_ = w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "fnm:") {
		t.Errorf("missing fnm group header in output: %s", output)
	}
	if !strings.Contains(output, "uv:") {
		t.Errorf("missing uv group header in output: %s", output)
	}
	if !strings.Contains(output, "eslint") {
		t.Errorf("missing eslint in output: %s", output)
	}
	if !strings.Contains(output, "mermaid") {
		t.Errorf("missing mermaid in output: %s", output)
	}
	if !strings.Contains(output, "yamllint") {
		t.Errorf("missing yamllint in output: %s", output)
	}
	if strings.Contains(output, "golangci-lint") {
		t.Errorf("binary app should not be listed: %s", output)
	}
	if strings.Contains(output, "govet") {
		t.Errorf("shell app should not be listed: %s", output)
	}

	// Verify sorted order: eslint before mermaid in fnm section
	eslintIdx := strings.Index(output, "eslint")
	mermaidIdx := strings.Index(output, "mermaid")
	if eslintIdx > mermaidIdx {
		t.Errorf("fnm apps should be sorted alphabetically, eslint at %d, mermaid at %d", eslintIdx, mermaidIdx)
	}
}

func TestListLockfileApps_Empty(t *testing.T) {
	apps := binmanager.MapOfApps{
		"govet": {
			Shell: &binmanager.AppConfigShell{Name: "go"},
		},
	}

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	listLockfileApps(apps)

	_ = w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "No apps with lock file support found") {
		t.Errorf("expected empty message, got: %s", output)
	}
}

func TestReadLockFile_FNM(t *testing.T) {
	tmpDir := t.TempDir()
	lockContent := "lockfileVersion: '9.0'\n"

	if err := os.WriteFile(filepath.Join(tmpDir, "pnpm-lock.yaml"), []byte(lockContent), 0644); err != nil {
		t.Fatal(err)
	}

	app := binmanager.App{
		Fnm: &binmanager.AppConfigFNM{
			PackageName: "eslint",
			Version:     "9.0.0",
		},
	}

	content, err := readLockFile(tmpDir, app)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != lockContent {
		t.Errorf("content = %q, want %q", content, lockContent)
	}
}

func TestReadLockFile_UV(t *testing.T) {
	tmpDir := t.TempDir()
	lockContent := "version = 1\n"

	if err := os.WriteFile(filepath.Join(tmpDir, "uv.lock"), []byte(lockContent), 0644); err != nil {
		t.Fatal(err)
	}

	app := binmanager.App{
		Uv: &binmanager.AppConfigUV{
			PackageName: "yamllint",
			Version:     "1.38.0",
		},
	}

	content, err := readLockFile(tmpDir, app)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != lockContent {
		t.Errorf("content = %q, want %q", content, lockContent)
	}
}

func TestReadLockFile_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()

	app := binmanager.App{
		Fnm: &binmanager.AppConfigFNM{
			PackageName: "eslint",
		},
	}

	_, err := readLockFile(tmpDir, app)
	if err == nil {
		t.Fatal("expected error when lock file doesn't exist")
	}
	if !strings.Contains(err.Error(), "failed to read lock file") {
		t.Errorf("error should mention read failure, got: %v", err)
	}
}

func TestReadLockFile_UnsupportedType(t *testing.T) {
	app := binmanager.App{
		Binary: &binmanager.AppConfigBinary{},
	}

	_, err := readLockFile(t.TempDir(), app)
	if err == nil {
		t.Fatal("expected error for binary app")
	}
	if !strings.Contains(err.Error(), "unsupported app type") {
		t.Errorf("error should mention unsupported type, got: %v", err)
	}
}

