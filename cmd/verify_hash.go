package cmd

import (
	"fmt"
	"os"
	"strings"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/ui"
)

// verifyChainHashes runs the root-layer expectChainHash gate over the layer map.
// On any mismatch it prints a full drift report to stderr and returns an error so
// the caller aborts before writing anything. noVerify bypasses the gate entirely
// (the --no-verify-hash flag); a nil layerMap is a no-op.
func verifyChainHashes(layerMap *config.SetupLayerMap, noVerify bool) error {
	if noVerify || layerMap == nil {
		return nil
	}

	mismatches := config.VerifyChainHashes(*layerMap)
	if len(mismatches) == 0 {
		return nil
	}

	// The human drift report is suppressed in JSON-L mode (it would be a non-JSON
	// block on the typed stderr stream); the returned error still carries the
	// failure, which Execute emits as a typed error event.
	if !ui.Quiet() {
		fmt.Fprint(os.Stderr, formatChainHashReport(mismatches))
	}
	return fmt.Errorf("chain-hash verification failed for %d config file(s)", len(mismatches))
}

// formatChainHashReport renders the drift report shown when one or more pinned
// configs diverge from their upstream chain: per file the expected/actual hash
// and the full incoming content (what reached the root layer), followed by
// guidance. Kept pure (no I/O) so it is unit-testable.
func formatChainHashReport(mismatches []config.ChainHashMismatch) string {
	rule := strings.Repeat("─", 60)
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s\n\n",
		clr.Bold(clr.Red(fmt.Sprintf("chain-hash verification failed for %d config file(s)", len(mismatches)))))
	for _, m := range mismatches {
		fmt.Fprintf(&b, "  %s\n", clr.Bold(m.FileName))
		fmt.Fprintf(&b, "    expected: %s\n", m.Expected)
		fmt.Fprintf(&b, "    actual:   %s\n", m.Actual)
		fmt.Fprintf(&b, "    %s\n", clr.Faint("incoming content (what reached the root config layer):"))
		fmt.Fprintf(&b, "    %s\n", clr.Faint(rule))
		for line := range strings.SplitSeq(m.Incoming, "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
		fmt.Fprintf(&b, "    %s\n\n", clr.Faint(rule))
	}
	fmt.Fprintf(&b, "%s\n", clr.Faint("The upstream config feeding these files changed. Review the incoming content"))
	fmt.Fprintf(&b, "%s\n", clr.Faint("against your overrides, update the pinned expectChainHash, then re-run."))
	fmt.Fprintf(&b, "%s\n\n", clr.Faint("Pass --no-verify-hash to bypass this check and write anyway."))
	return b.String()
}
