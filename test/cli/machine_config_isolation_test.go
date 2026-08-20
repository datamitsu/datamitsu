package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// This file freezes the containment property the machine-level toolchain rests
// on from the other side: a config that exists on disk in any of the locations a
// user would plausibly keep one is invisible to `lint`, `fix` and `check` unless
// it is named with --config.
//
// It matters because of cache.calculateInvalidationKey
// (internal/cache/cache.go), which marshals the whole config.Config into the
// execution-cache key. If a machine-level config could reach the chain
// implicitly, every project's lint cache would depend on the developer's
// personal config and a CI run and a local run of the same commit would
// disagree. `config show` prints exactly the json.Marshal of that same
// config.Config, so byte-identical output here is byte-identical key input.

// machineOnlyAppName appears only in the planted machine-level configs. Its
// absence from a project's resolved config is what proves nothing implicit read
// them.
const machineOnlyAppName = "machine-only-app"

// machineLevelConfigJS is what gets planted in every candidate discovery
// location. It spreads its input and adds one distinctive app, so if any
// location were ever read the name would surface in `config show` and in the
// invalidation key rather than being silently absorbed.
const machineLevelConfigJS = `globalThis.getConfig = (config) => ({
  ...config,
  apps: { ...(config.apps || {}), "` + machineOnlyAppName + `": { shell: { name: "true" } } }
});
globalThis.getMinVersion = () => "0.0.0";
`

// machineConfigLocations are the paths a machine-level config could live at if
// datamitsu ever grew an implicit discovery layer: the XDG config home, a
// dotfile directory under $HOME, $HOME itself, and — for the upward-walk
// variant — the directory containing the project.
//
// They are relative to $HOME except the last, which is handled separately.
var machineConfigLocations = []string{
	".config/datamitsu/datamitsu.config.js",
	".config/datamitsu/datamitsu.config.ts",
	".config/datamitsu/datamitsu.config.mjs",
	".datamitsu/datamitsu.config.js",
	"datamitsu.config.js",
}

// plantMachineConfigs writes machineLevelConfigJS to every candidate location
// under home, plus the parent directory of projectDir (the upward-walk case).
func plantMachineConfigs(t *testing.T, home, projectDir string) {
	t.Helper()
	paths := make([]string, 0, len(machineConfigLocations)+1)
	for _, rel := range machineConfigLocations {
		paths = append(paths, filepath.Join(home, rel))
	}
	paths = append(paths, filepath.Join(filepath.Dir(projectDir), "datamitsu.config.js"))

	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(machineLevelConfigJS), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		t.Cleanup(func() { _ = os.Remove(p) })
	}
}

// machineHomeEnv pins $HOME and $XDG_CONFIG_HOME at home so the run reads the
// planted fixture rather than the developer's real home directory. clitest's
// BaseEnv strips every DATAMITSU_* var but deliberately not these two, so a test
// that cares about them must pin them itself.
func machineHomeEnv(home string) []string {
	return []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config")}
}

// TestMachineConfigDoesNotReachTheProjectConfig runs `config show` in a project
// twice against the same pinned $HOME — once with nothing planted, once with a
// machine-level config in every candidate location — and requires the two
// outputs to be byte-identical.
//
// `config show` is json.MarshalIndent of the same config.Config that the
// execution-cache invalidation key marshals, so identical bytes here means the
// key is identical too: lint, fix and check are unaffected by a machine-level
// config being present on disk.
func TestMachineConfigDoesNotReachTheProjectConfig(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js", autoOnlyAppConfigJS)
	home := t.TempDir()
	cacheDir := t.TempDir()

	opts := clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir, Env: machineHomeEnv(home)}

	before := clitest.Run(t, opts, "config", "show")
	if before.ExitCode != 0 {
		t.Fatalf("`config show` exit = %d, want 0\nstderr:\n%s", before.ExitCode, before.Stderr)
	}

	plantMachineConfigs(t, home, p.Dir)

	after := clitest.Run(t, opts, "config", "show")
	if after.ExitCode != 0 {
		t.Fatalf("`config show` with machine configs planted exit = %d, want 0\nstderr:\n%s",
			after.ExitCode, after.Stderr)
	}
	if after.Stdout != before.Stdout {
		t.Errorf("a machine-level config on disk changed the resolved project config, "+
			"so it also changed the execution-cache invalidation key\nwithout:\n%s\nwith:\n%s",
			before.Stdout, after.Stdout)
	}
	if strings.Contains(after.Stdout, machineOnlyAppName) {
		t.Errorf("%q reached the project config: an implicit discovery layer exists\n%s",
			machineOnlyAppName, after.Stdout)
	}
}

// TestMachineConfigDoesNotAffectPlanning is the same containment property at the
// level the user sees it: the check, fix and lint plans are byte-identical with
// and without a machine-level config on disk.
func TestMachineConfigDoesNotAffectPlanning(t *testing.T) {
	for _, cmd := range []string{"check", "fix", "lint"} {
		t.Run(cmd, func(t *testing.T) {
			p := clitest.NewProject(t)
			p.WriteFile("datamitsu.config.js", explainToolsConfigJS)
			home := t.TempDir()
			cacheDir := t.TempDir()
			norm := clitest.NewNormalizer().MaskPath(p.Dir, "<TMP>")

			opts := clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir, Env: machineHomeEnv(home)}

			before := clitest.Run(t, opts, cmd, "--explain=json")
			if before.ExitCode != 0 {
				t.Fatalf("`%s --explain=json` exit = %d, want 0\nstderr:\n%s", cmd, before.ExitCode, before.Stderr)
			}

			plantMachineConfigs(t, home, p.Dir)

			after := clitest.Run(t, opts, cmd, "--explain=json")
			if after.ExitCode != 0 {
				t.Fatalf("`%s --explain=json` with machine configs planted exit = %d, want 0\nstderr:\n%s",
					cmd, after.ExitCode, after.Stderr)
			}
			if norm.Apply(after.Stdout) != norm.Apply(before.Stdout) {
				t.Errorf("a machine-level config on disk changed the `%s` plan\nwithout:\n%s\nwith:\n%s",
					cmd, norm.Apply(before.Stdout), norm.Apply(after.Stdout))
			}
		})
	}
}

// TestMachineConfigReachesTheChainOnlyWhenNamed is the positive control for both
// tests above: the planted config is not inert — naming one of the same files
// with --config does put its app in the resolved config. Without this, the
// identical-bytes assertions could pass against a config that never contributed
// anything anywhere.
func TestMachineConfigReachesTheChainOnlyWhenNamed(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js", autoOnlyAppConfigJS)
	home := t.TempDir()
	plantMachineConfigs(t, home, p.Dir)
	named := filepath.Join(home, machineConfigLocations[0])

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Env: machineHomeEnv(home)},
		"--config", named, "config", "show")
	if res.ExitCode != 0 {
		t.Fatalf("`--config <machine config> config show` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, machineOnlyAppName) {
		t.Fatalf("naming the machine-level config did not add %q, so the containment tests above "+
			"are asserting on an inert fixture:\n%s", machineOnlyAppName, res.Stdout)
	}
}
