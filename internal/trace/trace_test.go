package trace

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// enable turns tracing on for one test and restores the previous state, so a
// case that asserts the disabled path cannot be poisoned by an earlier one.
func enable(t *testing.T) {
	t.Helper()
	prev := enabled.Load()
	Reset()
	SetEnabled(true)
	t.Cleanup(func() {
		SetEnabled(prev)
		Reset()
		rec.mu.Lock()
		flushed = false
		rec.mu.Unlock()
	})
}

func TestDisabledRecordsNothing(t *testing.T) {
	Reset()
	SetEnabled(false)
	t.Cleanup(Reset)

	Start(CatPlan, "span").End()
	Start(CatPlan, "span").EndWith(A("k", 1))
	Instant(CatPlan, "event")
	c := NewCounter("test.disabled_counter")
	c.Add(5)

	if events, _ := rec.snapshot(); len(events) != 0 {
		t.Errorf("recorded %d events while disabled, want 0", len(events))
	}
	if got := c.Value(); got != 0 {
		t.Errorf("counter = %d while disabled, want 0", got)
	}
	if Enabled() {
		t.Error("Enabled() is true after SetEnabled(false)")
	}
}

func TestDisabledSpanIsZeroValue(t *testing.T) {
	Reset()
	SetEnabled(false)
	t.Cleanup(Reset)

	// The zero Span is what makes the disabled path allocation-free: End must be
	// a single compare on it, and must not reach the recorder.
	s := Start(CatExec, "x")
	if s.start != 0 {
		t.Errorf("disabled Start returned start=%d, want 0", s.start)
	}
	var zero Span
	zero.End()
	zero.EndWith(A("k", "v"))
	if events, _ := rec.snapshot(); len(events) != 0 {
		t.Errorf("the zero Span recorded %d events, want 0", len(events))
	}
}

func TestEnabledRecordsSpansAndCounters(t *testing.T) {
	enable(t)

	c := NewCounter("test.enabled_counter")
	span := Start(CatConfig, "load")
	time.Sleep(time.Millisecond)
	span.EndWith(A("bytes", 42))
	Instant(CatConfig, "cache-miss", A("key", "abc"))
	c.Add(3)
	c.Add(4)

	events, dropped := rec.snapshot()
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if len(events) != 2 {
		t.Fatalf("recorded %d events, want 2", len(events))
	}
	if events[0].Name != "load" || events[0].Cat != CatConfig {
		t.Errorf("span = %q/%q, want load/config", events[0].Cat, events[0].Name)
	}
	if events[0].Dur <= 0 {
		t.Errorf("span duration = %d, want > 0", events[0].Dur)
	}
	if !events[1].Instant {
		t.Error("second event should be an instant")
	}
	if got := c.Value(); got != 7 {
		t.Errorf("counter = %d, want 7", got)
	}
}

func TestConcurrentRecording(t *testing.T) {
	enable(t)

	c := NewCounter("test.concurrent")
	const goroutines, each = 16, 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range each {
				s := Start(CatExec, "task")
				c.Add(1)
				s.End()
			}
		}()
	}
	wg.Wait()

	events, _ := rec.snapshot()
	if len(events) != goroutines*each {
		t.Errorf("recorded %d events, want %d", len(events), goroutines*each)
	}
	if got := c.Value(); got != goroutines*each {
		t.Errorf("counter = %d, want %d", got, goroutines*each)
	}
}

func TestFlushWritesBothFiles(t *testing.T) {
	enable(t)

	dir := t.TempDir()
	Init(true, "lint")
	Start(CatPlan, "collectTasks").EndWith(A("tasks", 7))
	NewCounter("test.flush_counter").Add(11)

	var summary bytes.Buffer
	written := Flush(FlushOptions{
		Dir:     dir,
		Slug:    "my-repo",
		Summary: &summary,
		Meta:    map[string]string{"command": "lint"},
	})

	if len(written) != 2 {
		t.Fatalf("Flush wrote %d files, want 2: %v", len(written), written)
	}

	var jsonPath, textPath string
	for _, p := range written {
		switch filepath.Ext(p) {
		case ".json":
			jsonPath = p
		case ".txt":
			textPath = p
		}
	}
	if jsonPath == "" || textPath == "" {
		t.Fatalf("expected a .json and a .txt, got %v", written)
	}

	for _, p := range written {
		base := filepath.Base(p)
		if !strings.Contains(base, "my-repo") {
			t.Errorf("file name %q does not name the repository", base)
		}
		if !strings.Contains(base, "lint") {
			t.Errorf("file name %q does not name the command", base)
		}
	}

	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("reading trace: %v", err)
	}
	var decoded struct {
		TraceEvents []struct {
			Name string         `json:"name"`
			Ph   string         `json:"ph"`
			Args map[string]any `json:"args"`
		} `json:"traceEvents"`
		OtherData map[string]string `json:"otherData"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("trace file is not valid JSON: %v", err)
	}
	if decoded.OtherData["command"] != "lint" {
		t.Errorf("otherData.command = %q, want lint", decoded.OtherData["command"])
	}

	var sawSpan, sawCounters bool
	for _, e := range decoded.TraceEvents {
		if e.Name == "collectTasks" && e.Ph == "X" {
			sawSpan = true
		}
		if e.Name == "counters" {
			sawCounters = true
			if e.Args["test.flush_counter"] != float64(11) {
				t.Errorf("counter in trace = %v, want 11", e.Args["test.flush_counter"])
			}
		}
	}
	if !sawSpan {
		t.Error("trace has no complete event for collectTasks")
	}
	if !sawCounters {
		t.Error("trace has no counters event")
	}

	report, err := os.ReadFile(textPath)
	if err != nil {
		t.Fatalf("reading report: %v", err)
	}
	for _, want := range []string{"collectTasks", "test.flush_counter", "aggregated spans"} {
		if !strings.Contains(string(report), want) {
			t.Errorf("report does not mention %q", want)
		}
	}
	if !strings.Contains(summary.String(), "collectTasks") {
		t.Errorf("stderr summary does not mention the span:\n%s", summary.String())
	}
}

func TestFlushIsIdempotent(t *testing.T) {
	enable(t)
	dir := t.TempDir()

	Start(CatPlan, "once").End()
	if got := Flush(FlushOptions{Dir: dir, Slug: "r"}); len(got) != 2 {
		t.Fatalf("first Flush wrote %d files, want 2", len(got))
	}
	if got := Flush(FlushOptions{Dir: dir, Slug: "r"}); got != nil {
		t.Errorf("second Flush wrote %v, want nothing", got)
	}
}

func TestFlushDisabledWritesNothing(t *testing.T) {
	Reset()
	SetEnabled(false)
	t.Cleanup(Reset)

	dir := t.TempDir()
	var summary bytes.Buffer
	if got := Flush(FlushOptions{Dir: dir, Slug: "r", Summary: &summary}); got != nil {
		t.Errorf("Flush wrote %v while disabled, want nothing", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Flush created %d files while disabled, want 0", len(entries))
	}
	if summary.Len() != 0 {
		t.Errorf("Flush wrote a summary while disabled: %q", summary.String())
	}
}

// TestAssignLanesKeepsCompleteEventsNested pins the property the Chrome Trace
// format requires and the viewers reject a file for missing: within one lane,
// spans must nest, never partially overlap.
func TestAssignLanesKeepsCompleteEventsNested(t *testing.T) {
	events := []event{
		{Name: "outer", Start: 0, Dur: 100},
		{Name: "inner", Start: 10, Dur: 20},
		{Name: "overlap", Start: 50, Dur: 100}, // crosses outer's end
		{Name: "after", Start: 200, Dur: 10},
		{Name: "point", Start: 30, Dur: -1, Instant: true},
	}
	lanes := assignLanes(events)

	byLane := map[int][]event{}
	for i, e := range events {
		byLane[lanes[i]] = append(byLane[lanes[i]], e)
	}
	for lane, group := range byLane {
		for i := range group {
			for j := range group {
				if i == j {
					continue
				}
				a, b := group[i], group[j]
				aEnd, bEnd := spanEnd(a), spanEnd(b)
				// Disjoint, or one wholly inside the other.
				disjoint := aEnd <= b.Start || bEnd <= a.Start
				nested := (a.Start <= b.Start && bEnd <= aEnd) || (b.Start <= a.Start && aEnd <= bEnd)
				if !disjoint && !nested {
					t.Errorf("lane %d: %q [%d,%d) and %q [%d,%d) partially overlap",
						lane, a.Name, a.Start, aEnd, b.Name, b.Start, bEnd)
				}
			}
		}
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"lint":               "lint",
		"config runtime":     "config_runtime",
		"a/../../etc/passwd": "a_.._.._etc_passwd",
		"":                   "x",
		"...":                "x",
		"repo-name_1.0":      "repo-name_1.0",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitize(strings.Repeat("a", 200)); len(got) != 60 {
		t.Errorf("sanitize truncated to %d chars, want 60", len(got))
	}
	// A sanitized name must never be able to leave the trace directory.
	for _, in := range []string{"../x", "a/b", `a\b`} {
		if got := sanitize(in); strings.ContainsAny(got, `/\`) {
			t.Errorf("sanitize(%q) = %q, which still contains a separator", in, got)
		}
	}
}
