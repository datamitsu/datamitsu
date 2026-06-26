package uievent

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// throttleInterval is the minimum spacing between consecutive progress events
// for the SAME op_id. download/chunk streams can fire thousands of updates per
// second; without this a consumer would be flooded. Terminal events (start/done/
// fail) and low-frequency types (phase/install/tool_run/error/done) are never
// throttled — only Status==progress on download/chunk is.
const throttleInterval = 100 * time.Millisecond

// JSONLSink serializes events as newline-delimited JSON to an io.Writer (one
// JSON object per line). It is safe for concurrent use.
type JSONLSink struct {
	mu  sync.Mutex
	enc *json.Encoder

	// nowFn is overridable in tests for deterministic throttling.
	nowFn func() time.Time

	// lastProgress tracks the last emitted progress timestamp per op_id, used to
	// drop sub-interval progress updates. Entries are removed on the op's terminal
	// event so the map stays bounded over a long run.
	lastProgress map[string]time.Time
}

// NewJSONLSink returns a sink writing JSON-L to w (typically os.Stderr).
func NewJSONLSink(w io.Writer) *JSONLSink {
	return &JSONLSink{
		enc:          json.NewEncoder(w),
		nowFn:        time.Now,
		lastProgress: make(map[string]time.Time),
	}
}

// Emit stamps the event timestamp, applies per-op_id throttling to progress
// events, and writes the survivors as one JSON line each.
func (s *JSONLSink) Emit(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nowFn()
	e.TS = now.UnixMilli()

	if s.throttled(e, now) {
		return
	}

	// Terminal events end a chain — forget the op's throttle state.
	if e.Status == StatusDone || e.Status == StatusFail {
		delete(s.lastProgress, e.OpID)
	}

	//nolint:errchkjson // Event has only JSON-encodable fields; a broken stderr is unactionable
	_ = s.enc.Encode(e)
}

// throttled reports whether e is a progress update for download/chunk that
// arrived within throttleInterval of the previous one for the same op_id. When
// it is NOT throttled it records the timestamp. Caller holds s.mu.
func (s *JSONLSink) throttled(e Event, now time.Time) bool {
	if e.Status != StatusProgress {
		return false
	}
	if e.Type != TypeDownload && e.Type != TypeChunk {
		return false
	}
	if last, ok := s.lastProgress[e.OpID]; ok && now.Sub(last) < throttleInterval {
		return true
	}
	s.lastProgress[e.OpID] = now
	return false
}
