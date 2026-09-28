package ui

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/uievent"
)

// Every event passes through the masker before the sink writes it, and
// removing the masker leaves events as they are.
func TestSetEventMask(t *testing.T) {
	cs := &captureSink{}
	SetEventSink(cs, true)
	t.Cleanup(func() { SetEventSink(nil, false) })
	SetEventMask(func(e *uievent.Event) {
		e.OpID = strings.ReplaceAll(e.OpID, "secret", "***")
		e.Dir = strings.ReplaceAll(e.Dir, "secret", "***")
	})
	t.Cleanup(func() { SetEventMask(nil) })

	Emit(uievent.Event{Type: uievent.TypeToolRun, OpID: "run-1:t:secret:1", Dir: "secret"})
	SetEventMask(nil)
	Emit(uievent.Event{Type: uievent.TypeToolRun, OpID: "run-1:t:secret:2", Dir: "secret"})

	if len(cs.events) != 2 {
		t.Fatalf("events = %+v, want two", cs.events)
	}
	if e := cs.events[0]; e.OpID != "run-1:t:***:1" || e.Dir != "***" {
		t.Errorf("masked event = %+v", e)
	}
	if e := cs.events[1]; e.OpID != "run-1:t:secret:2" {
		t.Errorf("an event after the masker was removed = %+v", e)
	}
}

// Muted output drops human lines without a stream in their place, and is not
// JSON-L mode: Quiet stays false.
func TestSetMuted(t *testing.T) {
	var out, errOut strings.Builder
	d := newPlainDisplay(&out, &errOut)
	SetMuted(true)
	t.Cleanup(func() { SetMuted(false) })
	d.Println("human")
	d.Errorln("human")
	if !Muted() || Quiet() || out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("Muted %v, Quiet %v, wrote %q %q; want muted, not quiet, nothing written", Muted(), Quiet(), out.String(), errOut.String())
	}
	SetMuted(false)
	d.Println("human")
	if Muted() || out.String() != "human\n" {
		t.Errorf("after SetMuted(false): Muted %v, wrote %q", Muted(), out.String())
	}
}

// With no sink nothing is masked or written.
func TestEmitWithoutSink(t *testing.T) {
	called := false
	SetEventMask(func(*uievent.Event) { called = true })
	t.Cleanup(func() { SetEventMask(nil) })
	Emit(uievent.Event{Type: uievent.TypeLog, OpID: "log-1"})
	if called {
		t.Error("the masker ran without a sink")
	}
}
