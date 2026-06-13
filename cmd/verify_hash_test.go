package cmd

import (
	"strings"
	"testing"

	"github.com/fatih/color"

	"github.com/datamitsu/datamitsu/internal/config"
)

// mkHistory builds a two-layer history (one upstream layer + a root layer that
// pins `pin`), so the incoming content is `upstream` and the root layer's own
// output is excluded from the hash.
func mkHistory(upstream, pin string) *config.SetupLayerHistory {
	up := upstream
	out := "root-output"
	return &config.SetupLayerHistory{
		FinalConfig: config.ConfigSetup{ExpectChainHash: pin},
		Layers: []config.SetupLayerEntry{
			{LayerName: "base", GeneratedContent: &up},
			{LayerName: "root", GeneratedContent: &out},
		},
	}
}

func TestVerifyChainHashes_Gate(t *testing.T) {
	t.Run("nil layerMap is a no-op", func(t *testing.T) {
		if err := verifyChainHashes(nil, false, true); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("noVerify bypasses even on drift", func(t *testing.T) {
		lm := config.SetupLayerMap{
			"x": mkHistory("upstream", "xxh3:1234567890abcdef1234567890abcdef"),
		}
		if err := verifyChainHashes(&lm, true, true); err != nil {
			t.Fatalf("bypass should return nil, got %v", err)
		}
	})

	t.Run("no pins → nil", func(t *testing.T) {
		lm := config.SetupLayerMap{"x": mkHistory("upstream", "")}
		if err := verifyChainHashes(&lm, false, true); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("drift → error", func(t *testing.T) {
		lm := config.SetupLayerMap{
			"x": mkHistory("upstream", "xxh3:1234567890abcdef1234567890abcdef"),
		}
		err := verifyChainHashes(&lm, false, true)
		if err == nil {
			t.Fatal("expected an error on drift")
		}
		if !strings.Contains(err.Error(), "chain-hash verification failed") {
			t.Errorf("unexpected error text: %v", err)
		}
	})
}

func TestFormatChainHashReport(t *testing.T) {
	t.Run("no on-disk file → full incoming dump", func(t *testing.T) {
		mismatches := []config.ChainHashMismatch{{
			FileName: ".golangci.yaml",
			Expected: "xxh3:1234567890abcdef1234567890abcdef",
			Actual:   "xxh3:0a1b2c3d4e5f60718293a4b5c6d7e8f9",
			Incoming: "version: 2\nlinters:\n  enable: [govet]\n",
			OnDisk:   nil,
		}}
		report := formatChainHashReport(mismatches, true)

		wants := []string{
			"chain-hash verification failed for 1 config file(s)",
			".golangci.yaml",
			"xxh3:1234567890abcdef1234567890abcdef", // expected
			"xxh3:0a1b2c3d4e5f60718293a4b5c6d7e8f9", // actual
			"incoming content",
			"enable: [govet]",  // a verbatim line of the incoming content
			"--no-verify-hash", // bypass guidance
		}
		for _, w := range wants {
			if !strings.Contains(report, w) {
				t.Errorf("report missing %q\n---\n%s", w, report)
			}
		}
	})

	t.Run("on-disk file + showDiff → unified diff", func(t *testing.T) {
		onDisk := "version: 2\nlinters:\n  enable: [govet]\n"
		mismatches := []config.ChainHashMismatch{{
			FileName: ".golangci.yaml",
			Expected: "xxh3:1234567890abcdef1234567890abcdef",
			Actual:   "xxh3:0a1b2c3d4e5f60718293a4b5c6d7e8f9",
			Incoming: "version: 2\nlinters:\n  enable: [govet, errcheck]\n",
			OnDisk:   &onDisk,
		}}
		report := formatChainHashReport(mismatches, true)

		for _, w := range []string{
			"diff (on-disk → incoming):",
			"-  enable: [govet]",           // removed line
			"+  enable: [govet, errcheck]", // added line
			"--no-hash-diff",               // guidance for disabling the diff
		} {
			if !strings.Contains(report, w) {
				t.Errorf("diff report missing %q\n---\n%s", w, report)
			}
		}
		if strings.Contains(report, "incoming content (what reached") {
			t.Errorf("diff path should not also dump full incoming content\n---\n%s", report)
		}
	})

	t.Run("showDiff=false → full dump even with on-disk file", func(t *testing.T) {
		onDisk := "a: 1\n"
		mismatches := []config.ChainHashMismatch{{
			FileName: "x.yaml",
			Expected: "xxh3:1111111111111111111111111111111",
			Actual:   "xxh3:2222222222222222222222222222222",
			Incoming: "a: 2\n",
			OnDisk:   &onDisk,
		}}
		report := formatChainHashReport(mismatches, false)
		if strings.Contains(report, "diff (on-disk") {
			t.Errorf("--no-hash-diff should suppress the diff\n---\n%s", report)
		}
		if !strings.Contains(report, "incoming content") {
			t.Errorf("expected full incoming dump\n---\n%s", report)
		}
	})

	t.Run("colors lines by semantics, not by leading character", func(t *testing.T) {
		// Force colors on so we can assert the SGR codes. A deleted YAML document
		// separator renders as "----": the prefix-parsing approach would mistake
		// it for a "---" file header (bold); the structured renderer must tint it
		// red because its OpKind is Delete.
		color.NoColor = false
		t.Cleanup(func() { color.NoColor = true })

		onDisk := "---\nkeep: 1\n"
		incoming := "keep: 1\nadd: 2\n"
		report := formatChainHashReport([]config.ChainHashMismatch{{
			FileName: "x.yaml",
			Expected: "xxh3:1111111111111111111111111111111",
			Actual:   "xxh3:2222222222222222222222222222222",
			Incoming: incoming,
			OnDisk:   &onDisk,
		}}, true)

		const (
			red  = "\x1b[31m"
			bold = "\x1b[1m"
		)
		if !strings.Contains(report, red+"----") {
			t.Errorf("deleted '---' line should be red, got:\n%q", report)
		}
		if strings.Contains(report, bold+"----") {
			t.Errorf("deleted '---' line must not be colored as a bold header:\n%q", report)
		}
	})

	t.Run("identical on-disk and incoming → falls back to dump", func(t *testing.T) {
		same := "a: 1\n"
		mismatches := []config.ChainHashMismatch{{
			FileName: "x.yaml",
			Expected: "xxh3:1111111111111111111111111111111",
			Actual:   "xxh3:2222222222222222222222222222222",
			Incoming: same,
			OnDisk:   &same,
		}}
		report := formatChainHashReport(mismatches, true)
		if strings.Contains(report, "diff (on-disk") {
			t.Errorf("no diff expected when sides are identical\n---\n%s", report)
		}
	})
}
