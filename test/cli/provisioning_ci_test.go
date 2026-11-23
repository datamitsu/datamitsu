package cli_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/digest"
)

func TestProvisioningProgressNeutralizesNativeCICommands(t *testing.T) {
	payload, err := os.ReadFile(runtimeProbe(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, variable, prefix, runtimeName string
	}{
		{"azure", "TF_BUILD=true", "##vso[", "##vso[task.setvariable variable=x]value"},
		{"teamcity", "TEAMCITY_VERSION=2026.1", "##teamcity[", "##teamcity[setParameter name='x' value='value']"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			project := clitest.NewProject(t)
			config := fmt.Sprintf(`
globalThis.getBeforeConfigs = () => [];
globalThis.getMinVersion = () => "0.0.0";
const binary = {url:%q,hash:%q,contentType:"binary",binaryPath:%q};
globalThis.getConfig = () => ({version:"fixture",apps:{},tools:{},managedConfigs:{},runtimes:{%q:{
kind:"go",mode:"managed",go:{goVersion:"1.23"},managed:{binaries:{%q:{%q:{glibc:binary,musl:binary,unknown:binary}}}}
}}});`, server.URL, digest.SHA256Of(payload).String(), "runtime-probe"+exeSuffix(), tc.runtimeName, runtime.GOOS, runtime.GOARCH)
			configPath := project.WriteFile("runtime.config.js", config)
			args := runtimeArgs(project, configPath, "run", tc.runtimeName, "--", "--record", filepath.Join(project.Dir, "run.json"))
			result := clitest.Run(t, clitest.RunOptions{Dir: project.Dir, CacheDir: t.TempDir(), Env: []string{"DATAMITSU_OFFLINE=", tc.variable}}, args...)
			if result.ExitCode != 0 || result.Stdout != "probe stdout\n" || requests.Load() != 1 {
				t.Fatalf("cold runtime did not execute: requests=%d, result=%+v", requests.Load(), result)
			}
			if strings.Contains(result.Stdout+result.Stderr, tc.prefix) {
				t.Fatalf("progress exposed an executable CI command: %q", result.Stderr)
			}
			brokenPrefix := strings.ReplaceAll(tc.prefix, "[", " [")
			if !strings.Contains(result.Stderr, brokenPrefix) {
				t.Fatalf("runtime identity was hidden instead of safely rendered: %q", result.Stderr)
			}
		})
	}
}
