package cli_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestConfigAppDependencies(t *testing.T) {
	for _, tt := range []struct{ name, apps, wantErr string }{
		{"missing", `a: {binary: {}, dependsOn: ["missing"]}`, `app "a": dependency "missing" not found in apps`},
		{"cycle", `a: {binary: {}, dependsOn: ["b"]}, b: {binary: {}, dependsOn: ["c"]}, c: {binary: {}, dependsOn: ["a"]}`, `a -> b -> c -> a`},
		{"diamond", `a: {binary: {}, dependsOn: ["b", "c"]}, b: {binary: {}, dependsOn: ["d"]}, c: {binary: {}, dependsOn: ["d"]}, d: {binary: {}}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := p.WriteFile("apps.config.js", `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({apps: {`+tt.apps+`}, runtimes: {}, managedConfigs: {}, tools: {}});
globalThis.getMinVersion = () => "0.0.0";
`)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "--no-auto-config", "--config", cfg, "config", "show")
			wantExit := 0
			if tt.wantErr != "" {
				wantExit = 1
			}
			if res.ExitCode != wantExit {
				t.Fatalf("exit = %d, want %d: %s", res.ExitCode, wantExit, res.Stderr)
			}
			if tt.wantErr != "" && !strings.Contains(res.Stderr, tt.wantErr) {
				t.Fatalf("missing validation error: %s", res.Stderr)
			}
			clitest.AssertGolden(t, "config_app_dependencies_"+tt.name, clitest.NewNormalizer().Apply(fmt.Sprintf("exit: %d\nstdout:\n%s%s%s", res.ExitCode, res.Stdout, "stderr:\n", res.Stderr)))
		})
	}
}

func TestInstallAppDependency(t *testing.T) {
	p := clitest.NewProject(t)
	js := strings.Replace(installBinaryConfigJS, `"mytool": { binary: mkBin() }`, `"mytool": { binary: mkBin(), lazy: true }, "proxy": { shell: {name: "echo"}, dependsOn: ["mytool"] }`, 1)
	cfg := p.WriteFile("apps.config.js", js)
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "--no-auto-config", "--config", cfg, "install", "proxy", "--no-verify")
	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "failed to install mytool") || !strings.Contains(res.Stderr, "offline") {
		t.Fatalf("dependency was not installed: exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
}
