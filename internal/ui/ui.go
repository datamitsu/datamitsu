// Package ui is datamitsu's single rendering authority for the terminal.
//
// The whole program shares ONE progress container per process. Any code that
// downloads an artifact or wants to print a status line goes through the
// process-wide "current" Display (see Current); when a progress bar is active,
// lines are printed safely ABOVE the bars instead of corrupting them. This
// replaces the previous arrangement where binmanager and runner each owned a
// separate *mpb.Progress and a dozen call sites wrote to os.Stdout/os.Stderr
// directly, fighting over the same terminal.
//
// The default current Display is a Plain passthrough, so any path that has not
// activated a Display (e.g. `datamitsu exec`) behaves exactly like plain
// line-based output with no regression. Entry points that own a render scope
// (the runner, `init`) call Activate to install an Interactive (TTY) or Plain
// (CI/pipe) Display for the duration and restore the previous one when done.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/term"

	"github.com/vbauerster/mpb/v8"
)

// Symbol is a status glyph rendered in the unified visual language.
type Symbol int

const (
	// SymStep marks an in-progress step ("→").
	SymStep Symbol = iota
	// SymOK marks success ("✓").
	SymOK
	// SymFail marks failure ("✗").
	SymFail
	// SymInfo marks informational output ("ℹ").
	SymInfo
	// SymWarn marks a warning ("!").
	SymWarn
	// SymDownload marks a download ("⬇").
	SymDownload
)

func (s Symbol) render() string {
	switch s {
	case SymStep:
		return clr.Faint("→")
	case SymOK:
		return clr.Green("✓")
	case SymFail:
		return clr.Red("✗")
	case SymInfo:
		return clr.Cyan("ℹ")
	case SymWarn:
		return clr.Yellow("!")
	case SymDownload:
		return clr.Cyan("⬇")
	}
	return clr.Faint("→")
}

// Display owns the terminal for a render scope. It is safe for concurrent use
// from multiple goroutines (parallel downloads, parallel tool execution).
type Display struct {
	mode term.Mode

	mu   sync.Mutex
	prog *mpb.Progress // lazily created; Interactive mode only, nil otherwise
	bars int           // number of active bars; lines route through mpb only while > 0
	out  io.Writer
	err  io.Writer
}

// New creates a Display for the given mode writing to stdout/stderr.
func New(mode term.Mode) *Display {
	return &Display{mode: mode, out: os.Stdout, err: os.Stderr}
}

// Mode reports the Display's render mode.
func (d *Display) Mode() term.Mode { return d.mode }

// Println prints a line to stdout (safe above active bars).
func (d *Display) Println(a ...any) { d.writeLine(d.out, fmt.Sprint(a...)) }

// Printf prints a formatted line to stdout (safe above active bars). A single
// trailing newline is normalized away.
func (d *Display) Printf(format string, a ...any) {
	d.writeLine(d.out, strings.TrimRight(fmt.Sprintf(format, a...), "\n"))
}

// Statusf prints a status line prefixed with a unified symbol.
func (d *Display) Statusf(sym Symbol, format string, a ...any) {
	d.writeLine(d.out, sym.render()+" "+fmt.Sprintf(format, a...))
}

// Header prints a prominent phase header (a leading blank line, then a bold
// title) so distinct phases of a run are easy to tell apart.
func (d *Display) Header(title string) {
	d.writeLine(d.out, "")
	d.writeLine(d.out, clr.Bold(clr.Cyan("▶ "+title)))
}

// Errorln prints a line to stderr (safe above active bars).
func (d *Display) Errorln(a ...any) { d.writeLine(d.err, fmt.Sprint(a...)) }

// Close waits for the progress container (if any) to finish and tears it down.
// It is safe to call multiple times; after Close the Display falls back to
// direct line output.
func (d *Display) Close() {
	d.mu.Lock()
	p := d.prog
	d.prog = nil
	d.mu.Unlock()
	if p != nil {
		p.Wait()
	}
}

// ensureProg lazily creates the single mpb container. Returns nil in Plain
// mode. Caller must hold d.mu.
func (d *Display) ensureProg() *mpb.Progress {
	if d.mode != term.Interactive {
		return nil
	}
	if d.prog == nil {
		d.prog = mpb.New(mpb.WithWidth(60), mpb.WithOutput(d.out))
	}
	return d.prog
}

// barEnded decrements the active-bar counter when a Download/Task finishes.
func (d *Display) barEnded() {
	d.mu.Lock()
	if d.bars > 0 {
		d.bars--
	}
	d.mu.Unlock()
}

// writeLine emits one line. Lines are routed through the mpb container (printed
// safely ABOVE the running bars) ONLY while at least one bar is active; with no
// active bar the line is written straight to w. Routing buffer-free when idle
// avoids the mpb-pending-write race that would otherwise drop a line when
// non-ui code writes directly to the same stream between bars.
func (d *Display) writeLine(w io.Writer, s string) {
	d.mu.Lock()
	p := d.prog
	active := d.bars > 0
	d.mu.Unlock()
	if p != nil && active {
		_, _ = p.Write([]byte(s + "\n"))
		return
	}
	_, _ = fmt.Fprintln(w, s)
}

// --- process-wide current Display ------------------------------------------

var (
	curMu   sync.RWMutex
	current = New(term.Plain) // safe passthrough until a scope activates one
)

// Current returns the process-wide active Display. It never returns nil.
func Current() *Display {
	curMu.RLock()
	defer curMu.RUnlock()
	return current
}

// Activate installs d as the current Display and returns a restore func that
// reinstates the previous one. Typical use:
//
//	d := ui.New(term.DetectMode())
//	restore := ui.Activate(d)
//	defer func() { d.Close(); restore() }()
func Activate(d *Display) (restore func()) {
	curMu.Lock()
	prev := current
	current = d
	curMu.Unlock()
	return func() {
		curMu.Lock()
		current = prev
		curMu.Unlock()
	}
}
