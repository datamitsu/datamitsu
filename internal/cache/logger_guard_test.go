package cache

import (
	"testing"
	"time"
)

// A Cache built as a bare struct literal has no logger. debounceSave logs its
// failures from a timer goroutine, and a panic there is unrecoverable — it
// takes the process down rather than failing one operation. Both must survive
// a nil logger.
func TestDebouncedSaveSurvivesANilLogger(t *testing.T) {
	c := &Cache{data: &File{Verdicts: map[string]VerdictEntry{}}}
	t.Cleanup(c.Shutdown)

	// An empty path makes the save itself fail, which is what reaches the
	// logging branch this test exists to cover.
	c.AfterVerdict("k", VerdictEntry{InputHash: "h", ValidatedAt: time.Now()})

	// Outlive the 100ms debounce so the timer goroutine runs while the test is
	// still here to observe a panic.
	time.Sleep(300 * time.Millisecond)

	if got := len(c.data.Verdicts); got != 1 {
		t.Errorf("stored %d verdict(s), want 1", got)
	}
}

// Shutdown is how a caller stops the debounce timer, so it has to be callable
// on a Cache that never went through NewCache and therefore has no shutdown
// channel — closing a nil channel panics.
func TestShutdownIsSafeWithoutAShutdownChannel(t *testing.T) {
	c := &Cache{data: &File{Verdicts: map[string]VerdictEntry{}}}

	c.Shutdown()
	c.Shutdown() // idempotent via shutdownOnce
}

// The no-op fallback must never be nil, or every logging site trades one panic
// for another.
func TestLogFallsBackToANonNilLogger(t *testing.T) {
	if (&Cache{}).log() == nil {
		t.Error("log() returned nil for a Cache with no logger")
	}
}
