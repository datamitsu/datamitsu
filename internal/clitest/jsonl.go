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
	// Success is nil when the line carries no success field.
	Success *bool          `json:"success"`
	Fields  map[string]any `json:"-"`
}

// Has reports whether the line carried key.
func (e Event) Has(key string) bool {
	_, ok := e.Fields[key]
	return ok
}

// Terminal reports whether e ends a correlated chain.
func (e Event) Terminal() bool {
	return e.Status == "done" || e.Status == "fail"
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
//     op_id has as many terminals as starts — except for the tools named in
//     orphaned, which must have at least one start left without a terminal (a
//     task cancelled after it started);
//   - an operation's phase start precedes every tool_run of that operation;
//   - every operation that started ends with exactly one done, which follows
//     all of the operation's tool_run events and reports as runs the number
//     of its terminal tool_run events.
//
// A tool_run belongs to the operation whose phase op_id prefixes its own.
func AssertChains(tb testing.TB, events []Event, orphaned ...string) {
	tb.Helper()

	phaseAt := map[string]int{}
	doneAt := map[string][]int{}
	for i, e := range events {
		switch {
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
		tool             string
		starts, terminal int
	}
	chains := map[string]*chain{}
	terminals := map[string]int{}
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
			c = &chain{tool: e.Tool}
			chains[e.OpID] = c
		}
		switch {
		case e.Status == "start":
			c.starts++
		case e.Terminal():
			c.terminal++
			if c.terminal > c.starts {
				tb.Errorf("clitest: terminal tool_run %q (%s) has no preceding start", e.OpID, e.Status)
			}
			terminals[run]++
		default:
			tb.Errorf("clitest: tool_run %q has status %q", e.OpID, e.Status)
		}
	}

	expectOrphan := map[string]bool{}
	for _, tool := range orphaned {
		expectOrphan[tool] = true
	}
	orphanSeen := map[string]bool{}
	for opID, c := range chains {
		switch {
		case c.terminal == c.starts:
		case expectOrphan[c.tool]:
			orphanSeen[c.tool] = true
		default:
			tb.Errorf("clitest: tool_run %q has %d start(s) and %d terminal(s)", opID, c.starts, c.terminal)
		}
	}
	for _, tool := range orphaned {
		if !orphanSeen[tool] {
			tb.Errorf("clitest: expected an orphaned tool_run start for %q, found none", tool)
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
	for _, e := range events {
		if e.Type != "done" {
			continue
		}
		if _, ok := phaseAt[e.OpID]; !ok {
			tb.Errorf("clitest: done %q has no phase start", e.OpID)
			continue
		}
		if e.Runs != terminals[e.OpID] {
			tb.Errorf("clitest: done %q reports runs=%d, the stream has %d terminal tool_run event(s)",
				e.OpID, e.Runs, terminals[e.OpID])
		}
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
