package ui

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// now is overridable in tests for deterministic throttling.
var now = time.Now

// plainMinInterval and plainStepPercent control Plain-mode throttling: a
// progress line is emitted when EITHER at least plainMinInterval has elapsed OR
// progress advanced by plainStepPercent, whichever comes first (plus a final
// line at completion).
const (
	plainMinInterval = 2 * time.Second
	plainStepPercent = 10
)

// Download wraps r so that reading it reports progress for an artifact named
// `name` of `total` bytes (total <= 0 means unknown size). In Interactive mode
// it adds a bar to the shared container; in Plain mode it emits throttled
// percentage lines. The returned ReadCloser MUST be closed when the transfer
// finishes (this finalizes the bar). Closing does not close the underlying
// reader unless it implements io.Closer.
func (d *Display) Download(name string, total int64, r io.Reader) io.ReadCloser {
	d.mu.Lock()
	prog := d.ensureProg()
	if prog != nil {
		d.bars++
	}
	d.mu.Unlock()

	if prog != nil {
		bar := prog.AddBar(total,
			mpb.BarRemoveOnComplete(),
			mpb.PrependDecorators(
				decor.Name(name, decor.WC{W: 24, C: decor.DSyncWidthR}),
				// Reserve a fixed, sync-aligned column for the byte counters.
				// Without it the bar shifts horizontally whenever the digit count
				// changes (e.g. 9 → 43 → 103 MiB) or when parallel bars show
				// values of differing width — producing a jittery staircase.
				// W fits "1023.99 MiB / 1023.99 MiB"; DSyncWidthR keeps every bar
				// aligned to the same column.
				decor.CountersKibiByte("% .2f / % .2f", decor.WC{W: 25, C: decor.DSyncWidthR}),
			),
			mpb.AppendDecorators(
				// %3.0f keeps the percentage 3-wide ("  8" / " 83" / "100") so the
				// trailing speed never shifts as it crosses 10% and 100%.
				decor.NewPercentage(" %3.0f ", decor.WCSyncSpace),
				decor.EwmaSpeed(decor.SizeB1024(0), " % .2f", 60),
			),
		)
		return &barReader{ReadCloser: bar.ProxyReader(r), d: d}
	}

	return &plainDownload{d: d, name: name, total: total, r: r, lastPct: -1}
}

// barReader wraps an mpb ProxyReader to release the display's active-bar count
// exactly once when the transfer is closed.
type barReader struct {
	io.ReadCloser

	d    *Display
	once sync.Once
}

func (b *barReader) Close() error {
	b.once.Do(b.d.barEnded)
	return b.ReadCloser.Close() //nolint:wrapcheck // passthrough of mpb proxy reader
}

// plainDownload reports download progress as throttled append-only lines.
type plainDownload struct {
	d     *Display
	name  string
	total int64
	r     io.Reader

	mu      sync.Mutex
	read    int64
	lastPct int
	lastAt  time.Time
	done    bool
}

func (p *plainDownload) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.mu.Lock()
		p.read += int64(n)
		p.mu.Unlock()
		p.maybeReport(false)
	}
	if err == io.EOF {
		p.maybeReport(true)
	}
	return n, err //nolint:wrapcheck // passthrough of underlying reader error (incl. io.EOF) must not be wrapped
}

func (p *plainDownload) Close() error {
	p.maybeReport(true)
	if c, ok := p.r.(io.Closer); ok {
		return c.Close() //nolint:wrapcheck // passthrough of underlying closer
	}
	return nil
}

func (p *plainDownload) maybeReport(final bool) {
	p.mu.Lock()
	if p.done {
		p.mu.Unlock()
		return
	}

	pct := -1
	if p.total > 0 {
		pct = int(p.read * 100 / p.total)
	}

	t := now()
	emit := false
	switch {
	case final:
		p.done = true
		emit = true
	case pct >= 0 && pct >= p.lastPct+plainStepPercent:
		emit = true
	case p.lastAt.IsZero() || t.Sub(p.lastAt) >= plainMinInterval:
		emit = true
	}
	if !emit {
		p.mu.Unlock()
		return
	}
	p.lastPct = pct
	p.lastAt = t
	line := p.format(final)
	p.mu.Unlock()

	p.d.Statusf(SymDownload, "%s", line)
}

func (p *plainDownload) format(final bool) string {
	if final {
		return p.name + " done"
	}
	if p.total > 0 {
		return fmt.Sprintf("%s %d%%", p.name, p.read*100/p.total)
	}
	return fmt.Sprintf("%s %.1f MiB", p.name, float64(p.read)/(1024*1024))
}
