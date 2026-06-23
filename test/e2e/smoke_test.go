//go:build e2e_oci

package e2e_test

import (
	"encoding/json"
	"testing"
)

// TestOCISmoke is the scaffolding smoke test for the OCI e2e tier. It does not
// touch the network: it only proves the gating, the vendored config, and the
// overlay-inheritance plumbing work end-to-end by loading the digest-pinned
// config through an overlay and rendering it with `config show`. The heavier
// seed/install/exec/init/check tests in later tasks build on this scaffolding.
func TestOCISmoke(t *testing.T) {
	RequireOCIE2E(t)

	// Inherit the vendored OCI config but trim all tools, keeping the rest.
	p := newOverlayProject(t, "return { ...config, tools: {} };")

	res := runOnline(t, p.Dir, testCacheDir(t), "config", "show")
	if res.ExitCode != 0 {
		t.Fatalf("config show: exit %d\nstdout:\n%s\nstderr:\n%s",
			res.ExitCode, res.Stdout, res.Stderr)
	}

	// The rendered config must be a single valid JSON object — proof the vendored
	// config parsed and the overlay merged cleanly.
	var cfg map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &cfg); err != nil {
		t.Fatalf("config show did not emit valid JSON: %v\nstdout:\n%s", err, res.Stdout)
	}
	if _, ok := cfg["apps"]; !ok {
		t.Errorf("config show output missing expected \"apps\" key; got keys %v", keysOf(cfg))
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
