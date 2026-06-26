package ui

import (
	"sync"

	"github.com/datamitsu/datamitsu/internal/uievent"
)

// The typed event sink is process-global rather than a field on a single
// Display: scoped entry points (the runner, init) install their OWN Display via
// Activate for the duration of an operation, which would otherwise shadow a sink
// hung off the root Display exactly while downloads/installs happen. A global
// sink survives those Display swaps, so every Display's primitives forward to the
// same stream.
var (
	eventMu   sync.RWMutex
	eventSink uievent.Sink
	quiet     bool
)

// SetEventSink installs a process-global typed event sink and toggles quiet mode.
// When quiet is true, human line output is suppressed — writeLine drops lines and
// no mpb progress container is created (even in a TTY) — so the typed stream is
// the only output and stdout stays clean for commands that emit machine data
// there. Pass a nil sink to disable and restore normal human rendering.
func SetEventSink(s uievent.Sink, quietMode bool) {
	eventMu.Lock()
	eventSink = s
	quiet = quietMode && s != nil
	eventMu.Unlock()
}

// Emit forwards e to the active event sink, if any. It is a no-op when no sink is
// installed, so emitters may call it unconditionally at negligible cost.
func Emit(e uievent.Event) {
	eventMu.RLock()
	s := eventSink
	eventMu.RUnlock()
	if s != nil {
		s.Emit(e)
	}
}

// Quiet reports whether human line output is currently suppressed (JSON-L mode).
// Callers that print directly via fmt (bypassing Display) consult this to stay
// silent so the typed event stream is the sole output.
func Quiet() bool {
	eventMu.RLock()
	defer eventMu.RUnlock()
	return quiet
}

// sinkActive reports whether a typed event sink is installed. Used by emitters
// to skip building events (and wrapping readers) when nobody is listening.
func sinkActive() bool {
	eventMu.RLock()
	defer eventMu.RUnlock()
	return eventSink != nil
}
