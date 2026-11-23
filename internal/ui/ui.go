// Package ui is datamitsu's single rendering authority for the terminal.
//
// A Display owns at most one progress container at a time. Any code that
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
	"time"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/provision"
	"github.com/datamitsu/datamitsu/internal/term"

	"github.com/mattn/go-runewidth"
	terminal "github.com/vbauerster/cupwriter"
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
	// SymSkip marks a skipped item ("⊘").
	SymSkip
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
	case SymSkip:
		return clr.Faint("⊘")
	}
	return clr.Faint("→")
}

// Display owns the terminal for a render scope. It is safe for concurrent use
// from multiple goroutines (parallel downloads, parallel tool execution).
type Display struct {
	mode  term.Mode
	quiet bool // suppress routine human output without changing typed event delivery

	mu            sync.Mutex
	prog          *mpb.Progress // lazily created; Interactive mode only
	bars          int           // number of active bars
	closing       bool
	provision     map[string]*provisionRow
	hiddenWaits   map[string]provision.InstallProgress
	waitSnapshots map[string]provision.InstallProgress
	provisionNext int
	out           io.Writer
	err           io.Writer
}

// New creates a Display for the given mode writing to stdout/stderr.
func New(mode term.Mode) *Display {
	return &Display{mode: mode, out: os.Stdout, err: os.Stderr}
}

// Mode reports the Display's render mode.
func (d *Display) Mode() term.Mode { return d.mode }

// SetQuiet suppresses routine progress and display lines for this display only.
func (d *Display) SetQuiet(quiet bool) { d.quiet = quiet }

// IsQuiet identifies command-local suppression, independently of JSONL and agent modes.
func (d *Display) IsQuiet() bool { return d.quiet }

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

// Banner identifies the CLI and resolved configuration once at the top of a run.
func (d *Display) Banner(name, version, configName, configVersion string) {
	width, _, err := terminal.New(d.out, false).GetTermSize()
	if err != nil || width < 1 {
		width = 80
	}
	for _, line := range bannerLines(name, version, configName, configVersion, width) {
		d.writeLine(d.out, line)
	}
}

func bannerLines(name, version, configName, configVersion string, width int) []string {
	product := name + "  " + version
	configuration := configName + "  " + configVersion
	box := func(label, release string, color func(...any) string) []string {
		space := max(1, width-4)
		if runewidth.StringWidth(release)+3 < space {
			label = runewidth.Truncate(label, space-runewidth.StringWidth(release)-2, "…")
		} else {
			label = runewidth.Truncate(label+"  "+release, space, "…")
			release = ""
		}
		content := label
		styled := clr.Bold(color(label))
		if release != "" {
			content += "  " + release
			styled += clr.Faint(color("  " + release))
		}
		rule := strings.Repeat("─", runewidth.StringWidth(content)+2)
		return []string{color("╭" + rule + "╮"), color("│ ") + styled + color(" │"), color("╰" + rule + "╯")}
	}
	if width < 5 {
		return []string{runewidth.Truncate(product, width, ""), runewidth.Truncate(configuration, width, "")}
	}
	cli := box(name, version, clr.Green)
	config := box(configName, configVersion, clr.Orange)
	if runewidth.StringWidth(product)+runewidth.StringWidth(configuration)+9 <= width {
		for i := range cli {
			cli[i] += " " + config[i]
		}
		return cli
	}
	return append(cli, config...)
}

// Errorln keeps captured failure details visible when routine status is quiet.
func (d *Display) Errorln(a ...any) { d.Diagnosticf("%s\n", fmt.Sprint(a...)) }

// Close waits for the progress container (if any) to finish and tears it down.
// It is safe to call multiple times; after Close the Display falls back to
// direct line output.
func (d *Display) Close() {
	d.mu.Lock()
	d.closing = true
	for id := range d.provision {
		d.detachProvisionRowLocked(id, false)
	}
	p := d.prog
	d.prog = nil
	d.mu.Unlock()
	if p != nil {
		p.Wait()
	}
}

// Diagnosticf preserves diagnostics unless a typed event stream or agent mode is active.
func (d *Display) Diagnosticf(format string, a ...any) {
	if Quiet() || Muted() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, _ = fmt.Fprintf(d.lineWriterLocked(d.err), format, a...)
}

// SlowStatus keeps fast paths quiet. Its stop function joins the writer before
// the caller can open a new frame or switch displays.
func (d *Display) SlowStatus(message string) func() {
	if d.IsQuiet() || Quiet() || Muted() {
		return func() {}
	}
	done := make(chan struct{})
	timer := time.AfterFunc(time.Second, func() {
		defer close(done)
		if d.mode == term.Interactive {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.bars == 0 && !d.closing {
				_, _ = fmt.Fprintln(d.lineWriterLocked(d.err), SymStep.render()+" "+message)
			}
			return
		}
		d.writeLine(d.err, SymStep.render()+" "+message)
	})
	return func() {
		if !timer.Stop() {
			<-done
		}
	}
}

// ensureProg lazily creates the single mpb container. Returns nil in Plain
// mode, and in JSON-L (quiet) mode so no animated bars compete with the typed
// stream even when stdout is a TTY. Caller must hold d.mu.
func (d *Display) ensureProg() *mpb.Progress {
	if d.closing || d.mode != term.Interactive || d.quiet || Muted() {
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
		d.drainIdleProgressLocked()
	}
	d.mu.Unlock()
}

// drainIdleProgressLocked waits for the final cursor movement and repaint before
// handing the terminal back to ordinary output. A retired container cannot be
// reused; the next group of bars creates a new one.
func (d *Display) drainIdleProgressLocked() {
	if d.prog != nil && d.bars == 0 {
		d.prog.Wait()
		d.prog = nil
	}
}

func (d *Display) lineWriterLocked(w io.Writer) io.Writer {
	d.drainIdleProgressLocked()
	if d.prog != nil {
		return d.prog
	}
	return w
}

// writeLine prints above active bars, or directly once the terminal is idle.
func (d *Display) writeLine(w io.Writer, s string) {
	// JSON-L mode, agent output, and command-local quiet mode suppress human lines.
	if d.quiet || Muted() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, _ = fmt.Fprintln(d.lineWriterLocked(w), s)
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
