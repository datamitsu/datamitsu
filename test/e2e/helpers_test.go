//go:build e2e_oci

package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
	e2e "github.com/datamitsu/datamitsu/test/e2e"
)

// RequireOCIE2E is the second gate on the OCI tier. The `//go:build e2e_oci`
// build tag keeps these files out of the default build entirely; this env check
// means an accidental `-tags e2e_oci` run without explicit opt-in still skips,
// so the network-dependent tests never run unintentionally.
func RequireOCIE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("DATAMITSU_TEST_OCI") != "1" {
		t.Skip("OCI e2e tier disabled; set DATAMITSU_TEST_OCI=1 (and use -tags e2e_oci) to enable")
	}
}

// vendoredConfigPath returns the absolute path to the vendored, digest-pinned
// OCI config in testdata. It fails the test if the fixture is missing.
func vendoredConfigPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(e2e.VendoredConfigRelPath)
	if err != nil {
		t.Fatalf("e2e: resolve vendored config path: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("e2e: vendored OCI config missing at %s (re-download from %s): %v",
			abs, e2e.OCIConfigSource, err)
	}
	return abs
}

// testCacheDir returns a persistent, reusable cache base so the digest-pinned
// bundle is fetched once and deduped across runs. DATAMITSU_TEST_CACHE pins it
// (it may point at a warm or even the real cache); otherwise it defaults to a
// stable per-user path under the OS temp dir — deliberately NOT a wiped
// t.TempDir, so re-runs are fast.
func testCacheDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("DATAMITSU_TEST_CACHE")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "datamitsu-e2e-cache")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("e2e: create test cache dir %s: %v", dir, err)
	}
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("e2e: eval symlinks for cache dir %s: %v", dir, err)
	}
	return abs
}

// onlineEnv builds the subprocess environment for the OCI tier: it starts from
// the hermetic clitest.BaseEnv (clean DATAMITSU_*, NO_COLOR, shared GOCOVERDIR,
// isolated cache) but REMOVES the offline / no-oci flags, because this tier
// genuinely needs network and OCI access. Note BaseEnv sets DATAMITSU_OFFLINE /
// DATAMITSU_NO_OCI to a non-empty value and the binary treats any non-empty
// value as "on", so they must be dropped entirely rather than set to "0".
func onlineEnv(cacheDir string, extra ...string) []string {
	base := clitest.BaseEnv(cacheDir)
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if key == "DATAMITSU_OFFLINE" || key == "DATAMITSU_NO_OCI" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

// runOnline executes the build-once instrumented binary online (network + OCI
// enabled), in dir, with the given args. Coverage still flows into the shared
// GOCOVERDIR. It mirrors clitest.Run but with the online environment; the OCI
// tier cannot use clitest.Run directly because that forces offline mode.
func runOnline(t *testing.T, dir, cacheDir string, args ...string) clitest.Result {
	t.Helper()
	bin := clitest.BuildOnce(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// bin is the harness-built binary and args come from test code.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = onlineEnv(cacheDir)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("e2e: `datamitsu %s` timed out\n--- stdout ---\n%s\n--- stderr ---\n%s",
			strings.Join(args, " "), stdout.String(), stderr.String())
	}
	return clitest.Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: clitest.ExitCodeOf(err),
		Err:      err,
	}
}

// newOverlayProject creates a git-initialized temp project whose
// auto-discovered datamitsu.config.js inherits the vendored, digest-pinned OCI
// config via getBeforeConfigs and trims the tool/app set to a minimal one via
// mutateJS (the body of getConfig(config), which must `return` a config object).
// Pass "return { ...config, tools: {} };" to disable all tools, for example.
func newOverlayProject(t *testing.T, mutateJS string) *clitest.Project {
	t.Helper()
	p := clitest.NewProject(t)
	clitest.WriteOverlayConfig(p, vendoredConfigPath(t), mutateJS)
	return p
}
