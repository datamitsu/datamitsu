//go:build e2e_oci

package e2e_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// initPlan is the version-stable slice of `init` output used to assert that the
// dry-run plan and the real run agree on what work they describe: the detected
// project-types descriptor and the set of applicable init-command (hook) names.
type initPlan struct {
	projectTypes string
	hooks        []string
}

// stripFrame removes the "┃ " / "┃" body-line frame the ui.Display draws around
// an open phase, returning the inner content (empty for a bare spacer line).
func stripFrame(line string) string {
	switch {
	case line == "┃":
		return ""
	case strings.HasPrefix(line, "┃ "):
		return strings.TrimPrefix(line, "┃ ")
	default:
		return line
	}
}

// extractInitPlan parses the (ANSI-stripped) stdout of an `init` run into the
// stable plan signature shared by the dry-run and the real run. The descriptor
// is the first body line under the "init" phase-open rule; the hooks are the
// tokens on the "hooks" summary line.
func extractInitPlan(stdout string) initPlan {
	plain := clitest.NewNormalizer().Apply(stdout)
	lines := strings.Split(plain, "\n")

	var plan initPlan
	for i, line := range lines {
		if strings.Contains(line, "┏") && strings.Contains(line, "init") {
			for _, next := range lines[i+1:] {
				if body := strings.TrimSpace(stripFrame(next)); body != "" {
					plan.projectTypes = body
					break
				}
			}
			break
		}
	}

	for _, line := range lines {
		body := strings.TrimSpace(stripFrame(line))
		if !strings.HasPrefix(body, "hooks") {
			continue
		}
		for tok := range strings.FieldsSeq(strings.TrimPrefix(body, "hooks")) {
			if tok == "✓" || tok == "✗" {
				continue
			}
			plan.hooks = append(plan.hooks, tok)
		}
		break
	}
	sort.Strings(plan.hooks)
	return plan
}

// datamitsuDirExists reports whether {dir}/.datamitsu exists and is a directory.
func datamitsuDirExists(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".datamitsu"))
	return err == nil && info.IsDir()
}

// TestOCIInitMinimal exercises the real `init` pipeline against the digest-pinned
// bundle (warm from the persistent test cache): a dry-run that makes zero
// filesystem changes, a real run that installs the minimal set and materializes
// the project-local `.datamitsu/` (type defs + any link symlinks) and runs the
// applicable initCommands, and a re-run to prove idempotency. It also asserts
// dry-run / real-run plan parity (same detected project types; the dry-run's
// planned hooks are a subset of the hooks the real run actually executes). Gated
// by e2e_oci + DATAMITSU_TEST_OCI=1.
func TestOCIInitMinimal(t *testing.T) {
	RequireOCIE2E(t)

	// Keep the inherited config intact so init exercises the real install + link +
	// initCommand pipeline. The bundle is seeded from the warm cache, not the network.
	p := newOverlayProject(t, "return config;")
	cacheDir := testCacheDir(t)

	// --- init --dry-run: plan only, zero filesystem effects -----------------
	dry := runOnline(t, p.Dir, cacheDir, "init", "--dry-run")
	if dry.ExitCode != 0 {
		t.Fatalf("init --dry-run: exit %d\nstdout:\n%s\nstderr:\n%s",
			dry.ExitCode, dry.Stdout, dry.Stderr)
	}
	if !strings.Contains(clitest.NewNormalizer().Apply(dry.Stdout), "dry-run") {
		t.Errorf("init --dry-run output missing the dry-run marker\nstdout:\n%s", dry.Stdout)
	}
	if datamitsuDirExists(p.Dir) {
		t.Errorf("init --dry-run created .datamitsu/; dry-run must not touch the filesystem")
	}

	// --- init (real): installs the minimal set + materializes .datamitsu ----
	live := runOnline(t, p.Dir, cacheDir, "init")
	if live.ExitCode != 0 {
		t.Fatalf("init: exit %d\nstdout:\n%s\nstderr:\n%s",
			live.ExitCode, live.Stdout, live.Stderr)
	}
	if !datamitsuDirExists(p.Dir) {
		t.Fatalf("init did not create .datamitsu/ in the project\nstdout:\n%s", live.Stdout)
	}
	// The type-definition pair is always written, with or without link symlinks.
	for _, rel := range []string{
		filepath.Join(".datamitsu", "datamitsu.config.d.ts"),
		filepath.Join(".datamitsu", ".gitignore"),
	} {
		if _, err := os.Stat(filepath.Join(p.Dir, rel)); err != nil {
			t.Errorf("init did not create %s: %v", rel, err)
		}
	}

	// --- dry-run / real parity: same plan -----------------------------------
	dryPlan := extractInitPlan(dry.Stdout)
	realPlan := extractInitPlan(live.Stdout)
	if dryPlan.projectTypes != realPlan.projectTypes {
		t.Errorf("project-types descriptor differs between dry-run and real init:\n  dry  = %q\n  real = %q",
			dryPlan.projectTypes, realPlan.projectTypes)
	}
	// The dry-run plans against the clean tree; the real run may surface more hooks
	// once earlier ones create their `when` targets, but everything the dry-run
	// promised must actually run. Subset, not strict equality, captures that.
	realHooks := map[string]bool{}
	for _, h := range realPlan.hooks {
		realHooks[h] = true
	}
	for _, h := range dryPlan.hooks {
		if !realHooks[h] {
			t.Errorf("init --dry-run planned hook %q that the real run did not execute\n  dry  = %v\n  real = %v",
				h, dryPlan.hooks, realPlan.hooks)
		}
	}

	// --- re-running init is idempotent (warm cache, links rebuilt) ----------
	again := runOnline(t, p.Dir, cacheDir, "init")
	if again.ExitCode != 0 {
		t.Fatalf("second init: exit %d\nstdout:\n%s\nstderr:\n%s",
			again.ExitCode, again.Stdout, again.Stderr)
	}
	if !datamitsuDirExists(p.Dir) {
		t.Errorf("second init removed .datamitsu/")
	}
}
