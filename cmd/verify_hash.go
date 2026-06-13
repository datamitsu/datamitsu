package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/aymanbagabas/go-udiff"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
)

// diffContextLines is how many unchanged lines surround each change in the drift
// diff. Matches go-udiff's own default.
const diffContextLines = 3

// verifyChainHashes runs the root-layer expectChainHash gate over the layer map.
// On any mismatch it prints a full drift report to stderr and returns an error so
// the caller aborts before writing anything. noVerify bypasses the gate entirely
// (the --no-verify-hash flag); a nil layerMap is a no-op. showDiff renders each
// mismatch as a unified diff of the on-disk file against the incoming content;
// when false (the --no-hash-diff flag) the full incoming content is dumped.
func verifyChainHashes(layerMap *config.SetupLayerMap, noVerify, showDiff bool) error {
	if noVerify || layerMap == nil {
		return nil
	}

	mismatches := config.VerifyChainHashes(*layerMap)
	if len(mismatches) == 0 {
		return nil
	}

	fmt.Fprint(os.Stderr, formatChainHashReport(mismatches, showDiff))
	return fmt.Errorf("chain-hash verification failed for %d config file(s)", len(mismatches))
}

// formatChainHashReport renders the drift report shown when one or more pinned
// configs diverge from their upstream chain: per file the expected/actual hash
// and either a colored unified diff (on-disk -> incoming) or, when showDiff is
// false or no diff is available, the full incoming content, followed by guidance.
// Kept pure (no I/O) so it is unit-testable.
func formatChainHashReport(mismatches []config.ChainHashMismatch, showDiff bool) string {
	rule := strings.Repeat("─", 60)
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s\n\n",
		clr.Bold(clr.Red(fmt.Sprintf("chain-hash verification failed for %d config file(s)", len(mismatches)))))
	for _, m := range mismatches {
		fmt.Fprintf(&b, "  %s\n", clr.Bold(m.FileName))
		fmt.Fprintf(&b, "    expected: %s\n", m.Expected)
		fmt.Fprintf(&b, "    actual:   %s\n", m.Actual)

		if diff, ok := chainHashDiff(m, showDiff); ok {
			fmt.Fprintf(&b, "    %s\n", clr.Faint("diff (on-disk → incoming):"))
			fmt.Fprintf(&b, "    %s\n", clr.Faint(rule))
			for line := range strings.SplitSeq(strings.TrimRight(diff, "\n"), "\n") {
				fmt.Fprintf(&b, "    %s\n", line)
			}
			fmt.Fprintf(&b, "    %s\n\n", clr.Faint(rule))
			continue
		}

		fmt.Fprintf(&b, "    %s\n", clr.Faint("incoming content (what reached the root config layer):"))
		fmt.Fprintf(&b, "    %s\n", clr.Faint(rule))
		for line := range strings.SplitSeq(m.Incoming, "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
		fmt.Fprintf(&b, "    %s\n\n", clr.Faint(rule))
	}
	fmt.Fprintf(&b, "%s\n", clr.Faint("The upstream config feeding these files changed. Review the incoming content"))
	fmt.Fprintf(&b, "%s\n", clr.Faint("against your overrides, update the pinned expectChainHash, then re-run."))
	fmt.Fprintf(&b, "%s\n", clr.Faint("Pass --no-hash-diff to see the full incoming content instead of a diff."))
	fmt.Fprintf(&b, "%s\n\n", clr.Faint("Pass --no-verify-hash to bypass this check and write anyway."))
	return b.String()
}

// chainHashDiff returns a colored unified diff of the on-disk file against the
// incoming content. ok is false when diffing is disabled, the file is absent on
// disk, or the two sides are textually identical — the caller then falls back to
// dumping the full incoming content.
func chainHashDiff(m config.ChainHashMismatch, showDiff bool) (string, bool) {
	if !showDiff || m.OnDisk == nil || *m.OnDisk == m.Incoming {
		return "", false
	}
	return coloredUnifiedDiff("on-disk", "incoming", *m.OnDisk, m.Incoming, diffContextLines)
}

// coloredUnifiedDiff builds a unified diff via go-udiff's structured API and tints
// each line by its semantic operation (deletions red, insertions green, hunk
// headers cyan, file headers bold) rather than by parsing leading +/- characters,
// so content lines that themselves begin with +, -, or @@ are never miscolored.
// Colors are a no-op when disabled. ok is false when there is nothing to show.
func coloredUnifiedDiff(fromName, toName, old, incoming string, context int) (string, bool) {
	edits := udiff.Strings(old, incoming)
	if len(edits) == 0 {
		return "", false
	}
	u, err := udiff.ToUnifiedDiff(fromName, toName, old, edits, context)
	if err != nil || len(u.Hunks) == 0 {
		return "", false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", clr.Bold("--- "+u.From))
	fmt.Fprintf(&b, "%s\n", clr.Bold("+++ "+u.To))
	for _, h := range u.Hunks {
		fromCount, toCount := 0, 0
		for _, l := range h.Lines {
			switch l.Kind {
			case udiff.Delete:
				fromCount++
			case udiff.Insert:
				toCount++
			case udiff.Equal:
				fromCount++
				toCount++
			}
		}
		fmt.Fprintf(&b, "%s\n", clr.Cyan(hunkHeader(h.FromLine, fromCount, h.ToLine, toCount)))
		for _, l := range h.Lines {
			content := strings.TrimSuffix(l.Content, "\n")
			switch l.Kind {
			case udiff.Delete:
				fmt.Fprintf(&b, "%s\n", clr.Red("-"+content))
			case udiff.Insert:
				fmt.Fprintf(&b, "%s\n", clr.Green("+"+content))
			case udiff.Equal:
				fmt.Fprintf(&b, " %s\n", content)
			}
			if !strings.HasSuffix(l.Content, "\n") {
				fmt.Fprintf(&b, "%s\n", clr.Faint(`\ No newline at end of file`))
			}
		}
	}
	return b.String(), true
}

// hunkHeader formats a unified-diff "@@ -from,count +to,count @@" header, mirroring
// go-udiff's own omit-the-count-when-1 and empty-file conventions.
func hunkHeader(fromLine, fromCount, toLine, toCount int) string {
	var b strings.Builder
	b.WriteString("@@")
	switch {
	case fromCount > 1:
		fmt.Fprintf(&b, " -%d,%d", fromLine, fromCount)
	case fromLine == 1 && fromCount == 0:
		b.WriteString(" -0,0")
	default:
		fmt.Fprintf(&b, " -%d", fromLine)
	}
	switch {
	case toCount > 1:
		fmt.Fprintf(&b, " +%d,%d", toLine, toCount)
	case toLine == 1 && toCount == 0:
		b.WriteString(" +0,0")
	default:
		fmt.Fprintf(&b, " +%d", toLine)
	}
	b.WriteString(" @@")
	return b.String()
}
