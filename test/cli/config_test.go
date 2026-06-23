package cli_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// expectedConfigSubcommands is the exact set of `config` leaf commands. Like
// expectedTopLevelCommands it is a drift guard: a new config subcommand must be
// added here deliberately, with its own blackbox test.
var expectedConfigSubcommands = []string{
	"chain-hash",
	"lockfile",
	"runtime",
	"show",
	"types",
}

// TestConfigHelpGolden freezes `config --help`: a static, offline help block
// with no version or path tokens, so the normalized output equals the raw
// output. The subcommand set is additionally compared as a set to decouple the
// drift guard from help formatting.
func TestConfigHelpGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{}, "config", "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`config --help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`config --help` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "config_help", norm.Apply(res.Stdout))

	got := parseAvailableCommands(res.Stdout)
	if strings.Join(sortedCopy(got), ",") != strings.Join(sortedCopy(expectedConfigSubcommands), ",") {
		t.Errorf("config subcommand set drift:\n got: %v\nwant: %v", got, expectedConfigSubcommands)
	}
}

// TestConfigShow freezes `config show` against the minimal config: the no-op
// config returns empty collections which marshal (omitempty) to a bare object,
// so the contract is the literal "{}" plus the guarantee that it is valid JSON
// with no keys.
func TestConfigShow(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "show")
	if res.ExitCode != 0 {
		t.Fatalf("`config show` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	clitest.AssertGolden(t, "config_show", norm.Apply(res.Stdout))

	var obj map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &obj); err != nil {
		t.Fatalf("`config show` did not emit valid JSON: %v\nstdout:\n%s", err, res.Stdout)
	}
	if len(obj) != 0 {
		t.Errorf("minimal config show should be empty, got keys: %v", keysOf(obj))
	}
}

// TestConfigTypes asserts `config types` prints a non-empty .d.ts whose leading
// lines are stable. We do not golden the whole 1300-line file (it churns with
// the type surface); the contract under test is "exit 0, non-empty, starts with
// the ambient `declare global` block".
func TestConfigTypes(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "config", "types")
	if res.ExitCode != 0 {
		t.Fatalf("`config types` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`config types` wrote to stderr:\n%s", res.Stderr)
	}
	if len(res.Stdout) < 256 {
		t.Fatalf("`config types` output is suspiciously short (%d bytes):\n%s", len(res.Stdout), res.Stdout)
	}
	const wantPrefix = "declare global {\n"
	if !strings.HasPrefix(res.Stdout, wantPrefix) {
		t.Errorf("`config types` does not start with %q; got first line:\n%s",
			wantPrefix, firstLine(res.Stdout))
	}
}

// TestConfigRuntime asserts `config runtime` emits valid JSON with the stable,
// machine-independent runtime keys at their documented defaults. Machine- and
// platform-dependent fields (maxParallelWorkers, libc) are asserted present but
// not pinned to a value, per the runtimeconfig "required keys, not field count"
// policy. offline/noOci are true because the harness runs hermetically offline.
func TestConfigRuntime(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "config", "runtime")
	if res.ExitCode != 0 {
		t.Fatalf("`config runtime` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	var eff map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &eff); err != nil {
		t.Fatalf("`config runtime` did not emit valid JSON: %v\nstdout:\n%s", err, res.Stdout)
	}

	wantNum := map[string]float64{
		"concurrency":              3,
		"installTimeoutSeconds":    600,
		"maxCmdLength":             32000,
		"maxErrorCmdDisplay":       120,
		"minimumReleaseAgeMinutes": 10080,
	}
	for k, want := range wantNum {
		got, ok := eff[k].(float64)
		if !ok {
			t.Errorf("runtime[%q] missing or not a number: %v", k, eff[k])
			continue
		}
		if got != want {
			t.Errorf("runtime[%q] = %v, want %v", k, got, want)
		}
	}
	if eff["logLevel"] != "warn" {
		t.Errorf("runtime[logLevel] = %v, want warn", eff["logLevel"])
	}
	if eff["ociRegistry"] != "ghcr.io" {
		t.Errorf("runtime[ociRegistry] = %v, want ghcr.io", eff["ociRegistry"])
	}
	if eff["offline"] != true {
		t.Errorf("runtime[offline] = %v, want true (harness is offline)", eff["offline"])
	}
	if eff["noOci"] != true {
		t.Errorf("runtime[noOci] = %v, want true (harness disables OCI)", eff["noOci"])
	}
	if eff["timings"] != false {
		t.Errorf("runtime[timings] = %v, want false", eff["timings"])
	}
	// Present but machine/platform dependent — assert existence only.
	for _, k := range []string{"maxParallelWorkers", "libc"} {
		if _, ok := eff[k]; !ok {
			t.Errorf("runtime missing key %q", k)
		}
	}
}

// TestConfigRuntimeEnvOverride proves env overrides flow through to the
// effective snapshot, the documented introspection contract
// (`DATAMITSU_INSTALL_TIMEOUT=1200 datamitsu config runtime`).
func TestConfigRuntimeEnvOverride(t *testing.T) {
	cases := []struct {
		name string
		env  string
		key  string
		want float64
	}{
		{"install-timeout", "DATAMITSU_INSTALL_TIMEOUT=1200", "installTimeoutSeconds", 1200},
		{"min-release-age", "DATAMITSU_MIN_RELEASE_AGE=99", "minimumReleaseAgeMinutes", 99},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{Env: []string{tc.env}}, "config", "runtime")
			if res.ExitCode != 0 {
				t.Fatalf("`config runtime` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
			}
			var eff map[string]any
			if err := json.Unmarshal([]byte(res.Stdout), &eff); err != nil {
				t.Fatalf("invalid JSON: %v\nstdout:\n%s", err, res.Stdout)
			}
			got, ok := eff[tc.key].(float64)
			if !ok {
				t.Fatalf("runtime[%q] missing or not a number: %v", tc.key, eff[tc.key])
			}
			if got != tc.want {
				t.Errorf("with %s, runtime[%q] = %v, want %v", tc.env, tc.key, got, tc.want)
			}
		})
	}
}

// chainHashConfigJS is a config with two setup files used to exercise
// `config chain-hash`. The files exist on disk with fixed content, so the
// chain hashes (XXH3-128 of the content entering each file's root layer) are
// deterministic across runs and machines.
const chainHashConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({
  apps: {}, runtimes: {}, tools: {},
  setup: {
    "alpha.txt": { content: () => "alpha\n" },
    "beta.config.json": { content: () => "{}\n" },
  },
});
globalThis.getMinVersion = () => "0.0.0";
`

// newChainHashProject writes the two-file setup config plus the on-disk files it
// hashes, returning the project and the config path.
func newChainHashProject(t *testing.T) (*clitest.Project, string) {
	t.Helper()
	p := clitest.NewProject(t)
	cfg := p.WriteFile("chain-hash.config.js", chainHashConfigJS)
	p.WriteFile("alpha.txt", "alpha\n")
	p.WriteFile("beta.config.json", "{}\n")
	return p, cfg
}

// TestConfigChainHashTable freezes the no-args table: every setup file in a
// left-aligned "file  hash" grid, sorted by filename.
func TestConfigChainHashTable(t *testing.T) {
	p, cfg := newChainHashProject(t)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "chain-hash")
	if res.ExitCode != 0 {
		t.Fatalf("`config chain-hash` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	clitest.AssertGolden(t, "config_chain_hash_table", norm.Apply(res.Stdout))
}

// TestConfigChainHashSingle proves the scripting contract: with exactly one
// file the bare hash is printed (no filename column), and it matches that
// file's row in the full table.
func TestConfigChainHashSingle(t *testing.T) {
	p, cfg := newChainHashProject(t)

	table := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "chain-hash")
	if table.ExitCode != 0 {
		t.Fatalf("`config chain-hash` exit = %d, want 0\nstderr:\n%s", table.ExitCode, table.Stderr)
	}
	wantHash := hashFromTableRow(t, table.Stdout, "alpha.txt")

	single := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "chain-hash", "alpha.txt")
	if single.ExitCode != 0 {
		t.Fatalf("`config chain-hash alpha.txt` exit = %d, want 0\nstderr:\n%s", single.ExitCode, single.Stderr)
	}
	got := strings.TrimRight(single.Stdout, "\n")
	if strings.Contains(got, " ") {
		t.Errorf("single-file output should be a bare hash (no padding), got: %q", got)
	}
	if !strings.HasPrefix(got, "xxh3:") {
		t.Errorf("single-file hash missing xxh3: prefix: %q", got)
	}
	if got != wantHash {
		t.Errorf("single-file hash %q != alpha.txt table row %q", got, wantHash)
	}
}

// TestConfigChainHashUnknown asserts a typo'd file name is a non-zero error that
// names the unknown file, never a silent empty success.
func TestConfigChainHashUnknown(t *testing.T) {
	p, cfg := newChainHashProject(t)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "chain-hash", "does-not-exist.txt")
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unknown file, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "no setup config named") ||
		!strings.Contains(res.Stderr, "does-not-exist.txt") {
		t.Errorf("stderr should name the unknown file:\n%s", res.Stderr)
	}
}

// hashFromTableRow returns the hash column for the row whose first field equals
// file, failing the test if the row is absent.
func hashFromTableRow(t *testing.T, table, file string) string {
	t.Helper()
	for line := range strings.SplitSeq(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == file {
			return fields[1]
		}
	}
	t.Fatalf("table has no row for %q:\n%s", file, table)
	return ""
}

// TestConfigLockfile locks the offline contract of `config lockfile`. With no
// arguments it lists lock-file-capable apps (node/uv/go); against the minimal
// config there are none, so it reports the empty-list notice on stderr and exits
// 0 without reinstalling anything. An unknown app name is a config error. Neither
// path touches the network or a runtime install.
func TestConfigLockfile(t *testing.T) {
	t.Run("list-empty", func(t *testing.T) {
		p := clitest.NewProject(t)
		cfg := clitest.WriteMinimalConfig(p)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "config", "lockfile")
		if res.ExitCode != 0 {
			t.Fatalf("`config lockfile` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		if res.Stdout != "" {
			t.Errorf("`config lockfile` against empty config should print nothing to stdout, got:\n%s", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "No apps with lock file support found") {
			t.Errorf("stderr missing empty-list notice:\n%s", res.Stderr)
		}
	})

	t.Run("unknown-app", func(t *testing.T) {
		p := clitest.NewProject(t)
		cfg := clitest.WriteMinimalConfig(p)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "config", "lockfile", "nonesuch")
		if res.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unknown app, got 0\nstdout:\n%s", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "not found in configuration") {
			t.Errorf("stderr missing not-found message naming the app:\n%s", res.Stderr)
		}
		if strings.Contains(res.Stderr, "Usage:") {
			t.Errorf("config error printed usage block (SilenceUsage broken):\n%s", res.Stderr)
		}
	})
}

// sortedCopy returns a lexicographically sorted copy of s.
func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// firstLine returns the first line of s (without the trailing newline).
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// keysOf returns the keys of m (order unspecified) for error messages.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
