//go:build e2e_oci

package e2e_test

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// appMeta is the slice of an app's `config show` definition this tier needs to
// pick an install target and run its version check: whether it is a binary app
// and how its version is verified.
type appMeta struct {
	Binary       *json.RawMessage `json:"binary"`
	Shell        *json.RawMessage `json:"shell"`
	VersionCheck *struct {
		Disabled bool     `json:"disabled"`
		Args     []string `json:"args"`
	} `json:"versionCheck"`
}

// isBinary reports whether the app is a downloaded binary (not a shell alias),
// the only kind this test installs directly.
func (m appMeta) isBinary() bool { return m.Binary != nil && m.Shell == nil }

// verifiable reports whether the app has a runnable version check.
func (m appMeta) verifiable() bool {
	return m.VersionCheck == nil || !m.VersionCheck.Disabled
}

// versionArgs returns the args to run for the app's version check, mirroring the
// install verify default: VersionCheck.Args if set, otherwise "--version".
func (m appMeta) versionArgs() []string {
	if m.VersionCheck != nil && len(m.VersionCheck.Args) > 0 {
		return m.VersionCheck.Args
	}
	return []string{"--version"}
}

// configApps decodes the `apps` map from `config show` so the test can inspect
// each app's kind and version check.
func configApps(t *testing.T, p *clitest.Project, cacheDir string) map[string]appMeta {
	t.Helper()
	res := runOnline(t, p.Dir, cacheDir, "config", "show")
	if res.ExitCode != 0 {
		t.Fatalf("config show: exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	var cfg struct {
		Apps map[string]appMeta `json:"apps"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &cfg); err != nil {
		t.Fatalf("config show did not emit valid JSON: %v\nstdout:\n%s", err, res.Stdout)
	}
	if len(cfg.Apps) == 0 {
		t.Fatalf("config show reports no apps; vendored config did not merge\nstdout:\n%s", res.Stdout)
	}
	return cfg.Apps
}

// statusJSON runs `store status --json` and decodes it.
func statusJSON(t *testing.T, p *clitest.Project, cacheDir string) ociStatus {
	t.Helper()
	res := runOnline(t, p.Dir, cacheDir, "store", "status", "--json")
	if res.ExitCode != 0 {
		t.Fatalf("store status --json: exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	var st ociStatus
	if err := json.Unmarshal([]byte(res.Stdout), &st); err != nil {
		t.Fatalf("store status --json not valid JSON: %v\nstdout:\n%s", err, res.Stdout)
	}
	return st
}

// discoverInstallTarget picks a minimal, host-supported binary app to install.
// It prefers known-small tools (so the first cold run stays fast) and requires
// the app to be covered by the bundle for this host (guaranteeing it both
// resolves for this platform and seeds from the cache rather than the network).
// It falls back to the first covered binary app alphabetically; if none exists
// the test is skipped rather than failed (the bundle is the variable here).
func discoverInstallTarget(t *testing.T, p *clitest.Project, cacheDir string) (name string, versionArgs []string) {
	t.Helper()
	apps := configApps(t, p, cacheDir)

	covered := map[string]bool{}
	for _, a := range statusJSON(t, p, cacheDir).Apps {
		if a.Covered {
			covered[a.App] = true
		}
	}

	pick := func(n string) (appMeta, bool) {
		m, ok := apps[n]
		if !ok || !m.isBinary() || !m.verifiable() || !covered[n] {
			return appMeta{}, false
		}
		return m, true
	}

	// Prefer small, fast, single-binary tools known to live in the bundle.
	for _, n := range []string{
		"shellcheck", "hadolint", "shfmt", "yamlfmt", "typos",
		"actionlint", "jq", "yq", "gitleaks", "lefthook",
	} {
		if m, ok := pick(n); ok {
			return n, m.versionArgs()
		}
	}

	// Fallback: first covered binary app, in a deterministic order.
	names := make([]string, 0, len(apps))
	for n := range apps {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if m, ok := pick(n); ok {
			return n, m.versionArgs()
		}
	}

	t.Skip("no covered binary app with a version check found in the bundle")
	return "", nil
}

// storePath returns the absolute store directory the binary reports for this
// run's isolated cache, so the test can assert install actually wrote files.
func storePath(t *testing.T, p *clitest.Project, cacheDir string) string {
	t.Helper()
	res := runOnline(t, p.Dir, cacheDir, "store", "path")
	if res.ExitCode != 0 {
		t.Fatalf("store path: exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	return strings.TrimSpace(res.Stdout)
}

// dirHasEntries reports whether dir exists and contains at least one entry.
func dirHasEntries(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// TestOCIInstallAndExec exercises the real install + exec pipeline: it installs
// a minimal binary app from the digest-pinned bundle, asserts it materializes
// into the store, runs its version check through `exec`, and confirms `exec`
// with no args lists the now-installed tool. Gated by e2e_oci + DATAMITSU_TEST_OCI=1.
func TestOCIInstallAndExec(t *testing.T) {
	RequireOCIE2E(t)

	// Keep the inherited config intact so the real app registry is present.
	p := newOverlayProject(t, "return config;")
	cacheDir := testCacheDir(t)

	app, versionArgs := discoverInstallTarget(t, p, cacheDir)
	t.Logf("install target: %s (version check: %v)", app, versionArgs)

	// --- install materializes the app into the store -----------------------
	// Default verification (the post-install version check) stays on, so a
	// broken install fails here rather than silently.
	install := runOnline(t, p.Dir, cacheDir, "install", app)
	if install.ExitCode != 0 {
		t.Fatalf("install %s: exit %d\nstdout:\n%s\nstderr:\n%s",
			app, install.ExitCode, install.Stdout, install.Stderr)
	}

	if !dirHasEntries(t, storePath(t, p, cacheDir)) {
		t.Errorf("store path is empty after installing %s; nothing materialized", app)
	}

	// status must now report the installed app as present in the store.
	present := false
	for _, a := range statusJSON(t, p, cacheDir).Apps {
		if a.App == app {
			present = a.Present
			break
		}
	}
	if !present {
		t.Errorf("store status does not report %s as present after install", app)
	}

	// --- exec <app> -- <versionArgs> runs the real tool --------------------
	runArgs := append([]string{"exec", app, "--"}, versionArgs...)
	ver := runOnline(t, p.Dir, cacheDir, runArgs...)
	if ver.ExitCode != 0 {
		t.Fatalf("exec %s %v: exit %d\nstdout:\n%s\nstderr:\n%s",
			app, versionArgs, ver.ExitCode, ver.Stdout, ver.Stderr)
	}
	// Version output goes to stdout or stderr depending on the tool; assert the
	// combined output is non-empty (a real tool printed something).
	if strings.TrimSpace(ver.Stdout+ver.Stderr) == "" {
		t.Errorf("exec %s version check produced no output", app)
	}

	// --- exec with no args lists the now-installed tool --------------------
	list := runOnline(t, p.Dir, cacheDir, "exec")
	if list.ExitCode != 0 {
		t.Fatalf("exec (list): exit %d\nstderr:\n%s", list.ExitCode, list.Stderr)
	}
	if !strings.Contains(list.Stdout, "Available tools:") {
		t.Errorf("exec listing missing header\nstdout:\n%s", list.Stdout)
	}
	if !strings.Contains(list.Stdout, app) {
		t.Errorf("exec listing does not include installed tool %q\nstdout:\n%s", app, list.Stdout)
	}
}
