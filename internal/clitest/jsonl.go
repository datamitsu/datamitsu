package clitest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// Event is one line of a --log-format jsonl stderr stream. The typed fields are
// the ones a characterization asserts on; Fields keeps every key of the line as
// decoded, because whether an omitempty field is present at all is part of the
// contract.
type Event struct {
	Type    string `json:"type"`
	OpID    string `json:"op_id"`
	Status  string `json:"status"`
	Op      string `json:"op"`
	Tool    string `json:"tool"`
	Dir     string `json:"dir"`
	Msg     string `json:"msg"`
	Index   int    `json:"index"`
	Total   int    `json:"total"`
	Tools   int    `json:"tools"`
	Runs    int    `json:"runs"`
	Failed  int    `json:"failed"`
	Skipped int    `json:"skipped"`
	// Success, Cancelled and Complete are nil when the line does not carry them.
	Success   *bool          `json:"success"`
	Cancelled *int           `json:"cancelled"`
	Complete  *bool          `json:"complete"`
	Fields    map[string]any `json:"-"`
}

// Has reports whether the line carried key.
func (e Event) Has(key string) bool {
	_, ok := e.Fields[key]
	return ok
}

// Terminal reports whether e ends a correlated chain: done, fail, or skip for a
// task the run stopped before it finished.
func (e Event) Terminal() bool {
	return e.Status == "done" || e.Status == "fail" || e.Status == "skip"
}

// NotStarted reports whether e is the one event of a task the run stopped
// before it started: a skip that opens and closes its chain at once.
func (e Event) NotStarted() bool {
	return e.Type == "tool_run" && e.Status == "skip" && strings.HasPrefix(e.Msg, "not started: ")
}

// ParseJSONL decodes a JSON-L stream. Every non-blank line must be a JSON object
// carrying a non-empty type and op_id; the first line that is not fails the
// whole parse, naming it.
func ParseJSONL(stderr string) ([]Event, error) {
	var events []Event
	for i, line := range strings.Split(stderr, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields, err := decodeLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d is not a JSON object: %w: %q", i+1, err, line)
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("line %d does not match the event shape: %w: %q", i+1, err, line)
		}
		if e.Type == "" || e.OpID == "" {
			return nil, fmt.Errorf("line %d lacks a type or an op_id: %q", i+1, line)
		}
		e.Fields = fields
		events = append(events, e)
	}
	return events, nil
}

// MustParseJSONL is ParseJSONL failing the test on a malformed stream.
func MustParseJSONL(tb testing.TB, stderr string) []Event {
	tb.Helper()
	events, err := ParseJSONL(stderr)
	if err != nil {
		tb.Fatalf("clitest: stderr is not a JSON-L event stream: %v\n%s", err, stderr)
	}
	return events
}

// AssertChains checks the causal shape of an event stream without relying on
// the order in which parallel events arrive:
//
//   - a terminal tool_run never precedes a start with the same op_id, and each
//     op_id has as many terminals as starts, not counting the skip events of
//     tasks that never started, which stand alone;
//   - a skip that closes a start says the task was cancelled, one that stands
//     alone says it was not started;
//   - an operation's phase start precedes every tool_run of that operation;
//   - every operation that started ends with exactly one done, which follows
//     all of the operation's tool_run events, reports as runs the number of its
//     done and fail tool_run events, and as cancelled (absent meaning zero) the
//     number of its skip ones;
//   - the run-level done (op_id "cmd-…") appears at most once, after every
//     phase, tool_run and operation done, and reports the stream's totals of
//     runs and cancelled tasks, with complete false when any task was stopped.
//
// A tool_run belongs to the operation whose phase op_id prefixes its own. Tasks
// of one tool in one directory share an op_id, so which start a terminal closes
// is not asserted — only that the counts agree.
func AssertChains(tb testing.TB, events []Event) {
	tb.Helper()

	phaseAt := map[string]int{}
	doneAt := map[string][]int{}
	var runDone []int
	lastOperationEvent := -1
	for i, e := range events {
		if e.Type == "phase" || e.Type == "tool_run" || (e.Type == "done" && !isRunDone(e)) {
			lastOperationEvent = i
		}
		switch {
		case e.Type == "done" && isRunDone(e):
			runDone = append(runDone, i)
		case e.Type == "phase" && e.Status == "start":
			if _, seen := phaseAt[e.OpID]; seen {
				tb.Errorf("clitest: operation %q starts twice", e.OpID)
				continue
			}
			phaseAt[e.OpID] = i
		case e.Type == "done":
			doneAt[e.OpID] = append(doneAt[e.OpID], i)
		}
	}
	operationOf := func(opID string) (string, bool) {
		for run := range phaseAt {
			if strings.HasPrefix(opID, run+":") {
				return run, true
			}
		}
		return "", false
	}

	type chain struct {
		starts, terminal int
	}
	chains := map[string]*chain{}
	runs := map[string]int{}
	skips := map[string]int{}
	for i, e := range events {
		if e.Type != "tool_run" {
			continue
		}
		run, ok := operationOf(e.OpID)
		switch done := doneAt[run]; {
		case !ok:
			tb.Errorf("clitest: tool_run %q belongs to no phase", e.OpID)
		case phaseAt[run] > i:
			tb.Errorf("clitest: tool_run %q precedes the phase start of %q", e.OpID, run)
		case len(done) > 0 && done[0] < i:
			tb.Errorf("clitest: tool_run %q follows the done of %q", e.OpID, run)
		}
		c := chains[e.OpID]
		if c == nil {
			c = &chain{}
			chains[e.OpID] = c
		}
		switch {
		case e.Status == "start":
			c.starts++
		case e.NotStarted():
			skips[run]++
		case e.Terminal():
			c.terminal++
			if c.terminal > c.starts {
				tb.Errorf("clitest: terminal tool_run %q (%s) has no preceding start", e.OpID, e.Status)
			}
			if e.Status == "skip" {
				skips[run]++
				if !strings.HasPrefix(e.Msg, "cancelled: ") {
					tb.Errorf("clitest: skip tool_run %q closes a start but says %q", e.OpID, e.Msg)
				}
				continue
			}
			runs[run]++
		default:
			tb.Errorf("clitest: tool_run %q has status %q", e.OpID, e.Status)
		}
	}

	for opID, c := range chains {
		if c.terminal != c.starts {
			tb.Errorf("clitest: tool_run %q has %d start(s) and %d terminal(s)", opID, c.starts, c.terminal)
		}
	}

	for run, start := range phaseAt {
		done := doneAt[run]
		switch {
		case len(done) != 1:
			tb.Errorf("clitest: operation %q ends with %d done event(s), want 1", run, len(done))
		case done[0] < start:
			tb.Errorf("clitest: operation %q is done before its phase start", run)
		}
	}
	assertRunDone(tb, events, runDone, lastOperationEvent, runs, skips)
	for _, e := range events {
		if e.Type != "done" || isRunDone(e) {
			continue
		}
		if _, ok := phaseAt[e.OpID]; !ok {
			tb.Errorf("clitest: done %q has no phase start", e.OpID)
			continue
		}
		if e.Runs != runs[e.OpID] {
			tb.Errorf("clitest: done %q reports runs=%d, the stream has %d done or fail tool_run event(s)",
				e.OpID, e.Runs, runs[e.OpID])
		}
		cancelled := 0
		if e.Cancelled != nil {
			cancelled = *e.Cancelled
		}
		if cancelled != skips[e.OpID] {
			tb.Errorf("clitest: done %q reports cancelled=%d, the stream has %d skip tool_run event(s)",
				e.OpID, cancelled, skips[e.OpID])
		}
	}
}

func isRunDone(e Event) bool {
	return e.Type == "done" && strings.HasPrefix(e.OpID, "cmd-")
}

func assertRunDone(tb testing.TB, events []Event, at []int, lastOperationEvent int, runs, skips map[string]int) {
	tb.Helper()
	switch {
	case len(at) == 0:
		return
	case len(at) > 1:
		tb.Errorf("clitest: the stream has %d run-level done events, want at most 1", len(at))
		return
	case at[0] < lastOperationEvent:
		tb.Errorf("clitest: the run-level done precedes an operation event")
	}
	e := events[at[0]]
	totalRuns, totalSkips := 0, 0
	for _, n := range runs {
		totalRuns += n
	}
	for _, n := range skips {
		totalSkips += n
	}
	cancelled := -1
	if e.Cancelled != nil {
		cancelled = *e.Cancelled
	}
	if e.Runs != totalRuns || cancelled != totalSkips {
		tb.Errorf("clitest: run-level done reports runs=%d cancelled=%d, the stream has %d run(s) and %d stopped task(s)",
			e.Runs, cancelled, totalRuns, totalSkips)
	}
	if e.Complete == nil || (totalSkips > 0 && *e.Complete) {
		tb.Errorf("clitest: run-level done reports complete=%v with %d stopped task(s)", e.Complete, totalSkips)
	}
}

// NormalizeJSONL makes a JSON-L stream comparable across runs: ts becomes 0 and
// duration_ms becomes 1 on the lines that carry them, and each line is
// re-encoded with sorted keys. An absent field stays absent, so a field that
// omitempty drops is still visible as missing. A line that is not a JSON object
// is kept verbatim.
func NormalizeJSONL(stderr string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(stderr, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields, err := decodeLine(line)
		if err != nil {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		if _, ok := fields["ts"]; ok {
			fields["ts"] = 0
		}
		if _, ok := fields["duration_ms"]; ok {
			fields["duration_ms"] = 1
		}
		var out bytes.Buffer
		enc := json.NewEncoder(&out)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(fields); err != nil {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		b.Write(out.Bytes())
	}
	return b.String()
}

func decodeLine(line string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	var fields map[string]any
	if err := dec.Decode(&fields); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if fields == nil {
		return nil, errors.New("null")
	}
	// More reports only another element of an enclosing array or object, so a
	// stray "]" or "}" after the object would pass it; only EOF proves the line
	// held one object and nothing else.
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data")
	}
	return fields, nil
}
