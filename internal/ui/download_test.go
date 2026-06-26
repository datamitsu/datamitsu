package ui

import (
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// captureSink records emitted events for assertions.
type captureSink struct {
	mu     sync.Mutex
	events []uievent.Event
}

func (c *captureSink) Emit(e uievent.Event) {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
}

func (c *captureSink) lastDownloadStatus() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range slices.Backward(c.events) {
		if v.Type == uievent.TypeDownload {
			return v.Status
		}
	}
	return ""
}

// errReader yields data then a non-EOF error, simulating an aborted transfer.
type errReader struct {
	data []byte
	pos  int
	err  error
}

func (r *errReader) Read(b []byte) (int, error) {
	if r.pos < len(r.data) {
		n := copy(b, r.data[r.pos:])
		r.pos += n
		return n, nil
	}
	return 0, r.err
}

func TestDownloadEmitsDoneOnEOF(t *testing.T) {
	cs := &captureSink{}
	SetEventSink(cs, true)
	defer SetEventSink(nil, false)

	d := New(term.Plain)
	rc := d.Download("artifact", 4, strings.NewReader("data"))
	if _, err := io.ReadAll(rc); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	_ = rc.Close()

	if got := cs.lastDownloadStatus(); got != uievent.StatusDone {
		t.Errorf("terminal download status = %q, want %q", got, uievent.StatusDone)
	}
}

func TestDownloadEmitsFailOnAbort(t *testing.T) {
	cs := &captureSink{}
	SetEventSink(cs, true)
	defer SetEventSink(nil, false)

	d := New(term.Plain)
	rc := d.Download("artifact", 100, &errReader{data: []byte("part"), err: errors.New("boom")})
	// Drain until the non-EOF error surfaces (no done emitted), then close.
	_, _ = io.ReadAll(rc)
	_ = rc.Close()

	if got := cs.lastDownloadStatus(); got != uievent.StatusFail {
		t.Errorf("terminal download status = %q, want %q (aborted transfer must not report done)", got, uievent.StatusFail)
	}
}

// TestInteractiveCloseOfPartialDownloadDoesNotBlockDisplayClose pins the
// zombie-bar fix: a download closed mid-transfer (failed attempt) must abort
// its bar, otherwise Display.Close blocks forever on the incomplete bar and a
// failing command freezes instead of printing its error.
func TestInteractiveCloseOfPartialDownloadDoesNotBlockDisplayClose(t *testing.T) {
	d := New(term.Interactive)
	d.out = io.Discard
	d.err = io.Discard

	reader := d.Download("partial", 100, strings.NewReader("only-a-little"))
	buf := make([]byte, 4)
	if _, err := reader.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	done := make(chan struct{})
	go func() {
		d.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Display.Close blocked on an incomplete (aborted) download bar")
	}
}
