package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
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

func TestPrintAppInfo_Node(t *testing.T) {
	app := binmanager.App{
		Required:           false,
		Lazy:               false,
		Description:        "",
		VersionCheck:       nil,
		DependsOn:          nil,
		OfficialURL:        "",
		OfficialURLDerived: false,
		Binary:             nil,
		Bun:                nil,
		Uv:                 nil,
		Jvm:                nil,
		Go:                 nil,
		Shell:              nil,
		Env:                nil,
		RuntimeEnv:         nil,
		Caches:             nil,
		Files:              nil,
		Links:              nil,
		Archives:           nil,
		Prepare:            nil,
		Node: &binmanager.AppConfigNode{
			Runtime:     "",
			LockFile:    "",
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
	if !strings.Contains(output, "Runtime:      node") {
		t.Errorf("missing runtime in output: %s", output)
	}
	if !strings.Contains(output, "Version:      11.4.2") {
		t.Errorf("missing version in output: %s", output)
	}
	if !strings.Contains(output, "puppeteer: 23.11.1") {
		t.Errorf("missing dependencies in output: %s", output)
	}
}

func TestPrintAppInfo_Bun(t *testing.T) {
	app := binmanager.App{
		Required:           false,
		Lazy:               false,
		Description:        "",
		VersionCheck:       nil,
		DependsOn:          nil,
		OfficialURL:        "",
		OfficialURLDerived: false,
		Binary:             nil,
		Uv:                 nil,
		Node:               nil,
		Jvm:                nil,
		Go:                 nil,
		Shell:              nil,
		Env:                nil,
		RuntimeEnv:         nil,
		Caches:             nil,
		Files:              nil,
		Links:              nil,
		Archives:           nil,
		Prepare:            nil,
		Bun: &binmanager.AppConfigBun{
			Runtime:     "",
			LockFile:    "",
			PackageName: "eslint",
			Version:     "10.9.0",
			BinPath:     "node_modules/eslint/bin/eslint.js",
			Dependencies: map[string]string{
				"typescript": "6.0.2",
			},
		},
	}

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	printAppInfo("eslint", app)

	_ = w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "Runtime:      bun") {
		t.Errorf("missing runtime in output: %s", output)
	}
	if !strings.Contains(output, "Package:      eslint") {
		t.Errorf("missing package name in output: %s", output)
	}
	if !strings.Contains(output, "typescript: 6.0.2") {
		t.Errorf("missing dependencies in output: %s", output)
	}
}

func TestPrintAppInfo_UV(t *testing.T) {
	app := binmanager.App{
		Required:           false,
		Lazy:               false,
		Description:        "",
		VersionCheck:       nil,
		DependsOn:          nil,
		OfficialURL:        "",
		OfficialURLDerived: false,
		Binary:             nil,
		Bun:                nil,
		Node:               nil,
		Jvm:                nil,
		Go:                 nil,
		Shell:              nil,
		Env:                nil,
		RuntimeEnv:         nil,
		Caches:             nil,
		Files:              nil,
		Links:              nil,
		Archives:           nil,
		Prepare:            nil,
		Uv: &binmanager.AppConfigUV{
			Runtime:        "",
			LockFile:       "",
			RequiresPython: "",
			PackageName:    "yamllint",
			Version:        "1.38.0",
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
		Required:           false,
		Lazy:               false,
		Description:        "",
		VersionCheck:       nil,
		DependsOn:          nil,
		OfficialURL:        "",
		OfficialURLDerived: false,
		Binary:             nil,
		Bun:                nil,
		Uv:                 nil,
		Node:               nil,
		Jvm:                nil,
		Shell:              nil,
		Env:                nil,
		RuntimeEnv:         nil,
		Caches:             nil,
		Files:              nil,
		Links:              nil,
		Archives:           nil,
		Prepare:            nil,
		Go: &binmanager.AppConfigGo{
			Runtime:     "",
			LockFile:    "",
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
		Required:           false,
		Lazy:               false,
		Description:        "",
		VersionCheck:       nil,
		DependsOn:          nil,
		OfficialURL:        "",
		OfficialURLDerived: false,
		Bun:                nil,
		Uv:                 nil,
		Node:               nil,
		Jvm:                nil,
		Go:                 nil,
		Shell:              nil,
		Env:                nil,
		RuntimeEnv:         nil,
		Caches:             nil,
		Files:              nil,
		Links:              nil,
		Archives:           nil,
		Prepare:            nil,
		Binary:             &binmanager.AppConfigBinary{Binaries: nil, Version: ""},
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
		Required:           false,
		Lazy:               false,
		Description:        "",
		VersionCheck:       nil,
		DependsOn:          nil,
		OfficialURL:        "",
		OfficialURLDerived: false,
		Binary:             nil,
		Bun:                nil,
		Uv:                 nil,
		Node:               nil,
		Jvm:                nil,
		Go:                 nil,
		Env:                nil,
		RuntimeEnv:         nil,
		Caches:             nil,
		Files:              nil,
		Links:              nil,
		Archives:           nil,
		Prepare:            nil,
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
		"biome": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Binary:             nil,
			Uv:                 nil,
			Node:               nil,
			Jvm:                nil,
			Go:                 nil,
			Shell:              nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Bun: &binmanager.AppConfigBun{
				BinPath:      "",
				Runtime:      "",
				LockFile:     "",
				Dependencies: nil,
				PackageName:  "@biomejs/biome",
				Version:      "2.2.4",
			},
		},
		"mermaid": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Binary:             nil,
			Bun:                nil,
			Uv:                 nil,
			Jvm:                nil,
			Go:                 nil,
			Shell:              nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Node: &binmanager.AppConfigNode{
				BinPath:      "",
				Runtime:      "",
				LockFile:     "",
				Dependencies: nil,
				PackageName:  "@mermaid-js/mermaid-cli",
				Version:      "11.4.2",
			},
		},
		"eslint": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Binary:             nil,
			Bun:                nil,
			Uv:                 nil,
			Jvm:                nil,
			Go:                 nil,
			Shell:              nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Node: &binmanager.AppConfigNode{
				BinPath:      "",
				Runtime:      "",
				LockFile:     "",
				Dependencies: nil,
				PackageName:  "eslint",
				Version:      "9.0.0",
			},
		},
		"yamllint": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Binary:             nil,
			Bun:                nil,
			Node:               nil,
			Jvm:                nil,
			Go:                 nil,
			Shell:              nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Uv: &binmanager.AppConfigUV{
				Runtime:        "",
				LockFile:       "",
				RequiresPython: "",
				PackageName:    "yamllint",
				Version:        "1.38.0",
			},
		},
		"govulncheck": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Binary:             nil,
			Bun:                nil,
			Uv:                 nil,
			Node:               nil,
			Jvm:                nil,
			Shell:              nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Go: &binmanager.AppConfigGo{
				Runtime:     "",
				LockFile:    "",
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
			},
		},
		"golangci-lint": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Bun:                nil,
			Uv:                 nil,
			Node:               nil,
			Jvm:                nil,
			Go:                 nil,
			Shell:              nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Binary:             &binmanager.AppConfigBinary{Binaries: nil, Version: ""},
		},
		"govet": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Binary:             nil,
			Bun:                nil,
			Uv:                 nil,
			Node:               nil,
			Jvm:                nil,
			Go:                 nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Shell:              &binmanager.AppConfigShell{Args: nil, Name: "go"},
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

	if !strings.Contains(output, "bun:") {
		t.Errorf("missing bun group header in output: %s", output)
	}
	if !strings.Contains(output, "node:") {
		t.Errorf("missing node group header in output: %s", output)
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
	if !strings.Contains(output, "biome") {
		t.Errorf("missing biome in output: %s", output)
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

	// Verify sorted order: eslint before mermaid in node section
	eslintIdx := strings.Index(output, "eslint")
	mermaidIdx := strings.Index(output, "mermaid")
	if eslintIdx > mermaidIdx {
		t.Errorf("node apps should be sorted alphabetically, eslint at %d, mermaid at %d", eslintIdx, mermaidIdx)
	}
}

func TestListLockfileApps_Empty(t *testing.T) {
	apps := binmanager.MapOfApps{
		"govet": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Binary:             nil,
			Bun:                nil,
			Uv:                 nil,
			Node:               nil,
			Jvm:                nil,
			Go:                 nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Shell:              &binmanager.AppConfigShell{Args: nil, Name: "go"},
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
