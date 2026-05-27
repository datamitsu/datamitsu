package cmd

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"errors"
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

func TestPrintAppInfo_Go(t *testing.T) {
	app := binmanager.App{
		Go: &binmanager.AppConfigGo{
			PackageName: "golang.org/x/vuln/cmd/govulncheck",
			Version:     "v1.1.4",
		},
	}

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	printAppInfo("govulncheck", app)

	_ = w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "Runtime:      go") {
		t.Errorf("missing runtime in output: %s", output)
	}
	if !strings.Contains(output, "Package:      golang.org/x/vuln/cmd/govulncheck") {
		t.Errorf("missing package name in output: %s", output)
	}
	if !strings.Contains(output, "Version:      v1.1.4") {
		t.Errorf("missing version in output: %s", output)
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
		"govulncheck": {
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
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
	if !strings.Contains(output, "go:") {
		t.Errorf("missing go group header in output: %s", output)
	}
	if !strings.Contains(output, "govulncheck") {
		t.Errorf("missing govulncheck in output: %s", output)
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

func TestReadLockFile_Go(t *testing.T) {
	tmpDir := t.TempDir()
	goMod := "module datamitsu-govulncheck\n\ngo 1.22\n\nrequire golang.org/x/vuln v1.1.4\n"
	goSum := "golang.org/x/vuln v1.1.4 h1:abc=\ngolang.org/x/vuln v1.1.4/go.mod h1:def=\n"

	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "go.sum"), []byte(goSum), 0644); err != nil {
		t.Fatal(err)
	}

	app := binmanager.App{
		Go: &binmanager.AppConfigGo{
			PackageName: "golang.org/x/vuln/cmd/govulncheck",
			Version:     "v1.1.4",
		},
	}

	content, err := readLockFile(tmpDir, app)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// readLockFile must assemble the JSON wrapper from go.mod + go.sum.
	want, err := runtimemanager.BuildGoLockFileJSON(goMod, goSum)
	if err != nil {
		t.Fatalf("BuildGoLockFileJSON() error = %v", err)
	}
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

func TestReadLockFile_GoMissingGoMod(t *testing.T) {
	tmpDir := t.TempDir()
	// Only go.sum present; go.mod is missing.
	if err := os.WriteFile(filepath.Join(tmpDir, "go.sum"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	app := binmanager.App{Go: &binmanager.AppConfigGo{PackageName: "golang.org/x/vuln/cmd/govulncheck"}}

	_, err := readLockFile(tmpDir, app)
	if err == nil {
		t.Fatal("expected error when go.mod is missing")
	}
	if !strings.Contains(err.Error(), "go.mod") {
		t.Errorf("error should mention go.mod, got: %v", err)
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

func TestGenerateGoLockContent_RemovesTempWorkDirOnSuccess(t *testing.T) {
	app := binmanager.App{
		Go: &binmanager.AppConfigGo{
			PackageName: "golang.org/x/vuln/cmd/govulncheck",
			Version:     "v1.1.4",
		},
	}

	goMod := "module datamitsu-govulncheck\n\ngo 1.22\n\nrequire golang.org/x/vuln v1.1.4\n"
	goSum := "golang.org/x/vuln v1.1.4 h1:abc=\ngolang.org/x/vuln v1.1.4/go.mod h1:def=\n"

	var capturedWorkDir string
	content, err := generateGoLockContent("govulncheck", app, func(workDir string) error {
		capturedWorkDir = workDir
		if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte(goMod), 0644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(workDir, "go.sum"), []byte(goSum), 0644)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The content must be the JSON wrapper assembled from the generated files.
	want, err := runtimemanager.BuildGoLockFileJSON(goMod, goSum)
	if err != nil {
		t.Fatalf("BuildGoLockFileJSON() error = %v", err)
	}
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}

	if capturedWorkDir == "" {
		t.Fatal("generate callback was not invoked with a workDir")
	}
	if _, err := os.Stat(capturedWorkDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp workDir %q should have been removed, stat err = %v", capturedWorkDir, err)
	}
}

func TestGenerateGoLockContent_RemovesTempWorkDirOnError(t *testing.T) {
	app := binmanager.App{
		Go: &binmanager.AppConfigGo{PackageName: "golang.org/x/vuln/cmd/govulncheck", Version: "v1.1.4"},
	}

	var capturedWorkDir string
	sentinel := errors.New("generation failed")
	_, err := generateGoLockContent("govulncheck", app, func(workDir string) error {
		capturedWorkDir = workDir
		return sentinel
	})
	if err == nil {
		t.Fatal("expected error from generate callback")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected wrapped sentinel error, got: %v", err)
	}
	if capturedWorkDir == "" {
		t.Fatal("generate callback was not invoked with a workDir")
	}
	// Cleanup must still run on the failure path so a failed generation does not
	// leak the multi-hundred-MiB module cache.
	if _, statErr := os.Stat(capturedWorkDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("temp workDir %q should have been removed on error, stat err = %v", capturedWorkDir, statErr)
	}
}

func TestClearAppLockFile_FNMClearsLockFilePreservesFiles(t *testing.T) {
	originalWorkspace := "allowBuilds:\n  puppeteer: true\n"
	apps := binmanager.MapOfApps{
		"mmdc": {
			Fnm: &binmanager.AppConfigFNM{
				PackageName: "@mermaid-js/mermaid-cli",
				Version:     "11.4.2",
				BinPath:     "node_modules/.bin/mmdc",
				LockFile:    "lockfileVersion: '9.0'\n",
			},
			Files: map[string]string{
				"pnpm-workspace.yaml": originalWorkspace,
				".npmrc":              "registry=https://registry.npmjs.org/\n",
			},
		},
	}

	fresh := clearAppLockFile(apps, "mmdc")

	if fresh["mmdc"].Fnm.LockFile != "" {
		t.Errorf("LockFile = %q, want empty after clearing", fresh["mmdc"].Fnm.LockFile)
	}
	if got := fresh["mmdc"].Files["pnpm-workspace.yaml"]; got != originalWorkspace {
		t.Errorf("Files[pnpm-workspace.yaml] lost or mutated: got %q, want %q", got, originalWorkspace)
	}
	if fresh["mmdc"].Files[".npmrc"] == "" {
		t.Error("Files[.npmrc] should be preserved")
	}
	if apps["mmdc"].Fnm.LockFile == "" {
		t.Error("original apps map was mutated: LockFile should still be set on the source")
	}
}

func TestClearAppLockFile_UVClearsLockFilePreservesFiles(t *testing.T) {
	apps := binmanager.MapOfApps{
		"yamllint": {
			Uv: &binmanager.AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.38.0",
				LockFile:    "version = 1\n",
			},
			Files: map[string]string{
				"pyproject.toml": "[project]\nname = \"yamllint-wrapper\"\n",
			},
		},
	}

	fresh := clearAppLockFile(apps, "yamllint")

	if fresh["yamllint"].Uv.LockFile != "" {
		t.Errorf("UV LockFile = %q, want empty after clearing", fresh["yamllint"].Uv.LockFile)
	}
	if fresh["yamllint"].Files["pyproject.toml"] == "" {
		t.Error("Files[pyproject.toml] should be preserved")
	}
	if apps["yamllint"].Uv.LockFile == "" {
		t.Error("original UV LockFile was mutated; clearAppLockFile must be non-destructive")
	}
}

func TestClearAppLockFile_GoClearsLockFilePreservesFields(t *testing.T) {
	apps := binmanager.MapOfApps{
		"govulncheck": {
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
				Runtime:     "go",
				LockFile:    "br:compressed-lock-file",
			},
		},
	}

	fresh := clearAppLockFile(apps, "govulncheck")

	if fresh["govulncheck"].Go.LockFile != "" {
		t.Errorf("Go LockFile = %q, want empty after clearing", fresh["govulncheck"].Go.LockFile)
	}
	// packageName/version/runtime must survive: generation needs them.
	if fresh["govulncheck"].Go.PackageName != "golang.org/x/vuln/cmd/govulncheck" {
		t.Errorf("Go PackageName lost: %q", fresh["govulncheck"].Go.PackageName)
	}
	if fresh["govulncheck"].Go.Version != "v1.1.4" {
		t.Errorf("Go Version lost: %q", fresh["govulncheck"].Go.Version)
	}
	if fresh["govulncheck"].Go.Runtime != "go" {
		t.Errorf("Go Runtime lost: %q", fresh["govulncheck"].Go.Runtime)
	}
	if apps["govulncheck"].Go.LockFile == "" {
		t.Error("original Go LockFile was mutated; clearAppLockFile must be non-destructive")
	}
}

func TestClearAppLockFile_OtherAppsUntouched(t *testing.T) {
	apps := binmanager.MapOfApps{
		"mmdc": {
			Fnm: &binmanager.AppConfigFNM{
				PackageName: "@mermaid-js/mermaid-cli",
				Version:     "11.4.2",
				LockFile:    "lockfile-1",
			},
		},
		"eslint": {
			Fnm: &binmanager.AppConfigFNM{
				PackageName: "eslint",
				Version:     "9.0.0",
				LockFile:    "lockfile-2",
			},
		},
	}

	fresh := clearAppLockFile(apps, "mmdc")

	if fresh["eslint"].Fnm.LockFile != "lockfile-2" {
		t.Errorf("eslint.LockFile = %q, want %q (untouched)", fresh["eslint"].Fnm.LockFile, "lockfile-2")
	}
	if fresh["mmdc"].Fnm.LockFile != "" {
		t.Errorf("mmdc.LockFile should be cleared, got %q", fresh["mmdc"].Fnm.LockFile)
	}
}

func TestClearAppLockFile_MissingAppReturnsCopy(t *testing.T) {
	apps := binmanager.MapOfApps{
		"mmdc": {
			Fnm: &binmanager.AppConfigFNM{PackageName: "@mermaid-js/mermaid-cli", LockFile: "x"},
		},
	}

	fresh := clearAppLockFile(apps, "nonexistent")

	if len(fresh) != len(apps) {
		t.Errorf("fresh len = %d, want %d", len(fresh), len(apps))
	}
	if fresh["mmdc"].Fnm.LockFile != "x" {
		t.Errorf("unrelated app.LockFile = %q, want %q", fresh["mmdc"].Fnm.LockFile, "x")
	}
}

