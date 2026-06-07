package ui

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// spinnerNameWidth is the reserved minimum width of the name column. The detail
// (counters) starts after it, so concurrent install rows line up in a grid.
// A fixed reservation is used instead of mpb's cross-bar width sync, which does
// not reliably align dynamically added/removed spinners; names longer than this
// simply push their own detail right (that row only).
const spinnerNameWidth = 40

// Spinner shows indeterminate activity with a fixed name and a live, updatable
// detail (e.g. install counters) — used for install steps whose total work is
// unknown up front (pnpm/uv/go). In Interactive mode it renders an animated
// spinner in the shared container; the name is a width-synced column so the
// detail of every concurrent spinner lines up in the same place (grid
// alignment). In Plain mode it emits the name, throttled detail lines, and a
// final line.
type Spinner struct {
	d    *Display
	bar  *mpb.Bar // Interactive only; nil in Plain
	name string

	detail  atomic.Value // string
	release sync.Once

	mu     sync.Mutex
	lastAt time.Time
}

// Spinner starts an activity spinner with a fixed name (e.g. "Installing eslint").
func (d *Display) Spinner(name string) *Spinner {
	d.mu.Lock()
	prog := d.ensureProg()
	if prog != nil {
		d.bars++
	}
	d.mu.Unlock()

	s := &Spinner{d: d, name: name}
	s.detail.Store("")
	// Seed the throttle clock so the first detail update (Plain mode) waits a full
	// interval rather than emitting immediately on top of the name line — fast
	// installs then show just the name + final line, slow ones update.
	s.lastAt = now()

	if prog != nil {
		s.bar = prog.AddSpinner(0,
			mpb.BarWidth(1), // keep the frame to one cell; columns follow it
			mpb.BarRemoveOnComplete(),
			mpb.AppendDecorators(
				// Reserve a fixed minimum width for the name (left-aligned via
				// DindentRight) so the detail column lines up across concurrent rows.
				decor.Name(" "+name, decor.WC{W: spinnerNameWidth, C: decor.DindentRight}),
				decor.Any(func(decor.Statistics) string {
					if v, ok := s.detail.Load().(string); ok && v != "" {
						return "  " + v
					}
					return ""
				}),
			),
		)
		return s
	}

	// Plain mode: announce the step once.
	d.Statusf(SymStep, "%s", name)
	return s
}

// SetDetail updates the live detail shown after the name column (e.g. progress
// counters). In Interactive mode the change is picked up on the next render; in
// Plain mode it is emitted as a throttled line.
func (s *Spinner) SetDetail(detail string) {
	if s == nil {
		return
	}
	s.detail.Store(detail)
	if s.bar != nil {
		return
	}
	s.mu.Lock()
	t := now()
	emit := t.Sub(s.lastAt) >= plainMinInterval
	if emit {
		s.lastAt = t
	}
	s.mu.Unlock()
	if emit {
		// Pad the name to the same reserved width so Plain-mode rows align too.
		s.d.Statusf(SymStep, "%-*s  %s", spinnerNameWidth, s.name, detail)
	}
}

// Done completes the spinner (removing it in Interactive mode) and, if final is
// non-empty, prints a success line.
func (s *Spinner) Done(final string) {
	if s == nil {
		return
	}
	if s.bar != nil {
		s.bar.SetTotal(-1, true) // mark the indeterminate spinner complete
		s.release.Do(s.d.barEnded)
	}
	if final != "" {
		s.d.Statusf(SymOK, "%s", final)
	}
}

// Fail completes the spinner without a success line (the caller surfaces the
// error separately).
func (s *Spinner) Fail() {
	if s == nil {
		return
	}
	if s.bar != nil {
		s.bar.SetTotal(-1, true)
		s.release.Do(s.d.barEnded)
	}
}
