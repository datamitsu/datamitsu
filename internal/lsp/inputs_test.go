package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

// The fingerprint moves with any change to what a load reads — content, a file
// appearing or disappearing — and with nothing else.
func TestInputFingerprint(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "datamitsu.config.js")
	candidate := filepath.Join(dir, "datamitsu.config.ts")
	watch := []string{cfg, candidate, cfg}

	writeFile(t, cfg, "a")
	base := digestInputs(watch).fingerprint()
	if again := digestInputs(watch).fingerprint(); again != base {
		t.Fatal("the fingerprint of unchanged files changed")
	}

	changes := []struct {
		name   string
		change func()
	}{
		{name: "content", change: func() { writeFile(t, cfg, "b") }},
		{name: "a candidate appears", change: func() { writeFile(t, candidate, "") }},
		{name: "a file disappears", change: func() {
			if err := os.Remove(cfg); err != nil {
				t.Fatal(err)
			}
		}},
	}
	seen := map[string]string{base: "initial"}
	for _, c := range changes {
		c.change()
		fp := digestInputs(watch).fingerprint()
		if prev, dup := seen[fp]; dup {
			t.Errorf("after %s the fingerprint equals the one after %s", c.name, prev)
		}
		seen[fp] = c.name
	}
}

func TestDigestFileMarkers(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	writeFile(t, empty, "")

	if got := digestFile(filepath.Join(dir, "missing")); got != digestAbsent {
		t.Errorf("missing file digest = %q, want %q", got, digestAbsent)
	}
	if got := digestFile(empty); got == digestAbsent || len(got) != 32 {
		t.Errorf("empty file digest = %q, want a content hash distinct from absent", got)
	}
}
