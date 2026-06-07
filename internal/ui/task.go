package ui

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// Task tracks count-based progress (e.g. files processed) for a unit of work.
// In Interactive mode it renders a single bar in the shared container with a
// live label; in Plain mode it emits throttled "label n/total (pct%)" lines.
// Task is safe for concurrent use.
type Task struct {
	d     *Display
	total int64
	bar   *mpb.Bar // Interactive only

	label atomic.Value // string

	release sync.Once // releases the display's active-bar count once on Complete

	mu      sync.Mutex
	current int64
	lastPct int
	lastAt  time.Time
	done    bool
}

// Task creates a count-based progress task of `total` units with an initial
// label. total <= 0 renders an indeterminate counter.
func (d *Display) Task(label string, total int64) *Task {
	d.mu.Lock()
	prog := d.ensureProg()
	if prog != nil {
		d.bars++
	}
	d.mu.Unlock()

	t := &Task{d: d, total: total, lastPct: -1}
	t.label.Store(label)

	if prog != nil {
		t.bar = prog.AddBar(total,
			// Remove the bar once complete so a finished "tool [N/N]" line does not
			// linger above the results summary (matches download bars).
			mpb.BarRemoveOnComplete(),
			mpb.PrependDecorators(
				decor.Any(func(decor.Statistics) string {
					if s, ok := t.label.Load().(string); ok {
						return s
					}
					return ""
				}, decor.WC{W: 40, C: decor.DSyncWidthR}),
			),
			mpb.AppendDecorators(
				decor.CountersNoUnit(" %d / %d", decor.WCSyncSpace),
			),
		)
	}
	return t
}

// SetLabel updates the live label shown for the task.
func (t *Task) SetLabel(label string) {
	if t == nil {
		return
	}
	t.label.Store(label)
}

// Increment advances the task by one unit.
func (t *Task) Increment() { t.Add(1) }

// Add advances the task by n units.
func (t *Task) Add(n int64) {
	if t == nil {
		return
	}
	if t.bar != nil {
		t.bar.IncrBy(int(n))
	}
	t.mu.Lock()
	t.current += n
	t.mu.Unlock()
	t.maybeReport(false)
}

// Complete fills the task to its total and emits a final line (Plain mode).
func (t *Task) Complete() {
	if t == nil {
		return
	}
	if t.bar != nil {
		if t.total > 0 {
			t.bar.SetCurrent(t.total)
		} else {
			t.bar.SetTotal(-1, true)
		}
		t.release.Do(t.d.barEnded)
	}
	t.mu.Lock()
	if t.total > 0 {
		t.current = t.total
	}
	t.mu.Unlock()
	t.maybeReport(true)
}

func (t *Task) maybeReport(final bool) {
	// Interactive bars render themselves; only Plain mode emits lines.
	if t.bar != nil {
		return
	}

	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return
	}
	pct := -1
	if t.total > 0 {
		pct = int(t.current * 100 / t.total)
	}
	now := now()
	emit := false
	switch {
	case final:
		t.done = true
		emit = true
	case pct >= 0 && pct >= t.lastPct+plainStepPercent:
		emit = true
	case t.lastAt.IsZero() || now.Sub(t.lastAt) >= plainMinInterval:
		emit = true
	}
	if !emit {
		t.mu.Unlock()
		return
	}
	t.lastPct = pct
	t.lastAt = now
	cur, total := t.current, t.total
	label, _ := t.label.Load().(string)
	t.mu.Unlock()

	if total > 0 {
		t.d.Statusf(SymStep, "%s %d/%d (%d%%)", label, cur, total, cur*100/total)
	} else {
		t.d.Statusf(SymStep, "%s %d", label, cur)
	}
}
