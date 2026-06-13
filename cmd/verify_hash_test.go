package cmd

import (
	"strings"
	"testing"

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
		if err := verifyChainHashes(nil, false); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("noVerify bypasses even on drift", func(t *testing.T) {
		lm := config.SetupLayerMap{
			"x": mkHistory("upstream", "xxh3:1234567890abcdef1234567890abcdef"),
		}
		if err := verifyChainHashes(&lm, true); err != nil {
			t.Fatalf("bypass should return nil, got %v", err)
		}
	})

	t.Run("no pins → nil", func(t *testing.T) {
		lm := config.SetupLayerMap{"x": mkHistory("upstream", "")}
		if err := verifyChainHashes(&lm, false); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("drift → error", func(t *testing.T) {
		lm := config.SetupLayerMap{
			"x": mkHistory("upstream", "xxh3:1234567890abcdef1234567890abcdef"),
		}
		err := verifyChainHashes(&lm, false)
		if err == nil {
			t.Fatal("expected an error on drift")
		}
		if !strings.Contains(err.Error(), "chain-hash verification failed") {
			t.Errorf("unexpected error text: %v", err)
		}
	})
}

func TestFormatChainHashReport(t *testing.T) {
	mismatches := []config.ChainHashMismatch{
		{
			FileName: ".golangci.yaml",
			Expected: "xxh3:1234567890abcdef1234567890abcdef",
			Actual:   "xxh3:0a1b2c3d4e5f60718293a4b5c6d7e8f9",
			Incoming: "version: 2\nlinters:\n  enable: [govet]\n",
		},
	}
	report := formatChainHashReport(mismatches)

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
}
