//go:build e2e_oci

package e2e_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// ociStatus mirrors the JSON shape emitted by `store status --json`
// (internal/ocibundle.Status). Only the fields the contract pins are decoded.
type ociStatus struct {
	Ref      string `json:"ref"`
	Digest   string `json:"digest"`
	Seeded   bool   `json:"fullySeeded"`
	Selected string `json:"selected"`
	Layers   []struct {
		Subtree string `json:"subtree"`
		Present bool   `json:"present"`
	} `json:"layers"`
	Apps []struct {
		App     string `json:"app"`
		Covered bool   `json:"covered"`
		Present bool   `json:"present"`
	} `json:"apps"`
}

// ociRef is the subset of `config show` we read to learn the bundle the overlay
// inherits, so the seed/status assertions can be cross-checked against it.
type ociRef struct {
	OCI *struct {
		Ref    string `json:"ref"`
		Digest string `json:"digest"`
	} `json:"oci"`
}

// declaredOCI reads the effective `oci` declaration from `config show` so tests
// can assert that seed/status report exactly the bundle the config pins.
func declaredOCI(t *testing.T, p *clitest.Project, cacheDir string) (ref, digest string) {
	t.Helper()
	res := runOnline(t, p.Dir, cacheDir, "config", "show")
	if res.ExitCode != 0 {
		t.Fatalf("config show: exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	var cfg ociRef
	if err := json.Unmarshal([]byte(res.Stdout), &cfg); err != nil {
		t.Fatalf("config show did not emit valid JSON: %v\nstdout:\n%s", err, res.Stdout)
	}
	if cfg.OCI == nil {
		t.Fatalf("config show has no `oci` declaration; vendored config did not merge\nstdout:\n%s", res.Stdout)
	}
	if cfg.OCI.Ref == "" || !strings.HasPrefix(cfg.OCI.Digest, "sha256:") {
		t.Fatalf("declared oci is not digest-pinned: ref=%q digest=%q", cfg.OCI.Ref, cfg.OCI.Digest)
	}
	return cfg.OCI.Ref, cfg.OCI.Digest
}

// TestOCIStoreSeedAndStatus exercises the real OCI pull pipeline: it seeds the
// digest-pinned bundle from the config's `oci` key into the persistent test
// cache, then asserts `store status --json` reports that bundle as fully seeded
// with covered apps, and finally proves a re-seed is idempotent (dedup from the
// warm cache). It is gated by the e2e_oci tag + DATAMITSU_TEST_OCI=1.
func TestOCIStoreSeedAndStatus(t *testing.T) {
	RequireOCIE2E(t)

	// Keep the inherited config intact (return config) so `store status` reports
	// the real app coverage of the bundle, not a trimmed set.
	p := newOverlayProject(t, "return config;")
	cacheDir := testCacheDir(t)

	wantRef, wantDigest := declaredOCI(t, p, cacheDir)

	// --- store seed (no arg → config oci) ---------------------------------
	seed := runOnline(t, p.Dir, cacheDir, "store", "seed")
	if seed.ExitCode != 0 {
		t.Fatalf("store seed: exit %d\nstdout:\n%s\nstderr:\n%s",
			seed.ExitCode, seed.Stdout, seed.Stderr)
	}
	wantLine := "Seeded store from " + wantRef + "@" + wantDigest
	if !strings.Contains(seed.Stdout, wantLine) {
		t.Errorf("store seed stdout missing %q\ngot:\n%s", wantLine, seed.Stdout)
	}

	// --- store status --json ----------------------------------------------
	status := runOnline(t, p.Dir, cacheDir, "store", "status", "--json")
	if status.ExitCode != 0 {
		t.Fatalf("store status --json: exit %d\nstdout:\n%s\nstderr:\n%s",
			status.ExitCode, status.Stdout, status.Stderr)
	}
	var st ociStatus
	if err := json.Unmarshal([]byte(status.Stdout), &st); err != nil {
		t.Fatalf("store status --json did not emit valid JSON: %v\nstdout:\n%s", err, status.Stdout)
	}
	if st.Ref != wantRef {
		t.Errorf("status ref = %q, want %q", st.Ref, wantRef)
	}
	if st.Digest != wantDigest {
		t.Errorf("status digest = %q, want %q", st.Digest, wantDigest)
	}
	if !st.Seeded {
		t.Errorf("status fullySeeded = false after store seed; expected true\nstdout:\n%s", status.Stdout)
	}
	if st.Selected == "" {
		t.Errorf("status selected platform is empty; bundle has no entry for this host\nstdout:\n%s", status.Stdout)
	}
	if len(st.Layers) == 0 {
		t.Fatalf("status reports zero layers\nstdout:\n%s", status.Stdout)
	}
	for i, l := range st.Layers {
		if !l.Present {
			t.Errorf("layer %d (%s) not present in store after a full seed", i, l.Subtree)
		}
	}
	// A full seed must cover every configured app the bundle declares.
	covered := 0
	for _, a := range st.Apps {
		if a.Covered {
			covered++
		}
	}
	if len(st.Apps) > 0 && covered == 0 {
		t.Errorf("no apps reported as covered after a full seed (apps=%d)", len(st.Apps))
	}

	// --- re-seed is idempotent (dedup from the warm cache) ----------------
	reseed := runOnline(t, p.Dir, cacheDir, "store", "seed")
	if reseed.ExitCode != 0 {
		t.Fatalf("re-seed: exit %d\nstdout:\n%s\nstderr:\n%s",
			reseed.ExitCode, reseed.Stdout, reseed.Stderr)
	}
	if !strings.Contains(reseed.Stdout, wantLine) {
		t.Errorf("re-seed stdout missing %q\ngot:\n%s", wantLine, reseed.Stdout)
	}

	// Status after re-seed is unchanged: same bundle, still fully seeded.
	status2 := runOnline(t, p.Dir, cacheDir, "store", "status", "--json")
	if status2.ExitCode != 0 {
		t.Fatalf("store status --json (post re-seed): exit %d\nstderr:\n%s", status2.ExitCode, status2.Stderr)
	}
	var st2 ociStatus
	if err := json.Unmarshal([]byte(status2.Stdout), &st2); err != nil {
		t.Fatalf("post re-seed status not valid JSON: %v\nstdout:\n%s", err, status2.Stdout)
	}
	if st2.Digest != wantDigest || !st2.Seeded {
		t.Errorf("post re-seed status drifted: digest=%q seeded=%v (want %q / true)",
			st2.Digest, st2.Seeded, wantDigest)
	}
}
