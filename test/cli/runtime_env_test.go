package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

func TestExecRuntimeEnvDependency(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executables need a POSIX shell")
	}
	p := clitest.NewProject(t)
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	arch, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	binary := &binmanager.AppConfigBinary{Binaries: binmanager.MapOfBinaries{osType: {arch: {"unknown": {URL: "https://example.invalid/offline-fixture", Hash: strings.Repeat("0", 64), ContentType: binmanager.BinContentTypeBinary}}}}}
	apps := binmanager.MapOfApps{
		"proxy":            {Binary: binary, DependsOn: []string{"private-upstream"}, RuntimeEnv: map[string]string{"UPSTREAM": "${APP_BIN:private-upstream}"}},
		"private-upstream": {Binary: binary, Lazy: true},
	}
	data, err := json.Marshal(apps)
	if err != nil {
		t.Fatal(err)
	}
	cfg := p.WriteFile("bindings.config.js", `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({apps: `+string(data)+`, tools: {}, runtimes: {}});
globalThis.getMinVersion = () => "0.0.0";
`)
	bm := binmanager.New(apps, nil, nil)
	dep, _, err := bm.ResolveCommandInfo("private-upstream")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"proxy": "#!/bin/sh\nprintf '%s\\n' \"$UPSTREAM\"\n\"$UPSTREAM\"\n", "private-upstream": "#!/bin/sh\nprintf 'upstream executed\\n'\n"} {
		info, _, err := bm.ResolveCommandInfo(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(info.Command), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(info.Command, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	opts := clitest.RunOptions{Dir: p.Dir, CacheDir: cache}
	for range 2 {
		res := clitest.Run(t, opts, "--no-auto-config", "--config", cfg, "exec", "proxy")
		if res.ExitCode != 0 || res.Stdout != dep.Command+"\nupstream executed\n" {
			t.Fatalf("exit=%d stdout=%s stderr=%s", res.ExitCode, res.Stdout, res.Stderr)
		}
		output := strings.ReplaceAll(res.Stdout, dep.Command, "<DEP_BIN>")
		clitest.AssertGolden(t, "exec_runtime_env_dependency", fmt.Sprintf("exit: %d\nstdout:\n%s%s%s", res.ExitCode, output, "stderr:\n", res.Stderr))
	}
	show := clitest.Run(t, opts, "--no-auto-config", "--config", cfg, "config", "show")
	if show.ExitCode != 0 || !strings.Contains(show.Stdout, "${APP_BIN:private-upstream}") {
		t.Fatalf("cached config lost symbolic binding: %+v", show)
	}
}

func TestConfigRuntimeEnvValidation(t *testing.T) {
	for _, tt := range []struct{ name, extra, want string }{
		{"malformed", `apps: {proxy: {shell: {name: "echo"}, runtimeEnv: {BIN: "${APP_BIN:}"}}}`, "malformed APP_BIN"},
		{"env", `apps: {proxy: {shell: {name: "echo"}, env: {BIN: "${APP_BIN:dep}"}}}`, "apps.proxy.env.BIN"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := p.WriteFile("bindings.config.js", `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({`+tt.extra+`});
globalThis.getMinVersion = () => "0.0.0";
`)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "--no-auto-config", "--config", cfg, "config", "show")
			if res.ExitCode != 1 || !strings.Contains(res.Stderr, tt.want) {
				t.Fatalf("exit=%d stderr=%s", res.ExitCode, res.Stderr)
			}
			clitest.AssertGolden(t, "config_runtime_env_"+tt.name, clitest.NewNormalizer().Apply(fmt.Sprintf("exit: %d\nstdout:\n%s%s%s", res.ExitCode, res.Stdout, "stderr:\n", res.Stderr)))
		})
	}
}
