package cli_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/digest"
)

const managedRuntimeProbeConfig = `globalThis.getBeforeConfigs = () => [];
const binary = {url:%q,hash:%q,contentType:"binary",binaryPath:%q};
globalThis.getConfig = () => ({version:"fixture",apps:{},tools:{},managedConfigs:{},runtimes:{probe:{
kind:"go",mode:"managed",go:{goVersion:"1.23"},managed:{binaries:{%q:{%q:{glibc:binary,musl:binary,unknown:binary}}}}
}}});
globalThis.getMinVersion = () => "0.0.0";`

func TestRuntimeRunAcquiresPinnedManagedRuntimeThenReusesItOffline(t *testing.T) {
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
	project := clitest.NewProject(t)
	configPath := project.WriteFile("managed-runtime.config.js", fmt.Sprintf(managedRuntimeProbeConfig, server.URL, digest.SHA256Of(payload).String(), "runtime-probe"+exeSuffix(), runtime.GOOS, runtime.GOARCH))
	cache := t.TempDir()
	record := filepath.Join(project.Dir, "run.json")
	args := runtimeArgs(project, configPath, "run", "probe", "--", "--record", record, "argument with space")
	first := clitest.Run(t, clitest.RunOptions{Dir: project.Dir, CacheDir: cache, Env: []string{"DATAMITSU_OFFLINE="}}, args...)
	if first.ExitCode != 0 || first.Stdout != "probe stdout\n" {
		t.Fatalf("cold managed runtime did not execute: %+v", first)
	}
	if requests.Load() != 1 {
		t.Fatalf("cold runtime download count = %d, want one", requests.Load())
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("cold runtime did not run the supplied interpreter args: %v", err)
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	second := clitest.Run(t, clitest.RunOptions{Dir: project.Dir, CacheDir: cache}, args...)
	if second.ExitCode != 0 || second.Stdout != "probe stdout\n" || second.Stderr != "probe stderr\n" {
		t.Fatalf("offline managed runtime did not reuse its installation: %+v", second)
	}
	if requests.Load() != 1 {
		t.Fatalf("offline reuse contacted the release host: %d requests", requests.Load())
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("offline runtime did not execute again: %v", err)
	}
}

func TestManagedRuntimeStartFailureDoesNotRetryOrRetainStoreLease(t *testing.T) {
	payload := []byte("\x7fELF\x02\x01\x01invalid executable")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	project := clitest.NewProject(t)
	configPath := project.WriteFile("managed-runtime.config.js", fmt.Sprintf(managedRuntimeProbeConfig, server.URL, digest.SHA256Of(payload).String(), "runtime-probe"+exeSuffix(), runtime.GOOS, runtime.GOARCH))
	cache := t.TempDir()
	args := runtimeArgs(project, configPath, "run", "probe", "--", "unused")
	for _, offline := range []string{"", "1"} {
		result := clitest.Run(t, clitest.RunOptions{Dir: project.Dir, CacheDir: cache, Env: []string{"DATAMITSU_OFFLINE=" + offline}}, args...)
		if result.ExitCode != 1 || result.Stdout != "" || result.Stderr == "" {
			t.Fatalf("invalid managed interpreter did not report a start error: %+v", result)
		}
		if requests.Load() != 1 {
			t.Fatalf("start failure retried provisioning: %d requests", requests.Load())
		}
	}
	cleared := clitest.Run(t, clitest.RunOptions{Dir: project.Dir, CacheDir: cache}, "store", "clear")
	if cleared.ExitCode != 0 {
		t.Fatalf("start failure retained provisioning ownership: %+v", cleared)
	}
}
