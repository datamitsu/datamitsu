package prepare

import (
	"strings"
	"testing"
)

func TestDiagnosticTailPreservesLatestBytesAcrossOverflow(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		chunks []string
	}{
		{"single oversized diagnostic", []string{strings.Repeat("old", diagnosticTailBytes), "final registry failure"}},
		{"overflow across writes", []string{strings.Repeat("a", diagnosticTailBytes-5), "boundary", "final registry failure"}},
		{"exact boundary then overflow", []string{strings.Repeat("b", diagnosticTailBytes), "final registry failure"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var tail diagnosticTail
			var output string
			for _, chunk := range scenario.chunks {
				if n, err := tail.Write([]byte(chunk)); err != nil || n != len(chunk) {
					t.Fatalf("diagnostic write = %d, %v", n, err)
				}
				output += chunk
				want := output
				if len(want) > diagnosticTailBytes {
					want = want[len(want)-diagnosticTailBytes:]
				}
				if string(tail.data) != want {
					t.Fatal("diagnostic tail lost or reordered the latest stderr bytes")
				}
			}
		})
	}
}
