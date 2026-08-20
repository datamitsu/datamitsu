package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// These tests freeze the property the machine-level toolchain rests on: a config
// named explicitly with --config is resolved from its own path, and nothing on
// that path requires a git root. Config discovery is git-root-only, so without
// this property `datamitsu source <shell> --config <path>` from a shell rc file
// could never work outside a checkout.
//
// The code path that decides it is cmd/config_loader.go's loadConfigImpl: the
// git-root lookup is entered only for auto-discovery, and a failure there is
// fatal solely when traverser.HasGitDir(cwd) says a .git directory exists.
// Outside a repository the lookup fails, autoConfigPath stays empty, and
// buildConfigSources appends the --config paths regardless.

// explicitAppConfigJS declares one shell app and discards the inherited config,
// so the app list is exactly what this file declares. Shell apps download
// nothing, keeping the run hermetic and offline.
const explicitAppConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({
  apps: { "machine-tool": { shell: { name: "true" } } },
  runtimes: {}, setup: {}, tools: {}
});
globalThis.getMinVersion = () => "0.0.0";
`

// autoOnlyAppConfigJS is the auto-discovered git-root config for the merge
// cases. It too discards the inherited default, so "auto-app" is the only name
// its half of the chain contributes.
const autoOnlyAppConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({
  apps: { "auto-app": { shell: { name: "true" } } },
  runtimes: {}, setup: {}, tools: {}
});
globalThis.getMinVersion = () => "0.0.0";
`

// explicitMergeConfigJS is the --config half: it spreads its input, so whatever
// the layer below contributed survives alongside "explicit-app". That spread is
// what makes the merge observable — with the auto config in the chain both names
// appear, with --no-auto-config only "explicit-app" does.
const explicitMergeConfigJS = `globalThis.getConfig = (config) => ({
  ...config,
  apps: { ...(config.apps || {}), "explicit-app": { shell: { name: "true" } } }
});
globalThis.getMinVersion = () => "0.0.0";
`

// TestExplicitConfigResolvesWithoutGitRoot is the load-bearing case: a config
// file in a directory that is not inside any repository resolves its apps, with
// no git root anywhere on the path and no error about one.
func TestExplicitConfigResolvesWithoutGitRoot(t *testing.T) {
	d := clitest.NewBareDir(t)
	cfg := d.WriteFile("machine.config.js", explicitAppConfigJS)

	res := clitest.Run(t, clitest.RunOptions{Dir: d.Dir}, "--config", cfg, "exec")
	if res.ExitCode != 0 {
		t.Fatalf("`--config <path> exec` outside a repository exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "machine-tool") {
		t.Errorf("the explicitly named config's app is missing from the listing:\n%s", res.Stdout)
	}
	if res.Stderr != "" {
		t.Errorf("resolving an explicit config outside a repository must be silent, got stderr:\n%s", res.Stderr)
	}
}

// TestExplicitConfigMissingPathErrors locks the error contract for a --config
// path that does not exist: non-zero exit, and the failing path named so the
// user can see which entry of the chain is wrong. Silently skipping a named
// config would be far worse here than failing — a machine-level farm would bake
// from a config the user believes is loaded.
func TestExplicitConfigMissingPathErrors(t *testing.T) {
	d := clitest.NewBareDir(t)
	missing := filepath.Join(d.Dir, "absent.config.js")

	res := clitest.Run(t, clitest.RunOptions{Dir: d.Dir}, "--config", missing, "exec")
	if res.ExitCode == 0 {
		t.Fatalf("`--config <missing> exec` exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, missing) {
		t.Errorf("stderr should name the missing config path %q:\n%s", missing, res.Stderr)
	}
	if res.Stdout != "" {
		t.Errorf("a failed config load must write nothing to stdout, got:\n%s", res.Stdout)
	}
}

// TestExplicitConfigMergesWithDiscovered confirms how --config composes inside a
// repository: it is appended after the auto-discovered git-root config, so both
// halves' apps are present.
func TestExplicitConfigMergesWithDiscovered(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js", autoOnlyAppConfigJS)
	cfg := p.WriteFile("extra.config.js", explicitMergeConfigJS)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "--config", cfg, "exec")
	if res.ExitCode != 0 {
		t.Fatalf("`--config <path> exec` in a repository exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "auto-app") {
		t.Errorf("the discovered config's app is missing, so --config replaced the chain instead of extending it:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "explicit-app") {
		t.Errorf("the explicitly named config's app is missing:\n%s", res.Stdout)
	}
}

// TestExplicitConfigNoAutoConfigSuppressesDiscovered is the converse:
// --no-auto-config drops the discovered half while the explicitly named half
// still loads. This is the composition the machine-level toolchain uses inside a
// repository — name the config, ignore whatever the checkout declares.
func TestExplicitConfigNoAutoConfigSuppressesDiscovered(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js", autoOnlyAppConfigJS)
	cfg := p.WriteFile("extra.config.js", explicitMergeConfigJS)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "exec")
	if res.ExitCode != 0 {
		t.Fatalf("`--no-auto-config --config <path> exec` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "explicit-app") {
		t.Errorf("the explicitly named config's app is missing under --no-auto-config:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "auto-app") {
		t.Errorf("--no-auto-config must suppress the discovered git-root config:\n%s", res.Stdout)
	}
}
