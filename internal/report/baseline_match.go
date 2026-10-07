package report

import (
	"sort"
	"sync"

	"github.com/datamitsu/datamitsu/internal/tooling"
)

// BaselineMatcher marks the findings a baseline holds while their processes
// are judged, before the threshold decides.
//
// A fingerprint's ordinal counts the findings of one tool with the same rule
// on the same line of a file, and a report settles it across all the
// processes of the tool — which a process being judged cannot know. So the
// matcher counts instead: it reads from the baseline how many findings of a
// rule on a line it holds (ordinals 0 to n-1), and marks at most that many
// distinct findings of that rule on that line in an operation, whichever
// process reports them. A second finding beside a baselined one on the same
// line is never marked, however the tool's work was split; a finding two
// processes both report is one finding, marked or not in both.
type BaselineMatcher struct {
	set  BaselineSet
	root string

	mu   sync.Mutex
	held map[lineKey]int
	used map[opLine]int
	seen map[opFinding]bool
}

// lineKey is what a fingerprint rests on besides its ordinal: a rule on a
// line of a file.
type lineKey struct {
	tool, code, relPath, lineHash string
}

type opLine struct {
	op  string
	key lineKey
}

type opFinding struct {
	op    string
	input fingerprintInput
}

// NewBaselineMatcher matches against set; root is the repository root the
// findings' paths are made relative to.
func NewBaselineMatcher(set BaselineSet, root string) *BaselineMatcher {
	return &BaselineMatcher{
		set: set, root: root,
		held: map[lineKey]int{}, used: map[opLine]int{}, seen: map[opFinding]bool{},
	}
}

// Mark sets Baselined on the findings of proc, a process of task, that the
// baseline holds. The findings must be anchored (Annotator.Annotate).
func (m *BaselineMatcher) Mark(task tooling.Task, proc *tooling.ProcessResult) {
	if m == nil || len(proc.Diagnostics) == 0 {
		return
	}
	in := make([]fingerprintInput, len(proc.Diagnostics))
	for i, d := range proc.Diagnostics {
		in[i] = inputOf(task.ToolName, RelPath(m.root, d.File), d)
	}
	// In ordinal order, so that of the findings of a line the first ones are
	// the ones the baseline's first ordinals stand for.
	order := make([]int, len(in))
	for i := range order {
		order[i] = i
	}
	numbers := ordinals(in)
	sort.SliceStable(order, func(a, b int) bool { return numbers[order[a]] < numbers[order[b]] })

	m.mu.Lock()
	defer m.mu.Unlock()
	op := string(task.Operation)
	for _, i := range order {
		f := opFinding{op: op, input: in[i]}
		if marked, ok := m.seen[f]; ok {
			proc.Diagnostics[i].Baselined = marked
			continue
		}
		key := lineKey{tool: in[i].tool, code: in[i].code, relPath: in[i].relPath, lineHash: in[i].lineHash}
		at := opLine{op: op, key: key}
		marked := m.used[at] < m.heldOf(key)
		if marked {
			m.used[at]++
		}
		m.seen[f] = marked
		proc.Diagnostics[i].Baselined = marked
	}
}

// heldOf is how many findings of key the baseline holds: the ordinals from 0
// up whose fingerprints it has.
func (m *BaselineMatcher) heldOf(key lineKey) int {
	if n, ok := m.held[key]; ok {
		return n
	}
	n := 0
	for m.set[Fingerprint(key.tool, key.code, key.relPath, key.lineHash, n)] {
		n++
	}
	m.held[key] = n
	return n
}
