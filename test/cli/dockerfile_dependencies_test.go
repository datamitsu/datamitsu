package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestDockerfileDependencyExcluded(t *testing.T) {
	p := clitest.NewProject(t)
	js := strings.Replace(installBinaryConfigJS, `"mytool": { binary: mkBin() }`, `"mytool": { binary: mkBin(), dependsOn: ["upstream"] }, "upstream": {binary: {}}`, 1)
	cfg := p.WriteFile("apps.config.js", js)
	out := filepath.Join(p.Dir, "Dockerfile")
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "--no-auto-config", "--config", cfg, "devtools", "dockerfile", "--output", out, "--offline")
	if res.ExitCode != 1 || !strings.Contains(res.Stderr, `requires dependency "upstream": binary unavailable for the target Linux/libc variant`) {
		t.Fatalf("exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("rejected plan wrote Dockerfile: %v", err)
	}
	clitest.AssertGolden(t, "dockerfile_dependency_excluded", clitest.NewNormalizer().Apply(fmt.Sprintf("exit: %d\nstdout:\n%s%s%s", res.ExitCode, res.Stdout, "stderr:\n", res.Stderr)))
}

func TestSplitConfigDependencyUnsupportedPlatform(t *testing.T) {
	for _, noVerify := range []bool{false, true} {
		t.Run(fmt.Sprintf("noVerify=%t", noVerify), func(t *testing.T) {
			p := clitest.NewProject(t)
			arch := "arm64"
			if runtime.GOARCH == arch {
				arch = "amd64"
			}
			js := strings.Replace(installBinaryConfigJS, `"mytool": { binary: mkBin() }`, `"mytool": { binary: mkBin(), dependsOn: ["upstream"], runtimeEnv: {UPSTREAM: "${APP_BIN:upstream}"} }, "upstream": {binary: {binaries: {linux: {"`+arch+`": {glibc: {url: "https://example.invalid/upstream", hash: H, contentType: "raw"}}}}}}`, 1)
			cfg := p.WriteFile("apps.config.js", js)
			out := filepath.Join(p.Dir, "slices")
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "--no-auto-config", "--config", cfg, "devtools", "split-config", "--output", out)
			if res.ExitCode != 0 {
				t.Fatalf("split: %s", res.Stderr)
			}
			slice := filepath.Join(out, "app-mytool.js")
			res = clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "--no-auto-config", "--config", slice, "config", "show")
			if res.ExitCode != 0 || !strings.Contains(res.Stdout, "${APP_BIN:upstream}") {
				t.Fatalf("slice cannot load symbolic binding: %s %s", res.Stdout, res.Stderr)
			}
			args := []string{"--no-auto-config", "--config", slice, "install", "mytool"}
			if noVerify {
				args = append(args, "--no-verify")
			}
			res = clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, args...)
			if res.ExitCode != 1 || !strings.Contains(res.Stderr, "binary 'upstream' is not available for") {
				t.Fatalf("install: exit=%d stderr=%s", res.ExitCode, res.Stderr)
			}
		})
	}
}
