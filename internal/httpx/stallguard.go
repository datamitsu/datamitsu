package httpx

import (
	"context"
	"errors"
	"io"
	"time"
)

// DefaultStallWindow is how long a payload download may deliver ZERO bytes
// before its attempt is aborted (and retried by the caller's retry policy).
//
// This deliberately replaces flat end-to-end client deadlines for artifact
// payloads: any flat deadline encodes a hidden "size/speed < T" assumption
// that breaks either for large files or for slow links (a 400 MiB archive on
// a 1 Mbps VPN needs ~55 minutes and is perfectly healthy). A no-progress
// window is invariant to both — only a genuinely dead connection trips it.
// Two minutes rides out VPN hiccups and TCP retransmission storms, which
// matters because a retry restarts the download from scratch. A var so tests
// can shrink it.
var DefaultStallWindow = 2 * time.Minute

// ErrStalled marks an attempt aborted by a StallGuard. Match with errors.Is
// (also via context.Cause of the guarded context).
var ErrStalled = errors.New("download stalled: no data received")

// StallGuard cancels a context when no read progress happens for a window.
// Usage:
//
//	guard, ctx := httpx.NewStallGuard(ctx, httpx.DefaultStallWindow)
//	defer guard.Stop()
//	req, _ := http.NewRequestWithContext(ctx, ...)
//	_, err := io.Copy(dst, guard.Reader(resp.Body))
//	if guard.Stalled() { /* retryable: report the stall, not "context canceled" */ }
type StallGuard struct {
	cancel context.CancelCauseFunc
	//nolint:containedctx // held only to read context.Cause in Stalled()
	ctx    context.Context
	timer  *time.Timer
	window time.Duration
}

// NewStallGuard arms a guard over parent: the returned context is cancelled
// (cause ErrStalled) when window elapses with no Reset/Reader progress.
func NewStallGuard(parent context.Context, window time.Duration) (*StallGuard, context.Context) {
	ctx, cancel := context.WithCancelCause(parent)
	g := &StallGuard{cancel: cancel, ctx: ctx, window: window}
	g.timer = time.AfterFunc(window, func() { cancel(ErrStalled) })
	return g, ctx
}

// Reader wraps r so every successful read rearms the guard.
func (g *StallGuard) Reader(r io.Reader) io.Reader {
	return &stallGuardReader{guard: g, r: r}
}

// Reset manually rearms the guard (Reader does this automatically).
func (g *StallGuard) Reset() { g.timer.Reset(g.window) }

// Stalled reports whether the guard tripped.
func (g *StallGuard) Stalled() bool {
	return errors.Is(context.Cause(g.ctx), ErrStalled)
}

// Window returns the configured no-progress window.
func (g *StallGuard) Window() time.Duration { return g.window }

// Stop disarms the guard and releases the context.
func (g *StallGuard) Stop() {
	g.timer.Stop()
	g.cancel(nil)
}

type stallGuardReader struct {
	guard *StallGuard
	r     io.Reader
}

func (s *stallGuardReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.guard.Reset()
	}
	return n, err //nolint:wrapcheck // transparent reader passthrough
}
