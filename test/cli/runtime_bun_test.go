package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/digest"
)

func TestRuntimeRunBunPreservesUserArgumentsAndStreams(t *testing.T) {
	probe := runtimeProbe(t)
	command, err := json.Marshal(probe)
	if err != nil {
		t.Fatal(err)
	}
	project := clitest.NewProject(t)
	cfg := project.WriteFile("bun.config.js", fmt.Sprintf(`globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({version:"fixture",apps:{},tools:{},managedConfigs:{},runtimes:{
  bun:{kind:"bun",mode:"system",system:{command:%s,systemVersion:"fixture"},bun:{bunVersion:"1.4.3",pnpmRuntime:"pnpm"}},
  pnpm:{kind:"pnpm",mode:"system",system:{command:"missing-pnpm-command"},pnpm:{pnpmVersion:"12"}}
}});
globalThis.getMinVersion = () => "0.0.0";`, command))
	record := filepath.Join(project.Dir, "bun-arguments.json")
	args := []string{"--record", record, "--exit", "37", "script with space", ""}
	result := clitest.Run(t, clitest.RunOptions{Dir: project.Dir, CacheDir: t.TempDir(), Stdin: "Bun stdin"}, runtimeArgs(project, cfg, append([]string{"run", "bun", "--"}, args...)...)...)
	if result.ExitCode != 37 || result.Stdout != "probe stdout\n" || result.Stderr != "probe stderr\n" {
		t.Fatalf("Bun handoff: %+v", result)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Args  []string `json:"args"`
		Cwd   string   `json:"cwd"`
		Stdin string   `json:"stdin"`
	}
	if err := json.Unmarshal(data, &captured); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(captured.Args, args) || captured.Cwd != project.Dir || captured.Stdin != "Bun stdin" {
		t.Fatalf("Bun changed the caller contract: %+v", captured)
	}
}

func TestRuntimeRunBunColdInstallDoesNotRequirePNPM(t *testing.T) {
	payload, err := os.ReadFile(runtimeProbe(t))
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	foreignOS := "windows"
	if runtime.GOOS == "windows" {
		foreignOS = "linux"
	}
	libcs := "{unknown:artifact}"
	if runtime.GOOS == "linux" {
		libcs = "{glibc:artifact,musl:artifact}"
	}
	project := clitest.NewProject(t)
	cfg := project.WriteFile("bun.config.js", fmt.Sprintf(`globalThis.getBeforeConfigs = () => [];
const artifact={url:%q,hash:%q,contentType:"binary",binaryPath:%q};
globalThis.getConfig = () => ({version:"fixture",apps:{},tools:{},managedConfigs:{},runtimes:{
  bun:{kind:"bun",mode:"managed",managed:{binaries:{%q:{%q:%s}}},bun:{bunVersion:"1.4.3",pnpmRuntime:"pnpm"}},
  pnpm:{kind:"pnpm",mode:"managed",managed:{binaries:{%q:{%q:{unknown:artifact}}}},pnpm:{pnpmVersion:"12"}}
}});
globalThis.getMinVersion = () => "0.0.0";`, server.URL, digest.SHA256Of(payload).String(), "probe"+exeSuffix(), runtime.GOOS, runtime.GOARCH, libcs, foreignOS, runtime.GOARCH))
	cache := t.TempDir()
	options := clitest.RunOptions{Dir: project.Dir, CacheDir: cache, Env: []string{"DATAMITSU_OFFLINE="}}
	args := runtimeArgs(project, cfg, "run", "bun", "--", "--exit", "37")
	first := clitest.Run(t, options, args...)
	if first.ExitCode != 37 || first.Stdout != "probe stdout\n" {
		t.Fatalf("cold Bun execution: %+v", first)
	}
	options.Env = []string{"DATAMITSU_OFFLINE=1"}
	second := clitest.Run(t, options, args...)
	if second.ExitCode != 37 || second.Stdout != "probe stdout\n" {
		t.Fatalf("offline Bun execution: %+v", second)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("cold/offline Bun executions fetched the interpreter %d times, want 1", got)
	}
}
