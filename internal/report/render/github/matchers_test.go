package github

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The problem matchers the setup actions register for the whole job, as the
// upstream repositories hold them at the last commit that changed each file:
// actions/setup-go matchers.json at fdeec47002800c39dbe2cd2ab93d5f853d14ba32,
// actions/setup-node .github/tsc.json at 46071b5c7a2e0c34e49c3cb8a0e792e86e18d5ea,
// .github/eslint-stylish.json at 40337cb8f758cccdfe3475af609daa63f81c7e23 and
// .github/eslint-compact.json at 1ccdddc9b8a87c2e16da2b1a0641137dc86b498b.
// A matcher reads every line a step prints, stop-commands or not, so what the
// frame prints must not look like a tool's own report of a finding.

type matcher struct {
	owner    string
	patterns []*regexp.Regexp
}

func loadMatcher(t *testing.T, name string) matcher {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "matchers", name))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ProblemMatcher []struct {
			Owner   string `json:"owner"`
			Pattern []struct {
				Regexp string `json:"regexp"`
			} `json:"pattern"`
		} `json:"problemMatcher"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	pm := doc.ProblemMatcher[0]
	m := matcher{owner: pm.Owner}
	for _, p := range pm.Pattern {
		m.patterns = append(m.patterns, regexp.MustCompile(p.Regexp))
	}
	return m
}

// match runs the matcher over lines as the runner does, a multi-line matcher
// needing each of its patterns on consecutive lines, and returns the file
// group of every match.
func (m matcher) match(lines []string) []string {
	var files []string
	for i := range lines {
		if i+len(m.patterns) > len(lines) {
			break
		}
		matched := true
		var file string
		for k, p := range m.patterns {
			sub := p.FindStringSubmatch(lines[i+k])
			if sub == nil {
				matched = false
				break
			}
			if k == 0 && len(sub) > 1 {
				file = sub[1]
			}
		}
		if matched {
			files = append(files, file)
		}
	}
	return files
}

// framed is a line as a failure frame prints it, colours off.
func framed(line string) string { return "  │  " + line }

func TestMatchersAgainstFramedOutput(t *testing.T) {
	goRaw := framed("internal/x/a.go:12:3: undefined: y")
	goParsed := framed("internal/x/a.go:12:3 error undefined: y [typecheck]")
	tscRaw := framed("src/a.ts(3,7): error TS2322: Type 'string' is not assignable to type 'number'.")
	tscParsed := framed("src/a.ts:3:7 error Type 'string' is not assignable to type 'number'. [TS2322]")
	stylish := []string{framed("/repo/src/a.js"), framed("  3:7  error  'x' is assigned a value but never used  no-unused-vars")}
	compact := framed("/repo/src/a.js: line 3, col 7, Error - Unexpected var, use let or const instead. (no-var)")

	t.Run("tsc", func(t *testing.T) {
		m := loadMatcher(t, "tsc.json")
		if got := m.match([]string{tscRaw, tscParsed, goRaw}); len(got) != 0 {
			t.Errorf("tsc matched a framed line: %q", got)
		}
		if got := m.match([]string{"src/a.ts(3,7): error TS2322: x"}); len(got) != 1 {
			t.Error("the fixture no longer matches the unframed tsc line it describes")
		}
	})

	t.Run("eslint-stylish", func(t *testing.T) {
		m := loadMatcher(t, "eslint-stylish.json")
		lines := append(append([]string{}, stylish...), goRaw, tscRaw, compact)
		if got := m.match(lines); len(got) != 0 {
			t.Errorf("eslint-stylish matched framed lines: %q", got)
		}
		if got := m.match([]string{"/repo/src/a.js", "  3:7  error  'x' is unused  no-unused-vars"}); len(got) != 1 {
			t.Error("the fixture no longer matches the unframed stylish report it describes")
		}
	})

	// Residual: go's pattern is not anchored at the path — any text may come
	// before it — so a framed raw line in Go's format matches, with the frame
	// in the file group. GitHub finds no such file and keeps the annotation
	// without a location. A parsed line, which has no colon after its column,
	// does not match.
	t.Run("go", func(t *testing.T) {
		m := loadMatcher(t, "go.json")
		got := m.match([]string{goRaw})
		if len(got) != 1 || !strings.HasPrefix(got[0], "│") {
			t.Errorf("go on a framed raw line = %q, want one match whose file starts with the frame", got)
		}
		if got := m.match([]string{goParsed, tscRaw, tscParsed}); len(got) != 0 {
			t.Errorf("go matched a framed parsed line: %q", got)
		}
	})

	// Residual: eslint-compact is not anchored at the path either, and no
	// wrapper tool prints its format; the frame lands in the file group.
	t.Run("eslint-compact", func(t *testing.T) {
		m := loadMatcher(t, "eslint-compact.json")
		got := m.match([]string{compact})
		if len(got) != 1 || !strings.HasPrefix(got[0], "  │  ") {
			t.Errorf("eslint-compact on a framed line = %q, want one match whose file starts with the frame", got)
		}
	})
}
