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

func TestInstallerChildCannotRunRuntimeButCanInspectIt(t *testing.T) {
	project, configPath, _ := runtimeProject(t, runtimeProbe(t))
	cache := t.TempDir()
	record := filepath.Join(project.Dir, "must-not-run.json")
	options := clitest.RunOptions{Dir: project.Dir, CacheDir: cache, Env: []string{"DATAMITSU_INSTALL_CHILD=1"}}
	blocked := clitest.Run(t, options, runtimeArgs(project, configPath, "run", "zed", "--", "--record", record)...)
	if blocked.ExitCode != 1 || blocked.Stdout != "" || blocked.Stderr == "" {
		t.Fatalf("installer reentry was not rejected before interpreter execution: %+v", blocked)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("installer reentry executed the interpreter: %v", err)
	}
	for _, args := range [][]string{{"list", "--json"}, {"run", "--dry-run", "zed", "--", "--record", record}} {
		result := clitest.Run(t, options, runtimeArgs(project, configPath, args...)...)
		if result.ExitCode != 0 {
			t.Fatalf("installer child could not perform read-only runtime inspection: %+v", result)
		}
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("read-only inspection executed the interpreter: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "store")); !os.IsNotExist(err) {
		t.Fatalf("runtime reentry or inspection materialized the store: %v", err)
	}
}

func TestUnavailableRuntimeFailsBeforeProvisioningEvenForDryRun(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	platform := "windows"
	if runtime.GOOS == "windows" {
		platform = "linux"
	}
	project := clitest.NewProject(t)
	configPath := project.WriteFile("unavailable.config.js", fmt.Sprintf(`globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({version:"fixture",apps:{},tools:{},managedConfigs:{},runtimes:{probe:{
kind:"go",mode:"managed",go:{goVersion:"1.23"},managed:{binaries:{%q:{%q:{unknown:{url:%q,hash:%q,contentType:"binary",binaryPath:"probe"}}}}}
}}});
globalThis.getMinVersion = () => "0.0.0";`, platform, runtime.GOARCH, server.URL, digest.SHA256Of([]byte("unused")).String()))
	cache := t.TempDir()
	for _, args := range [][]string{{"run", "probe", "--", "unused"}, {"run", "--dry-run", "probe", "--", "unused"}} {
		result := clitest.Run(t, clitest.RunOptions{Dir: project.Dir, CacheDir: cache, Env: []string{"DATAMITSU_OFFLINE="}}, runtimeArgs(project, configPath, args...)...)
		if result.ExitCode != 1 || result.Stdout != "" || result.Stderr == "" {
			t.Fatalf("unavailable host target was not rejected: %+v", result)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("unavailable target triggered %d artifact requests", requests.Load())
	}
	if _, err := os.Stat(filepath.Join(cache, "store")); !os.IsNotExist(err) {
		t.Fatalf("unavailable runtime materialized store state: %v", err)
	}
}
