package ui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/term"
)

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
