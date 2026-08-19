package cli_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// testedLeafCommands maps every datamitsu leaf command (a command with no
// further subcommands) to the blackbox test that exercises it. It is the
// contract-completeness registry: TestContractCompletenessGate discovers the
// live leaf set from the binary's `--help` tree and asserts it is exactly
// (testedLeafCommands ∪ builtinLeafCommands). A new leaf command therefore fails
// the build until it is given a blackbox test and registered here, and a removed
// command fails until its stale entry is deleted — neither can drift silently.
//
// Group commands (e.g. `config`, `devtools apps`) are intentionally absent: they
// run no work of their own and are covered by their `--help` goldens and
// command-set drift guards.
var testedLeafCommands = map[string]string{
	"cache clear":                  "TestCacheClearDryRun",
	"cache path project":           "TestCachePathProject",
	"check":                        "TestExplainPlanGolden / TestCheckFixLintHelpGolden",
	"config chain-hash":            "TestConfigChainHashTable",
	"config lockfile":              "TestConfigLockfile",
	"config runtime":               "TestConfigRuntime",
	"config show":                  "TestConfigShow",
	"config types":                 "TestConfigTypes",
	"devtools apps inspect":        "TestDevtoolsAppsInspectPath",
	"devtools apps list":           "TestDevtoolsAppsList",
	"devtools apps path":           "TestDevtoolsAppsInspectPath",
	"devtools bundles inspect":     "TestDevtoolsBundlesInspectPath",
	"devtools bundles list":        "TestDevtoolsBundlesList",
	"devtools bundles path":        "TestDevtoolsBundlesInspectPath",
	"devtools dockerfile":          "TestDevtoolsHelpGolden / TestDevtoolsArgValidation",
	"devtools pack-inline-archive": "TestDevtoolsArgValidation",
	"devtools parsers inspect":     "TestDevtoolsParsersInspect",
	"devtools parsers list":        "TestDevtoolsParsersList / TestDevtoolsParsersListJSONEmpty",
	"devtools parsers prefetch":    "TestDevtoolsParsersPrefetch",
	"devtools parsers run":         "TestDevtoolsParsersRun",
	"devtools pull-github":         "TestDevtoolsArgValidation",
	"devtools pull-node":           "TestDevtoolsArgValidation",
	"devtools pull-runtimes":       "TestDevtoolsArgValidation",
	"devtools pull-uv":             "TestDevtoolsArgValidation",
	"devtools split-config":        "TestDevtoolsHelpGolden / TestDevtoolsArgValidation",
	"devtools tools inspect":       "TestDevtoolsToolsInspect",
	"devtools tools list":          "TestDevtoolsToolsList",
	"devtools verify-all":          "TestDevtoolsHelpGolden",
	"exec":                         "TestExecListEmpty / TestExecListGrouped",
	"fix":                          "TestExplainPlanGolden",
	"init":                         "TestInitNoopSuccess / TestInitDryRunGolden",
	"install":                      "TestInstallNoTargets",
	"lint":                         "TestExplainPlanGolden",
	"llms":                         "TestLlmsHelpGolden / TestLlmsRootIndex / TestLlmsUnknownPageGolden",
	"lsp":                          "TestLspFormattingSession",
	"setup":                        "TestSetupDryRunGolden",
	"source bash":                  "TestSourceBashActivation",
	"source fish":                  "TestSourceFishActivation",
	"source refresh":               "TestSourceRefresh",
	"source status":                "TestSourceStatus",
	"source zsh":                   "TestSourceZshActivation",
	"store clear":                  "TestStoreClear",
	"store import":                 "TestStoreImportArgValidation",
	"store path":                   "TestStorePath",
	"store refs":                   "TestStoreRefsPopulated / TestStoreRefsEmpty",
	"store seed":                   "TestStoreSeedArgValidation",
	"store status":                 "TestStoreStatusNoOCI",
	"version":                      "TestVersionGolden",
}

// builtinLeafCommands are cobra-generated leaf commands that are part of the CLI
// surface but not datamitsu's own behavior, so they are exempt from the
// per-command blackbox-test requirement. They are still enumerated here so the
// gate stays exhaustive: a new builtin (or the disappearance of one) is flagged.
var builtinLeafCommands = map[string]bool{
	"help":                  true,
	"completion bash":       true,
	"completion fish":       true,
	"completion powershell": true,
	"completion zsh":        true,
}

// TestContractCompletenessGate is the registry-driven guard that every leaf
// command has at least one blackbox test. It walks the binary's `--help` tree to
// discover the live leaf set and asserts it equals the union of the tested and
// builtin registries — failing on an untested new command, an unregistered
// builtin, or a stale registry entry alike.
func TestContractCompletenessGate(t *testing.T) {
	leaves := discoverLeafCommands(t)
	discovered := make(map[string]bool, len(leaves))
	for _, leaf := range leaves {
		discovered[leaf] = true
	}

	// 1. Every discovered leaf is either tested or a registered builtin.
	for _, leaf := range leaves {
		_, tested := testedLeafCommands[leaf]
		if !tested && !builtinLeafCommands[leaf] {
			t.Errorf("leaf command %q has no registered blackbox test; add one and register it in testedLeafCommands", leaf)
		}
	}

	// 2. No stale registry entries: every registered command must still exist.
	for cmd := range testedLeafCommands {
		if !discovered[cmd] {
			t.Errorf("testedLeafCommands has stale entry %q (command no longer exists as a leaf)", cmd)
		}
	}
	for cmd := range builtinLeafCommands {
		if !discovered[cmd] {
			t.Errorf("builtinLeafCommands has stale entry %q (command no longer exists as a leaf)", cmd)
		}
	}
}

// discoverLeafCommands walks the command tree by recursively reading each
// command's `--help` output and collecting the paths that expose no further
// subcommands (the leaves). Paths are space-joined, e.g. "devtools apps list".
func discoverLeafCommands(t *testing.T) []string {
	t.Helper()
	var leaves []string
	var walk func(path []string)
	walk = func(path []string) {
		args := append(append([]string(nil), path...), "--help")
		res := clitest.Run(t, clitest.RunOptions{}, args...)
		if res.ExitCode != 0 {
			t.Fatalf("`%s --help` exit = %d, want 0\nstderr:\n%s",
				strings.Join(path, " "), res.ExitCode, res.Stderr)
		}
		subs := parseAvailableCommands(res.Stdout)
		if len(subs) == 0 {
			leaves = append(leaves, strings.Join(path, " "))
			return
		}
		for _, s := range subs {
			walk(append(append([]string(nil), path...), s))
		}
	}
	walk(nil)
	sort.Strings(leaves)
	return leaves
}

// detKind selects how produceDet sets up a determinism case.
type detKind int

const (
	detRaw     detKind = iota // no project/config; raw normalized stdout
	detCache                  // isolated cache only; cache path masked
	detMinimal                // git project + no-op minimal config
	detConfig                 // git project + the case's inline config content
	// detSource is detConfig's auto-discovery counterpart for `source`: the
	// config must be discovered at the git root (source refuses an implicit
	// fallback), PATH/SHELL are pinned because shadow detection reads them, and
	// the per-root farm fingerprint is masked on top of the path masks.
	detSource
)

// detCases are the dynamic-output invocations whose stability is most at risk
// (durations, timings, machine-dependent paths) and therefore the meaningful
// targets of a determinism check. produceDet renders each into normalized output
// from a fresh, isolated setup; TestGoldenSuiteDeterministic runs each twice and
// asserts the two are byte-identical. (Static help/JSON-shape goldens are stable
// by construction; the exhaustive form of this check is `go test ./test/cli/
// -count=2`, which the repo runs per task.)
var detCases = []struct {
	name   string
	kind   detKind
	config string // inline config content, for detConfig cases
	args   []string
}{
	{name: "version", kind: detRaw, args: []string{"version"}},
	{name: "cache-path", kind: detCache, args: []string{"cache", "path"}},
	{name: "config-show", kind: detMinimal, args: []string{"config", "show"}},
	{name: "config-runtime", kind: detMinimal, args: []string{"config", "runtime"}},
	{name: "init-dry-run", kind: detMinimal, args: []string{"init", "--dry-run"}},
	{name: "setup-dry-run", kind: detMinimal, args: []string{"setup", "--dry-run"}},
	{name: "check-explain-json", kind: detConfig, config: explainToolsConfigJS, args: []string{"check", "--explain=json"}},
	{name: "fix-explain-json", kind: detConfig, config: explainToolsConfigJS, args: []string{"fix", "--explain=json"}},
	{name: "lint-explain-json", kind: detConfig, config: explainToolsConfigJS, args: []string{"lint", "--explain=json"}},
	{name: "exec-list", kind: detConfig, config: appsListConfigJS, args: []string{"exec"}},
	{name: "devtools-apps-list", kind: detConfig, config: appsListConfigJS, args: []string{"devtools", "apps", "list"}},
	{name: "source-bash", kind: detSource, config: sourceAutoConfigJS, args: []string{"source", "bash"}},
	{name: "source-fish", kind: detSource, config: sourceAutoConfigJS, args: []string{"source", "fish"}},
	{name: "source-status", kind: detSource, config: sourceAutoConfigJS, args: []string{"source", "status"}},
	{name: "source-status-json", kind: detSource, config: sourceAutoConfigJS, args: []string{"source", "status", "--json"}},
}

// TestGoldenSuiteDeterministic runs each dynamic-output invocation twice, in
// independent isolated environments, and asserts the normalized results are
// byte-identical — proving the contract output (and the normalizers that mask its
// timing/path nondeterminism) is reproducible run-to-run.
func TestGoldenSuiteDeterministic(t *testing.T) {
	for _, tc := range detCases {
		t.Run(tc.name, func(t *testing.T) {
			first := produceDet(t, tc.kind, tc.config, tc.args...)
			second := produceDet(t, tc.kind, tc.config, tc.args...)
			if first != second {
				t.Errorf("`%s` is not byte-stable across two runs\n--- run 1 ---\n%s\n--- run 2 ---\n%s",
					tc.name, first, second)
			}
		})
	}
}

// produceDet renders one determinism case into normalized output from a fresh,
// isolated setup. Project and cache paths (which differ between independent runs)
// are masked, so two invocations of the same case must yield identical strings if
// the underlying command output is deterministic.
func produceDet(t *testing.T, kind detKind, config string, args ...string) string {
	t.Helper()
	cacheBase := t.TempDir()

	switch kind {
	case detRaw:
		res := clitest.Run(t, clitest.RunOptions{}, args...)
		return clitest.NewNormalizer().Apply(res.Stdout)
	case detCache:
		norm := clitest.NewNormalizer().MaskPath(cacheBase, "<CACHE>")
		res := clitest.Run(t, clitest.RunOptions{CacheDir: cacheBase}, args...)
		return norm.Apply(res.Stdout)
	case detMinimal, detConfig:
		p := clitest.NewProject(t)
		var cfg string
		if kind == detMinimal {
			cfg = clitest.WriteMinimalConfig(p)
		} else {
			cfg = p.WriteFile("det.config.js", config)
		}
		norm := clitest.NewNormalizer().MaskPath(p.Dir, "<TMP>").MaskPath(cacheBase, "<CACHE>")
		full := append([]string{"--no-auto-config", "--config", cfg}, args...)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheBase}, full...)
		return norm.Apply(res.Stdout) + "\n===STDERR===\n" + norm.Apply(res.Stderr)
	case detSource:
		p := clitest.NewProject(t)
		p.WriteFile("datamitsu.config.js", config)
		mask := sourceNormalizer(p, cacheBase)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheBase, Env: sourceEnv()}, args...)
		return mask(res.Stdout) + "\n===STDERR===\n" + mask(res.Stderr)
	default:
		t.Fatalf("produceDet: unknown detKind %d", kind)
		return ""
	}
}
