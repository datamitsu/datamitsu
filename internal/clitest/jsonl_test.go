package clitest

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseJSONL(t *testing.T) {
	stream := `{"type":"phase","op_id":"run-1","ts":17,"status":"start","op":"lint"}

{"type":"tool_run","op_id":"run-1:alpha:pkg","ts":18,"status":"fail","tool":"alpha","dir":"pkg","success":false,"duration_ms":4}
{"type":"done","op_id":"run-1","ts":19,"status":"fail","tools":1,"runs":1,"failed":1}
`
	events, err := ParseJSONL(stream)
	if err != nil {
		t.Fatalf("ParseJSONL: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (blank lines skipped)", len(events))
	}

	run := events[1]
	if run.Type != "tool_run" || run.OpID != "run-1:alpha:pkg" || run.Tool != "alpha" || run.Dir != "pkg" {
		t.Errorf("tool_run decoded as %+v", run)
	}
	if run.Success == nil || *run.Success {
		t.Errorf("success = %v, want an explicit false", run.Success)
	}
	if !run.Has("duration_ms") || run.Has("msg") {
		t.Errorf("Has: duration_ms=%v msg=%v, want true and false", run.Has("duration_ms"), run.Has("msg"))
	}
	if !run.Terminal() || events[0].Terminal() {
		t.Error("Terminal: a fail ends a chain, a start does not")
	}
	if events[0].Success != nil {
		t.Errorf("a line without success decoded success = %v, want nil", *events[0].Success)
	}
	if done := events[2]; done.Tools != 1 || done.Runs != 1 || done.Failed != 1 {
		t.Errorf("done decoded as %+v", done)
	}
}

func TestParseJSONLRejects(t *testing.T) {
	cases := map[string]string{
		"plain text":     "error: operation failed\n",
		"array":          `[{"type":"done","op_id":"run-1"}]`,
		"no type":        `{"op_id":"run-1"}`,
		"no op_id":       `{"type":"done"}`,
		"trailing data":  `{"type":"done","op_id":"run-1"} {}`,
		"stray bracket":  `{"type":"done","op_id":"run-1"}]`,
		"stray brace":    `{"type":"done","op_id":"run-1"}}`,
		"null":           `null`,
		"wrong type":     `{"type":"done","op_id":"run-1","runs":"two"}`,
		"after a good 1": "{\"type\":\"done\",\"op_id\":\"run-1\"}\nnot json\n",
	}
	for name, stream := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseJSONL(stream); err == nil {
				t.Errorf("ParseJSONL(%q) accepted a malformed stream", stream)
			}
		})
	}
}

func TestNormalizeJSONL(t *testing.T) {
	in := `{"type":"tool_run","op_id":"run-1:a:","ts":1737000000123,"status":"done","duration_ms":245,"tool":"a"}
{"ts":1737000000124,"type":"chunk","op_id":"run-1:a:","index":1,"total":3}

error: not an event
{"type":"done","op_id":"run-1","ts":7}]
{"type":"log","op_id":"log-1","ts":5,"msg":"<a> & <b>","duration_ms":0}
`
	want := `{"duration_ms":1,"op_id":"run-1:a:","status":"done","tool":"a","ts":0,"type":"tool_run"}
{"index":1,"op_id":"run-1:a:","total":3,"ts":0,"type":"chunk"}
error: not an event
{"type":"done","op_id":"run-1","ts":7}]
{"duration_ms":1,"msg":"<a> & <b>","op_id":"log-1","ts":0,"type":"log"}
`
	got := NormalizeJSONL(in)
	if got != want {
		t.Fatalf("NormalizeJSONL:\n%s", lineDiff(want, got))
	}
	if again := NormalizeJSONL(got); again != got {
		t.Errorf("NormalizeJSONL is not idempotent:\n%s", lineDiff(got, again))
	}
	if strings.Contains(NormalizeJSONL(`{"type":"phase","op_id":"run-1"}`), "duration_ms") {
		t.Error("NormalizeJSONL added a field the line did not carry")
	}
}

// errorsTB records Errorf calls instead of failing, so AssertChains can be
// driven over streams that must fail.
type errorsTB struct {
	testing.TB

	errs []string
}

func (r *errorsTB) Helper() {}

func (r *errorsTB) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func TestAssertChains(t *testing.T) {
	const (
		phase    = `{"type":"phase","op_id":"run-1","status":"start","op":"lint"}`
		aStart   = `{"type":"tool_run","op_id":"run-1:a:","status":"start","tool":"a"}`
		aDone    = `{"type":"tool_run","op_id":"run-1:a:","status":"done","tool":"a"}`
		bStart   = `{"type":"tool_run","op_id":"run-1:b:x","status":"start","tool":"b","dir":"x"}`
		bFail    = `{"type":"tool_run","op_id":"run-1:b:x","status":"fail","tool":"b","dir":"x"}`
		doneTwo  = `{"type":"done","op_id":"run-1","status":"done","runs":2}`
		doneOne  = `{"type":"done","op_id":"run-1","status":"fail","runs":1}`
		runError = `{"type":"error","op_id":"run-2","status":"fail","msg":"operation failed"}`
	)
	cases := []struct {
		name     string
		lines    []string
		orphaned []string
		wantErr  string
	}{
		{"complete chains", []string{phase, aStart, bStart, bFail, aDone, doneTwo, runError}, nil, ""},
		{"expected orphan", []string{phase, aStart, bStart, aDone, doneOne}, []string{"b"}, ""},
		{"unexpected orphan", []string{phase, aStart, bStart, aDone, doneOne}, nil, "1 start(s) and 0 terminal(s)"},
		{"missing orphan", []string{phase, aStart, aDone, doneOne}, []string{"b"}, `orphaned tool_run start for "b"`},
		{"terminal before start", []string{phase, aDone, aStart, doneOne}, nil, "no preceding start"},
		{"tool_run before phase", []string{aStart, phase, aDone, doneOne}, nil, "precedes the phase start"},
		{"no phase", []string{aStart, aDone}, nil, "belongs to no phase"},
		{"runs mismatch", []string{phase, aStart, aDone, doneTwo}, nil, "reports runs=2"},
		{"done before tool_run", []string{phase, doneOne, aStart, aDone}, nil, "follows the done"},
		{"missing done", []string{phase, aStart, aDone}, nil, "ends with 0 done event(s)"},
		{"two done events", []string{phase, aStart, aDone, doneOne, doneOne}, nil, "ends with 2 done event(s)"},
		{"done before phase", []string{`{"type":"done","op_id":"run-1","status":"done"}`, phase}, nil, "done before its phase start"},
		{"phase twice", []string{phase, phase, aStart, aDone, doneOne}, nil, "starts twice"},
		{"done without phase", []string{doneOne}, nil, "has no phase start"},
		{"progress status", []string{phase, `{"type":"tool_run","op_id":"run-1:a:","status":"progress","tool":"a"}`}, nil, `status "progress"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events, err := ParseJSONL(strings.Join(tc.lines, "\n"))
			if err != nil {
				t.Fatalf("ParseJSONL: %v", err)
			}
			rec := &errorsTB{}
			AssertChains(rec, events, tc.orphaned...)
			got := strings.Join(rec.errs, "\n")
			switch {
			case tc.wantErr == "" && got != "":
				t.Errorf("AssertChains reported:\n%s", got)
			case tc.wantErr != "" && !strings.Contains(got, tc.wantErr):
				t.Errorf("AssertChains reported %q, want it to contain %q", got, tc.wantErr)
			}
		})
	}
}
