package report

import (
	"reflect"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func anchored(col int, message string) diagnostic.Diagnostic {
	return diagnostic.Diagnostic{
		File: abs("a.ts"), Row: 3, Col: col, Code: "R", Source: "eslint", Message: message,
		Severity: diagnostic.SeverityError, Anchor: &diagnostic.Anchor{LineHash: "h"},
	}
}

func marks(proc *tooling.ProcessResult) []bool {
	out := make([]bool, len(proc.Diagnostics))
	for i, d := range proc.Diagnostics {
		out[i] = d.Baselined
	}
	return out
}

// TestBaselineMatcherCountsALine: a baseline holding one finding of a rule on
// a line marks one — never a second, new one beside it, whichever process of
// the tool reports it and in whichever order the processes are judged.
func TestBaselineMatcherCountsALine(t *testing.T) {
	set := BaselineSet{Fingerprint("eslint", "R", "a.ts", "h", 0): true}
	lint := tooling.Task{ToolName: "eslint", Operation: config.OpLint}
	fix := tooling.Task{ToolName: "eslint", Operation: config.OpFix}

	t.Run("two processes", func(t *testing.T) {
		for _, order := range [][2]int{{0, 1}, {1, 0}} {
			m := NewBaselineMatcher(set, root)
			processes := []*tooling.ProcessResult{
				{Diagnostics: []diagnostic.Diagnostic{anchored(1, "old")}},
				{Diagnostics: []diagnostic.Diagnostic{anchored(5, "new")}},
			}
			for _, i := range order {
				m.Mark(lint, processes[i])
			}
			if n := len(filter(marks(processes[0]), marks(processes[1]))); n != 1 {
				t.Errorf("order %v: %d findings marked, want exactly one", order, n)
			}
		}
	})

	t.Run("one process, in ordinal order", func(t *testing.T) {
		m := NewBaselineMatcher(set, root)
		proc := &tooling.ProcessResult{Diagnostics: []diagnostic.Diagnostic{anchored(9, "later"), anchored(1, "first")}}
		m.Mark(lint, proc)
		if got := marks(proc); !reflect.DeepEqual(got, []bool{false, true}) {
			t.Errorf("marks = %v, want the first finding of the line marked", got)
		}
	})

	t.Run("a finding two processes report is one", func(t *testing.T) {
		m := NewBaselineMatcher(set, root)
		a := &tooling.ProcessResult{Diagnostics: []diagnostic.Diagnostic{anchored(1, "old")}}
		b := &tooling.ProcessResult{Diagnostics: []diagnostic.Diagnostic{anchored(1, "old")}}
		m.Mark(lint, a)
		m.Mark(lint, b)
		if !a.Diagnostics[0].Baselined || !b.Diagnostics[0].Baselined {
			t.Error("a duplicate should be marked as its first report is")
		}
	})

	t.Run("each operation", func(t *testing.T) {
		m := NewBaselineMatcher(set, root)
		f := &tooling.ProcessResult{Diagnostics: []diagnostic.Diagnostic{anchored(1, "old")}}
		l := &tooling.ProcessResult{Diagnostics: []diagnostic.Diagnostic{anchored(1, "old")}}
		m.Mark(fix, f)
		m.Mark(lint, l)
		if !f.Diagnostics[0].Baselined || !l.Diagnostics[0].Baselined {
			t.Error("fix and lint each report the finding the baseline holds")
		}
	})

	t.Run("not held", func(t *testing.T) {
		m := NewBaselineMatcher(set, root)
		other := anchored(1, "other rule")
		other.Code = "S"
		proc := &tooling.ProcessResult{Diagnostics: []diagnostic.Diagnostic{other}}
		m.Mark(lint, proc)
		if proc.Diagnostics[0].Baselined {
			t.Error("a finding of another rule was marked")
		}
	})

	var nilMatcher *BaselineMatcher
	nilMatcher.Mark(lint, &tooling.ProcessResult{Diagnostics: []diagnostic.Diagnostic{anchored(1, "x")}})
}

func filter(lists ...[]bool) []bool {
	var out []bool
	for _, list := range lists {
		for _, b := range list {
			if b {
				out = append(out, b)
			}
		}
	}
	return out
}
