package uievent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNextOpIDUniqueAndPrefixed(t *testing.T) {
	a := NextOpID("run")
	b := NextOpID("run")
	if a == b {
		t.Fatalf("NextOpID returned duplicate ids: %q", a)
	}
	for _, id := range []string{a, b} {
		if !strings.HasPrefix(id, "run-") {
			t.Errorf("id %q missing prefix", id)
		}
	}
}

// decodeLines parses every line written to buf as a JSON object, failing if any
// line is not valid JSON or is missing the mandatory type/op_id fields.
func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(buf)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("line is not valid JSON: %q: %v", line, err)
		}
		if _, ok := m["type"]; !ok {
			t.Errorf("line missing mandatory type: %q", line)
		}
		if _, ok := m["op_id"]; !ok {
			t.Errorf("line missing mandatory op_id: %q", line)
		}
		out = append(out, m)
	}
	return out
}

func TestJSONLSinkEmitsValidTypedLines(t *testing.T) {
	var buf bytes.Buffer
	s := NewJSONLSink(&buf)

	s.Emit(Event{Type: TypePhase, OpID: "run-1", Status: StatusStart, Op: "lint"})
	s.Emit(Event{Type: TypeToolRun, OpID: "run-1:eslint:.", Status: StatusDone, Tool: "eslint", Success: new(true)})
	s.Emit(Event{Type: TypeDone, OpID: "run-1", Status: StatusDone, Op: "lint", Tools: 1, Runs: 1})

	lines := decodeLines(t, &buf)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	// ts is stamped by the sink on every line.
	for _, m := range lines {
		if _, ok := m["ts"]; !ok {
			t.Errorf("line missing ts: %v", m)
		}
	}
	// Success:true must round-trip (pointer field, not dropped).
	if lines[1]["success"] != true {
		t.Errorf("success = %v, want true", lines[1]["success"])
	}
}

func TestJSONLSinkThrottlesProgressPerOp(t *testing.T) {
	var buf bytes.Buffer
	s := NewJSONLSink(&buf)

	base := time.Unix(1700000000, 0)
	clock := base
	s.nowFn = func() time.Time { return clock }

	emitProgress := func(op string) {
		s.Emit(Event{Type: TypeDownload, OpID: op, Status: StatusProgress, Name: "x", BytesDone: 1})
	}

	// First progress for dl-1 passes; a second within the interval is dropped.
	emitProgress("dl-1")
	clock = base.Add(10 * time.Millisecond)
	emitProgress("dl-1")
	// A different op_id is NOT throttled by dl-1's clock.
	emitProgress("dl-2")
	// After the interval elapses, dl-1 passes again.
	clock = base.Add(150 * time.Millisecond)
	emitProgress("dl-1")
	// A terminal (done) event is never throttled, even immediately after.
	s.Emit(Event{Type: TypeDownload, OpID: "dl-1", Status: StatusDone, Name: "x", BytesDone: 2})

	lines := decodeLines(t, &buf)
	// Expect: dl-1 progress, dl-2 progress, dl-1 progress (post-interval), dl-1 done
	// = 4 lines (the 10ms-later dl-1 progress is dropped).
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4: %v", len(lines), lines)
	}
}

func TestJSONLSinkDoesNotThrottlePhaseOrToolRun(t *testing.T) {
	var buf bytes.Buffer
	s := NewJSONLSink(&buf)
	clock := time.Unix(1700000000, 0)
	s.nowFn = func() time.Time { return clock } // frozen clock

	// All low-frequency event types fire back-to-back at the same instant and
	// must all survive (only download/chunk progress is throttled).
	s.Emit(Event{Type: TypePhase, OpID: "run-1", Status: StatusStart})
	s.Emit(Event{Type: TypeToolRun, OpID: "run-1:a:.", Status: StatusStart})
	s.Emit(Event{Type: TypeToolRun, OpID: "run-1:a:.", Status: StatusDone})
	s.Emit(Event{Type: TypeInstall, OpID: "inst-1", Status: StatusStart})
	s.Emit(Event{Type: TypeError, OpID: "run-1:a:.", Msg: "boom"})

	if lines := decodeLines(t, &buf); len(lines) != 5 {
		t.Fatalf("got %d lines, want 5", len(lines))
	}
}
