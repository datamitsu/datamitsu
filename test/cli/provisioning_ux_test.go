package cli_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/digest"
)

func TestCheckProvisionsLintAfterFixAndKeepsWarmRunQuiet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture commands use a Unix shell")
	}
	p := clitest.NewProject(t)
	payload := []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> .invocations\n")
	var requests atomic.Int32
	var lateLint atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/lint" {
			if _, err := os.Stat(filepath.Join(p.Dir, ".invocations")); err == nil {
				lateLint.Store(true)
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	p.WriteFile(".gitignore", ".invocations\n")
	p.WriteFile("sample.txt", "sample\n")
	p.WriteFile("datamitsu.config.js", fmt.Sprintf(`
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({
  version: "fixture",
  apps: {
    "fix-app": { binary: { binaries: { %q: { %q: { unknown: { url: %q, hash: %q, contentType: "binary" } } } } } },
    "lint-app": { binary: { binaries: { %q: { %q: { unknown: { url: %q, hash: %q, contentType: "binary" } } } } } }
  },
  runtimes: {},
  projectTypes: { sample: { markers: ["sample.txt"] } },
  tools: {
    formatter: { projectTypes: ["sample"], operations: { fix: { app: "fix-app", args: ["fix", "{files}"], globs: ["*.txt"] } } },
    analyzer: { projectTypes: ["sample"], operations: { lint: { app: "lint-app", args: ["lint", "{files}"], globs: ["*.txt"] } } }
  }
});`, runtime.GOOS, runtime.GOARCH, server.URL+"/fix", digest.SHA256Of(payload).String(), runtime.GOOS, runtime.GOARCH, server.URL+"/lint", digest.SHA256Of(payload).String()))
	cache := t.TempDir()
	opts := clitest.RunOptions{
		Stdin:   "",
		Timeout: 0,
		Dir:     p.Dir, CacheDir: cache, Env: []string{"DATAMITSU_OFFLINE="},
	}
	cold := clitest.Run(t, opts, "check")
	if cold.ExitCode != 0 {
		t.Fatalf("cold check: exit %d\n%s\n%s", cold.ExitCode, cold.Stdout, cold.Stderr)
	}
	if !lateLint.Load() || requests.Load() != 2 {
		t.Fatalf("check did not defer lint provisioning until after the fix: late lint=%v, requests=%d", lateLint.Load(), requests.Load())
	}
	invocations, err := os.ReadFile(filepath.Join(p.Dir, ".invocations"))
	if err != nil || string(invocations) != "fix\nlint\n" {
		t.Fatalf("operation execution: %q, %v", invocations, err)
	}
	warm := clitest.Run(t, opts, "check")
	if warm.ExitCode != 0 {
		t.Fatalf("warm check: exit %d\n%s\n%s", warm.ExitCode, warm.Stdout, warm.Stderr)
	}
	if requests.Load() != 2 || strings.Contains(warm.Stdout+warm.Stderr, "Downloading") || strings.Contains(warm.Stdout+warm.Stderr, "Installing") {
		t.Fatalf("warm check repeated provisioning:\n%s\n%s", warm.Stdout, warm.Stderr)
	}
	for _, mode := range []string{"jsonl", "agent"} {
		t.Run(mode, func(t *testing.T) {
			if err := os.Remove(filepath.Join(p.Dir, ".invocations")); err != nil {
				t.Fatal(err)
			}
			lateLint.Store(false)
			wantRequests := requests.Load() + 2
			machineOpts := opts
			machineOpts.CacheDir = t.TempDir()
			args := []string{"check", "--output", "agent"}
			if mode == "jsonl" {
				args = []string{"--log-format", "jsonl", "check"}
			}
			result := clitest.Run(t, machineOpts, args...)
			if result.ExitCode != 0 || !lateLint.Load() || requests.Load() != wantRequests {
				t.Fatalf("cold %s check: exit %d, late lint=%v\n%s\n%s", mode, result.ExitCode, lateLint.Load(), result.Stdout, result.Stderr)
			}
			if strings.Contains(result.Stdout+result.Stderr, "\x1b[") || strings.Contains(result.Stdout+result.Stderr, "Downloading") || strings.Contains(result.Stdout+result.Stderr, "Installing") {
				t.Fatalf("human provisioning output contaminated %s output:\n%s\n%s", mode, result.Stdout, result.Stderr)
			}
			if mode == "jsonl" {
				events := clitest.MustParseJSONL(t, result.Stderr)
				clitest.AssertChains(t, events)
				if result.Stdout != "" {
					t.Fatalf("JSON-L check wrote human stdout: %q", result.Stdout)
				}
			}
		})
	}
}
