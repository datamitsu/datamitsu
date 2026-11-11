package cli_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/digest"
)

func TestPrepareDiagnosticsCannotInjectGitHubCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable is a POSIX shell script")
	}
	payload := []byte("#!/bin/sh\nprintf 'prepare failed\\n::error::PREPARE_INJECTED_ERROR\\r::warning::PREPARE_INJECTED_WARNING\\n' >&2\nexit 4\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	project := clitest.NewProject(t)
	project.WriteFile("fixture.marker", "")
	cfg := project.WriteFile("commands.config.js", fmt.Sprintf(`globalThis.getBeforeConfigs = () => [];
const binary = {url:%q,hash:%q,contentType:"binary"};
globalThis.getConfig = () => ({version:"fixture",projectTypes:{fixture:{markers:["fixture.marker"]}},apps:{fixture:{binary:{version:"1",binaries:{%q:{%q:{glibc:binary,musl:binary,unknown:binary}}}},prepare:{args:[],network:false}}},tools:{"lint-only":{name:"lint-only",operations:{lint:{app:"fixture",args:[],scope:"repository"}}}},runtimes:{},managedConfigs:{}});
globalThis.getMinVersion = () => "0.5.0";`, server.URL, digest.SHA256Of(payload).String(), runtime.GOOS, runtime.GOARCH))
	for _, scenario := range []struct {
		name string
		args []string
	}{
		{"check", []string{"check", "--tools", "lint-only"}},
		{"install", []string{"install", "fixture"}},
		{"quiet", []string{"install", "--quiet", "fixture"}},
		{"agent", []string{"check", "--tools", "lint-only", "--output=agent"}},
		{"jsonl", []string{"check", "--tools", "lint-only", "--log-format=jsonl"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			args := append([]string{"--no-auto-config", "--config", cfg}, scenario.args...)
			result := clitest.Run(t, clitest.RunOptions{Dir: project.Dir, CacheDir: t.TempDir(), Env: []string{"DATAMITSU_OFFLINE=", "GITHUB_ACTIONS=true", "CI=true"}}, args...)
			if result.ExitCode != 1 || !strings.Contains(result.Stderr, "PREPARE_INJECTED_ERROR") || !strings.Contains(result.Stderr, "PREPARE_INJECTED_WARNING") {
				t.Fatalf("prepare diagnostics missing: %+v", result)
			}
			for _, stream := range []string{result.Stdout, result.Stderr} {
				for _, line := range strings.FieldsFunc(stream, func(r rune) bool { return r == '\n' || r == '\r' }) {
					if strings.HasPrefix(strings.TrimLeft(line, " \t"), "::error::") || strings.HasPrefix(strings.TrimLeft(line, " \t"), "::warning::") {
						t.Fatalf("prepare emitted an executable GitHub workflow command: %q", line)
					}
				}
			}
			if scenario.name == "jsonl" {
				clitest.MustParseJSONL(t, result.Stderr)
			}
		})
	}
}
