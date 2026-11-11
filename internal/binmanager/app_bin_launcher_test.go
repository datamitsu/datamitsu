package binmanager

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/launch"
)

func TestNodeDependencyBindingExecutesNativeLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture command uses a POSIX shell")
	}
	root := filepath.Join(t.TempDir(), "cache root with spaces")
	t.Setenv("DATAMITSU_CACHE_DIR", root)
	target := filepath.Join(t.TempDir(), "node app")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\nprintf 'runtime=%s\\n' \"$FRONTEND_RUNTIME\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixedArgs := []string{"entrypoint.mjs", "--no-install"}
	runtimeEnv := map[string]string{"FRONTEND_RUNTIME": "managed-node"}
	apps := MapOfApps{
		"root":     {Shell: &AppConfigShell{Name: "true"}, DependsOn: edges("frontend"), RuntimeEnv: map[string]string{"FRONTEND_BIN": "${APP_BIN:frontend}"}},
		"frontend": {Node: &AppConfigNode{PackageName: "frontend", Version: "1", BinPath: "entrypoint.mjs"}},
	}
	manager := &mockRuntimeAppManager{
		resolveCommandInfoFunc: func(string, App) (*CommandInfo, error) {
			return &CommandInfo{Command: target, Args: fixedArgs, Env: runtimeEnv}, nil
		},
		getCommandInfoFunc: func(context.Context, string, App) (*CommandInfo, error) {
			return &CommandInfo{Command: target, Args: fixedArgs, Env: runtimeEnv}, nil
		},
	}
	bm := New(apps, nil, manager)
	resolved, _, err := bm.ResolveCommandInfo("root")
	if err != nil {
		t.Fatalf("read-only ResolveCommandInfo(root): %v", err)
	}
	launcher := resolved.Env["FRONTEND_BIN"]
	if _, err := os.Stat(launcher); !os.IsNotExist(err) {
		t.Fatalf("read-only resolution launcher stat error = %v, want not-exist", err)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(launcher))); !os.IsNotExist(err) {
		t.Fatalf("read-only resolution created launcher directory: %v", err)
	}
	if _, err := bm.GetCommandInfo(context.Background(), "frontend"); err != nil {
		t.Fatalf("GetCommandInfo(frontend): %v", err)
	}
	info, _, err := bm.ResolveCommandInfo("root")
	if err != nil {
		t.Fatalf("ResolveCommandInfo(root): %v", err)
	}
	launcher = info.Env["FRONTEND_BIN"]
	if !filepath.IsAbs(launcher) || !strings.Contains(launcher, ".app-launchers") || !strings.Contains(launcher, "cache root with spaces") {
		t.Fatalf("FRONTEND_BIN = %q, want absolute managed launcher under spaced cache root", launcher)
	}
	cmd := exec.Command(launcher, "identity", "--schema", "x.json")
	cmd.Env = []string{"DATAMITSU_CACHE_DIR=" + root, "PATH="}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run frontend launcher without ambient PATH: %v: %s", err, output)
	}
	want := "entrypoint.mjs\n--no-install\nidentity\n--schema\nx.json\nruntime=managed-node\n"
	if string(output) != want {
		t.Fatalf("frontend output = %q, want %q", output, want)
	}
}

func TestNodeDependencyBindingPreservesExitAndSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture command uses POSIX process signals")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(t.TempDir(), "cache root"))
	for _, tc := range []struct {
		name string
		body string
		code int
		sig  string
	}{
		{name: "exit", body: "exit 23\n", code: 23},
		{name: "signal", body: "kill -TERM $$\n", code: -1, sig: "signal: terminated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, []byte("#!/bin/sh\n"+tc.body), 0o755); err != nil {
				t.Fatal(err)
			}
			launcher, err := launch.Ensure(target, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(launcher)
			cmd.Env = []string{"DATAMITSU_CACHE_DIR=" + os.Getenv("DATAMITSU_CACHE_DIR"), "PATH="}
			err = cmd.Run()
			if tc.sig != "" {
				if err == nil || !strings.Contains(err.Error(), tc.sig) {
					t.Fatalf("launcher error = %v, want %q", err, tc.sig)
				}
				return
			}
			exitErr := &exec.ExitError{}
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.code {
				t.Fatalf("launcher error = %v, want exit %d", err, tc.code)
			}
		})
	}
}

func TestNativeLauncherPreservesStreams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture command uses a POSIX shell")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	target := filepath.Join(t.TempDir(), "streams")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nread value\nprintf 'out=%s\\n' \"$value\"\nprintf 'err=%s\\n' \"$value\" >&2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher, err := launch.Ensure(target, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(launcher)
	cmd.Env = []string{"DATAMITSU_CACHE_DIR=" + os.Getenv("DATAMITSU_CACHE_DIR"), "PATH="}
	cmd.Stdin = strings.NewReader("payload\n")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("run launcher: %v", err)
	}
	if string(output) != "out=payload\n" || stderr.String() != "err=payload\n" {
		t.Fatalf("stdout/stderr = %q / %q", output, stderr.String())
	}
}
