// Package uievent defines datamitsu's structured status-event vocabulary and
// the JSON-L sink that serializes it.
//
// It is a leaf package: it imports nothing from internal/ui or internal/runner,
// so both of those (and anything else that wants to emit status) can depend on
// it without an import cycle. The human renderer (internal/ui) and the typed
// JSON-L stream are two consumers of the SAME semantic events — emitters call
// the ui primitives as before, and ui forwards a typed Event to the active Sink
// when one is installed.
//
// Every event carries a discriminator Type and a correlation OpID; both are
// mandatory on every line so a consumer can mechanically separate and stitch
// "start X -> progress X -> X done" chains. All other fields are omitempty so a
// line only carries what its type needs.
package uievent

import "sync/atomic"

// Type is the discriminator carried on every event line.
type Type string

const (
	// TypePhase marks the start/end of an operation phase (fix, lint, ...).
	TypePhase Type = "phase"
	// TypeDownload marks artifact download progress (binary/runtime/parser).
	TypeDownload Type = "download"
	// TypeInstall marks a per-app install boundary (node/uv/go/pnpm/extract).
	TypeInstall Type = "install"
	// TypeChunk marks per-unit work progress (files processed by a tool).
	TypeChunk Type = "chunk"
	// TypeToolRun marks the start/end of a single tool execution.
	TypeToolRun Type = "tool_run"
	// TypeError marks an error surfaced during an operation.
	TypeError Type = "error"
	// TypeDone marks the completion of an operation, with a summary.
	TypeDone Type = "done"
)

// Status values for the optional Status field.
const (
	// StatusStart marks the beginning of a correlated chain.
	StatusStart = "start"
	// StatusProgress marks a rate-limited intermediate update.
	StatusProgress = "progress"
	// StatusDone marks successful completion of a chain.
	StatusDone = "done"
	// StatusFail marks failed completion of a chain.
	StatusFail = "fail"
)

// Event is the single envelope written as one JSON object per line. Type and
// OpID are always set; everything else is optional. A flat envelope (rather than
// a struct per type) is intentional: events are emitted from many call sites
// that each populate only a few fields, and consumers discriminate on Type.
type Event struct {
	Type   Type   `json:"type"`             // discriminator, always set
	OpID   string `json:"op_id"`            // correlation id, always set
	TS     int64  `json:"ts"`               // unix milliseconds
	Status string `json:"status,omitempty"` // start | progress | done | fail

	// Identity.
	Op   string `json:"op,omitempty"`   // operation name for phase/done (fix, lint)
	Tool string `json:"tool,omitempty"` // tool name for tool_run/chunk/error
	Dir  string `json:"dir,omitempty"`  // project-relative dir for tool_run/chunk
	Name string `json:"name,omitempty"` // artifact/app name for download/install

	// Progress (download/chunk).
	BytesDone  int64 `json:"bytes_done,omitempty"`
	BytesTotal int64 `json:"bytes_total,omitempty"`
	Percent    int   `json:"percent,omitempty"`
	Index      int   `json:"index,omitempty"`
	Total      int   `json:"total,omitempty"`

	// Outcome. Success is a pointer so a failed run (success:false) is
	// distinguishable from an event that carries no success field at all.
	Success    *bool  `json:"success,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Msg        string `json:"msg,omitempty"` // error / diagnostic text

	// done summary.
	Tools   int `json:"tools,omitempty"`
	Runs    int `json:"runs,omitempty"`
	Failed  int `json:"failed,omitempty"`
	Skipped int `json:"skipped,omitempty"`
}

// Sink consumes typed events. Implementations MUST be safe for concurrent use
// from multiple goroutines (parallel downloads, parallel tool execution).
type Sink interface {
	Emit(e Event)
}

// opCounter backs NextOpID. A plain monotonic counter is sufficient: op ids are
// correlation handles within a single process run, never compared against any
// external value, so no hashing is warranted here.
var opCounter atomic.Uint64

// NextOpID returns a fresh, process-unique correlation id with the given prefix
// (e.g. "run", "dl", "inst"). Concurrency-safe.
func NextOpID(prefix string) string {
	n := opCounter.Add(1)
	return prefix + "-" + formatUint(n)
}

// formatUint renders n in base 10 without importing strconv (keeps this leaf package
// dependency-free; the values are small).
func formatUint(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
