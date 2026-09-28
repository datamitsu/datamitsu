package textdiff

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func TestUnified(t *testing.T) {
	tests := []struct {
		name, before, after, want string
	}{
		{name: "equal", before: "a\n", after: "a\n", want: ""},
		{
			name:   "one line changed",
			before: "1\n2\n3\n4\n5\n6\n7\n8\n9\n",
			after:  "1\n2\n3\n4\nfive\n6\n7\n8\n9\n",
			want:   "--- a/f.go\n+++ b/f.go\n@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+five\n 6\n 7\n 8\n",
		},
		{
			name:   "two changes far apart are two hunks",
			before: "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n",
			after:  "one\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\ntwelve\n",
			want: "--- a/f.go\n+++ b/f.go\n@@ -1,4 +1,4 @@\n-1\n+one\n 2\n 3\n 4\n" +
				"@@ -9,4 +9,4 @@\n 9\n 10\n 11\n-12\n+twelve\n",
		},
		{
			name:   "insertion into an empty file",
			before: "",
			after:  "a\n",
			want:   "--- a/f.go\n+++ b/f.go\n@@ -0,0 +1 @@\n+a\n",
		},
		{
			name:   "a final newline added",
			before: "a",
			after:  "a\n",
			want:   "--- a/f.go\n+++ b/f.go\n@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+a\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Unified("f.go", tt.before, tt.after); got != tt.want {
				t.Errorf("Unified =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestUnifiedApplies: applying the patch to the text before gives the text
// after, for many random edits.
func TestUnifiedApplies(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := range 500 {
		before := randomText(rng)
		after := mutate(rng, before)
		patch := Unified("f", before, after)
		got, err := apply(before, patch)
		if err != nil || got != after {
			t.Fatalf("case %d: apply = %q, %v; want %q\nbefore %q\npatch\n%s", i, got, err, after, before, patch)
		}
	}
}

func randomText(rng *rand.Rand) string {
	var b strings.Builder
	for range rng.Intn(20) {
		b.WriteString(strconv.Itoa(rng.Intn(5)) + "\n")
	}
	if rng.Intn(4) == 0 {
		b.WriteString("tail")
	}
	return b.String()
}

func mutate(rng *rand.Rand, text string) string {
	lines := splitLines(text)
	for range rng.Intn(4) {
		switch pos := rng.Intn(len(lines) + 1); rng.Intn(3) {
		case 0:
			lines = append(lines[:pos], append([]string{"new" + strconv.Itoa(rng.Intn(3)) + "\n"}, lines[pos:]...)...)
		case 1:
			if pos < len(lines) {
				lines = append(lines[:pos], lines[pos+1:]...)
			}
		default:
			if pos < len(lines) {
				lines[pos] = "changed\n"
			}
		}
	}
	return strings.Join(lines, "")
}

// apply applies a unified diff the way patch does, for the test.
func apply(before, patch string) (string, error) {
	if patch == "" {
		return before, nil
	}
	src := splitLines(before)
	var out []string
	pos := 0
	lines := strings.SplitAfter(patch, "\n")
	for i := 2; i < len(lines) && lines[i] != ""; i++ {
		line := lines[i]
		if !strings.HasPrefix(line, "@@ -") {
			return "", fmt.Errorf("line %d: %q is not a hunk header", i, line)
		}
		var start int
		spec := strings.Fields(line)[1][1:]
		if n, _, _ := strings.Cut(spec, ","); n != "" {
			start, _ = strconv.Atoi(n)
		}
		from := start - 1
		if strings.HasSuffix(spec, ",0") {
			from = start
		}
		out = append(out, src[pos:from]...)
		pos = from
		for i+1 < len(lines) && lines[i+1] != "" && !strings.HasPrefix(lines[i+1], "@@") {
			i++
			body := lines[i]
			text := body[1:]
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], `\ No newline`) {
				text = strings.TrimSuffix(text, "\n")
				i++
			}
			switch body[0] {
			case ' ':
				out = append(out, src[pos])
				pos++
			case '-':
				if src[pos] != text {
					return "", fmt.Errorf("removes %q where the text holds %q", text, src[pos])
				}
				pos++
			case '+':
				out = append(out, text)
			}
		}
	}
	out = append(out, src[pos:]...)
	return strings.Join(out, ""), nil
}
