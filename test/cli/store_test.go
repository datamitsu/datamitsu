package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// expectedStoreSubcommands is the exact set of `store` leaf commands (drift
// guard): a new store subcommand must be added here deliberately, with its own
// blackbox test.
var expectedStoreSubcommands = []string{
	"clear",
	"import",
	"path",
	"refs",
	"seed",
	"status",
}

// TestStoreHelpGolden freezes `store --help`: a static, offline help block with
// no version or path tokens, so the normalized output equals the raw output. The
// subcommand set is additionally compared as a set to decouple the drift guard
// from help formatting.
func TestStoreHelpGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{}, "store", "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`store --help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`store --help` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "store_help", norm.Apply(res.Stdout))

	got := parseAvailableCommands(res.Stdout)
	if strings.Join(sortedCopy(got), ",") != strings.Join(sortedCopy(expectedStoreSubcommands), ",") {
		t.Errorf("store subcommand set drift:\n got: %v\nwant: %v", got, expectedStoreSubcommands)
	}
}

// TestStorePath freezes `store path`: the printed path is the store subdir
// ({base}/store) of DATAMITSU_CACHE_DIR. We pin the base to a known temp dir,
// mask it to <CACHE>, and golden the masked line; an exact equality check
// against the computed path guards the contract independently of the golden.
func TestStorePath(t *testing.T) {
	cacheBase := t.TempDir()
	norm := clitest.NewNormalizer().MaskPath(cacheBase, "<CACHE>")

	res := clitest.Run(t, clitest.RunOptions{CacheDir: cacheBase}, "store", "path")
	if res.ExitCode != 0 {
		t.Fatalf("`store path` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`store path` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "store_path", norm.Apply(res.Stdout))

	wantPath := filepath.Join(cacheBase, "store")
	if got := strings.TrimRight(res.Stdout, "\n"); got != wantPath {
		t.Errorf("`store path` = %q, want %q", got, wantPath)
	}
}

// TestStoreStatusNoOCI locks the offline contract for `store status` (human and
// --json) against the minimal config, which declares no top-level `oci` key.
// BundleStatus refuses to invent a bundle: both forms fail with a non-zero exit
// and an error that names the missing `oci` declaration, never a silent empty
// success. (A populated bundle requires the network and is exercised by the
// gated OCI e2e tier.)
func TestStoreStatusNoOCI(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)

	for _, extra := range [][]string{nil, {"--json"}} {
		args := append([]string{"--no-auto-config", "--config", cfg, "store", "status"}, extra...)
		name := "human"
		if len(extra) > 0 {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, args...)
			if res.ExitCode == 0 {
				t.Fatalf("`store status %s` exit = 0, want non-zero\nstdout:\n%s",
					strings.Join(extra, " "), res.Stdout)
			}
			if !strings.Contains(res.Stderr, "no oci bundle declared") {
				t.Errorf("stderr should name the missing oci declaration:\n%s", res.Stderr)
			}
			if strings.Contains(res.Stderr, "Usage:") {
				t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
			}
		})
	}
}

// TestStoreClear freezes the success path of `store clear` against the isolated
// temp store: it prints the masked "Cleared store" line and actually removes the
// store subtree (a planted sentinel is gone afterward). The store path is the
// harness temp dir, never the developer's real store.
func TestStoreClear(t *testing.T) {
	cacheBase := t.TempDir()
	storeDir := filepath.Join(cacheBase, "store")
	sentinel := filepath.Join(storeDir, "sub", "sentinel.txt")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		t.Fatalf("mkdir store sentinel dir: %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("delete me"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	norm := clitest.NewNormalizer().MaskPath(cacheBase, "<CACHE>")

	res := clitest.Run(t, clitest.RunOptions{CacheDir: cacheBase}, "store", "clear")
	if res.ExitCode != 0 {
		t.Fatalf("`store clear` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	clitest.AssertGolden(t, "store_clear", norm.Apply(res.Stdout))

	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("`store clear` did not remove the store sentinel %s (err=%v)", sentinel, err)
	}
}

// TestStoreClearRefusesDangerousPath locks the safety guard: `store clear`
// refuses when the resolved store path coincides with $HOME (a stand-in for any
// dangerous, non-store path). It exits non-zero with a "refusing to clear
// dangerous path" message and deletes nothing.
func TestStoreClearRefusesDangerousPath(t *testing.T) {
	cacheBase := t.TempDir()
	storeDir := filepath.Join(cacheBase, "store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("mkdir store dir: %v", err)
	}

	// Point HOME at the store path so the dangerous-path guard (storePath ==
	// home) trips without needing a literal "/" or volume root.
	res := clitest.Run(t, clitest.RunOptions{CacheDir: cacheBase, Env: []string{"HOME=" + storeDir}},
		"store", "clear")
	if res.ExitCode == 0 {
		t.Fatalf("`store clear` on a dangerous path exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "refusing to clear dangerous path") {
		t.Errorf("stderr should report the dangerous-path refusal:\n%s", res.Stderr)
	}
	if _, err := os.Stat(storeDir); err != nil {
		t.Errorf("refused clear must not delete the store dir %s: %v", storeDir, err)
	}
}

// TestStoreSeedArgValidation locks the offline argument-validation contract for
// `store seed`: a bare tag is refused without --resolve-tag, an unpinned
// reference with no tag is refused, and no argument against a config without an
// `oci` key is an error. None of these reach the network.
func TestStoreSeedArgValidation(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "bare-tag-without-resolve",
			args: []string{"store", "seed", "ghcr.io/owner/repo:latest"},
			want: "a tag reference does not pin content",
		},
		{
			name: "unpinned-reference",
			args: []string{"store", "seed", "ghcr.io-owner-repo"},
			want: "must be pinned as <ref>@sha256:<digest>",
		},
		{
			name: "no-arg-no-oci",
			args: []string{"store", "seed"},
			want: "no oci bundle declared in the effective config",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--no-auto-config", "--config", cfg}, tc.args...)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, args...)
			if res.ExitCode == 0 {
				t.Fatalf("`%s` exit = 0, want non-zero\nstdout:\n%s",
					strings.Join(tc.args, " "), res.Stdout)
			}
			if !strings.Contains(res.Stderr, tc.want) {
				t.Errorf("stderr should contain %q:\n%s", tc.want, res.Stderr)
			}
			if strings.Contains(res.Stderr, "Usage:") {
				t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
			}
		})
	}
}

// TestStoreImportArgValidation locks the offline contract for `store import`:
// it requires exactly one positional layout dir (ExactArgs(1)), errors when no
// digest can be resolved (no `oci` key and no --digest), and errors on a missing
// layout dir when a digest is supplied. All paths are offline.
func TestStoreImportArgValidation(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	missing := filepath.Join(p.Dir, "no-such-layout")
	const digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "no-arg",
			args: []string{"store", "import"},
			want: "accepts 1 arg(s), received 0",
		},
		{
			name: "missing-digest-no-oci",
			args: []string{"store", "import", missing},
			want: "no oci bundle declared in the effective config",
		},
		{
			name: "missing-layout-dir",
			args: []string{"store", "import", "--digest", digest, missing},
			want: "no such file or directory",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--no-auto-config", "--config", cfg}, tc.args...)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, args...)
			if res.ExitCode == 0 {
				t.Fatalf("`%s` exit = 0, want non-zero\nstdout:\n%s",
					strings.Join(tc.args, " "), res.Stdout)
			}
			if !strings.Contains(res.Stderr, tc.want) {
				t.Errorf("stderr should contain %q:\n%s", tc.want, res.Stderr)
			}
		})
	}
}

// refsConfigJS declares one of everything `store refs` reports: a bundle pin, a
// registry-sourced parser, a url-sourced parser, a per-platform binary app and
// a node app carrying an external archive. It exists to prove the traversal reaches every entity —
// the minimal config would exercise only the empty case.
func refsConfigJS() string {
	return `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  apps: {
    shellcheck: {
      binary: {
        binaries: {
          linux: { amd64: { glibc: { url: "https://example.test/shellcheck-linux-amd64.tar.gz", hash: "` + strings.Repeat("11", 32) + `", contentType: "tar.gz" } } },
          darwin: { arm64: { unknown: { url: "https://example.test/shellcheck-darwin-arm64.tar.gz", hash: "` + strings.Repeat("22", 32) + `", contentType: "tar.gz" } } },
        },
      },
    },
    prettier: {
      node: { packageName: "prettier", version: "3.8.3", binPath: "bin/prettier.cjs", runtime: "node", lockFile: "{}" },
      archives: {
        completions: { url: "https://example.test/prettier-completions.tar.gz", hash: "` + strings.Repeat("33", 32) + `", format: "tar.gz" },
      },
    },
  },
  oci: { ref: "ghcr.io/datamitsu/example-bundle", digest: "sha256:` + strings.Repeat("44", 32) + `" },
  parsers: {
    core: { hash: "` + strings.Repeat("55", 32) + `", oci: { ref: "ghcr.io/datamitsu/datamitsu-parsers", digest: "sha256:` + strings.Repeat("66", 32) + `" } },
    legacy: { hash: "` + strings.Repeat("77", 32) + `", url: "https://example.test/legacy.wasm" },
  },
  runtimes: {
    node: {
      kind: "node",
      mode: "managed",
      node: { nodeVersion: "22.12.0", pnpmVersion: "11.20.0", pnpmHash: "` + strings.Repeat("88", 32) + `" },
      managed: {
        binaries: {
          linux: { amd64: { glibc: { url: "https://example.test/node-linux-amd64.tar.gz", hash: "` + strings.Repeat("99", 32) + `", contentType: "tar.gz", extractDir: true } } },
        },
      },
    },
  },
  setup: {},
  tools: {},
});
globalThis.getMinVersion = () => "0.0.0";
`
}

// TestStoreRefsEmpty pins the "nothing to mirror" case: a config with no oci
// pin and no downloads prints nothing at all and still exits 0, so a mirroring
// loop reading this output degrades to a no-op instead of an error.
func TestStoreRefsEmpty(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "store", "refs")
	if res.ExitCode != 0 {
		t.Fatalf("`store refs` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "" {
		t.Errorf("`store refs` on an empty config printed:\n%s", res.Stdout)
	}
}

// TestStoreRefsPopulated freezes the traversal against a config declaring one
// of every reportable entity, in all three output shapes. The point of the
// goldens is the ORDER as much as the content: the output is meant to be piped
// into a mirroring loop, so it has to be byte-stable across runs.
func TestStoreRefsPopulated(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("refs.config.js", refsConfigJS())
	norm := clitest.NewNormalizer()

	cases := map[string]struct {
		args   []string
		golden string
	}{
		"plain":    {nil, "store_refs"},
		"oci only": {[]string{"--oci-only"}, "store_refs_oci_only"},
		"json":     {[]string{"--json"}, "store_refs_json"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"--no-auto-config", "--config", cfg, "store", "refs"}, tc.args...)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, args...)
			if res.ExitCode != 0 {
				t.Fatalf("`store refs %s` exit = %d, want 0\nstderr:\n%s",
					strings.Join(tc.args, " "), res.ExitCode, res.Stderr)
			}
			if res.Stderr != "" {
				t.Errorf("`store refs` wrote to stderr:\n%s", res.Stderr)
			}
			clitest.AssertGolden(t, tc.golden, norm.Apply(res.Stdout))
		})
	}
}

// TestStoreRefsHelpGolden freezes the leaf help, including the mirroring
// one-liner in the long description: that example is the command's reason to
// exist and should not silently disappear.
func TestStoreRefsHelpGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{}, "store", "refs", "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`store refs --help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	clitest.AssertGolden(t, "store_refs_help", norm.Apply(res.Stdout))
}
