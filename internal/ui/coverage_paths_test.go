package ui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/term"
)

// newInteractiveDisplay builds an Interactive-mode Display whose mpb container
// renders into the given buffer (no TTY required), exercising the bar-backed
// branches of Task/Spinner/Download without asserting on escape bytes.
func newInteractiveDisplay(buf io.Writer) *Display {
	return &Display{mode: term.Interactive, out: buf, err: buf}
}

func TestSymbolRenderAllBranches(t *testing.T) {
	tests := []struct {
		sym  Symbol
		want string
	}{
		{SymStep, "→"},
		{SymOK, "✓"},
		{SymFail, "✗"},
		{SymInfo, "ℹ"},
		{SymWarn, "!"},
		{SymDownload, "⬇"},
		{SymSkip, "⊘"},
		{Symbol(999), "→"}, // out-of-range falls through to the default arrow
	}
	for _, tt := range tests {
		if got := tt.sym.render(); !strings.Contains(got, tt.want) {
			t.Errorf("Symbol(%d).render() = %q, want it to contain %q", tt.sym, got, tt.want)
		}
	}
}

func TestModeReportsConfiguredMode(t *testing.T) {
	if m := New(term.Plain).Mode(); m != term.Plain {
		t.Errorf("Mode() = %v, want Plain", m)
	}
	if m := New(term.Interactive).Mode(); m != term.Interactive {
		t.Errorf("Mode() = %v, want Interactive", m)
	}
}

func TestPrintfTrimsTrailingNewline(t *testing.T) {
	var out bytes.Buffer
	d := newPlainDisplay(&out, &out)

	d.Printf("value=%d\n", 7)

	got := out.String()
	if !strings.Contains(got, "value=7") {
		t.Errorf("Printf missing content: %q", got)
	}
	// Printf normalizes away a single trailing newline before writeLine adds its
	// own, so the output must not contain a doubled blank line.
	if strings.Contains(got, "value=7\n\n") {
		t.Errorf("Printf did not trim trailing newline: %q", got)
	}
}

func TestHeaderEmitsBlankLineAndTitle(t *testing.T) {
	var out bytes.Buffer
	d := newPlainDisplay(&out, &out)

	d.Header("Build")

	got := out.String()
	if !strings.Contains(got, "Build") {
		t.Errorf("Header missing title: %q", got)
	}
	// A leading blank line precedes the title.
	if !strings.HasPrefix(got, "\n") {
		t.Errorf("Header missing leading blank line: %q", got)
	}
}

func TestBannerIncludesNameAndVersion(t *testing.T) {
	var out bytes.Buffer
	d := newPlainDisplay(&out, &out)

	d.Banner("datamitsu", "v1.2.3")

	got := out.String()
	if !strings.Contains(got, "datamitsu") || !strings.Contains(got, "v1.2.3") {
		t.Errorf("Banner missing name/version: %q", got)
	}
	// The rounded box corners frame the content.
	if !strings.Contains(got, "╭") || !strings.Contains(got, "╰") {
		t.Errorf("Banner missing box corners: %q", got)
	}
}

func TestPhaseOpenBodyClose(t *testing.T) {
	var out bytes.Buffer
	d := newPlainDisplay(&out, &out)

	d.PhaseOpen("Lint")
	d.PhaseBody("running eslint")
	d.PhaseBody("") // bare spacer line
	d.PhaseClose("Lint OK", "Lint OK")

	got := out.String()
	for _, want := range []string{"Lint", "running eslint", "Lint OK"} {
		if !strings.Contains(got, want) {
			t.Errorf("phase output missing %q: %q", want, got)
		}
	}
	if !strings.HasPrefix(got, "\n") {
		t.Errorf("PhaseOpen missing leading blank line: %q", got)
	}
}

func TestTaskIncrementPlain(t *testing.T) {
	var out bytes.Buffer
	d := newPlainDisplay(&out, &out)

	orig := now
	now = func() time.Time { return time.Unix(0, 0) }
	defer func() { now = orig }()

	task := d.Task("scan", 3)
	task.Increment()
	task.Increment()
	task.Complete()

	got := out.String()
	if !strings.Contains(got, "scan") {
		t.Errorf("task output missing label: %q", got)
	}
	if !strings.Contains(got, "3/3") {
		t.Errorf("task output missing completion: %q", got)
	}
}

// TestTaskIndeterminatePlain covers the total <= 0 branch of Complete/maybeReport
// (no percentage, bare counter).
func TestTaskIndeterminatePlain(t *testing.T) {
	var out bytes.Buffer
	d := newPlainDisplay(&out, &out)

	orig := now
	now = func() time.Time { return time.Unix(0, 0) }
	defer func() { now = orig }()

	task := d.Task("indexing", 0)
	task.Add(5)
	task.Complete()

	got := out.String()
	if !strings.Contains(got, "indexing") {
		t.Errorf("indeterminate task missing label: %q", got)
	}
	// No "/" counter for indeterminate totals.
	if strings.Contains(got, "5/") {
		t.Errorf("indeterminate task should not show a total: %q", got)
	}
}

// TestNilReceiversNoPanic locks in the nil-safe guards used by callers that hold
// an optional *Task / *Spinner.
func TestNilReceiversNoPanic(t *testing.T) {
	var task *Task
	task.SetLabel("x")
	task.Increment()
	task.Add(2)
	task.Complete()

	var sp *Spinner
	sp.SetDetail("x")
	sp.Done("x")
	sp.Fail()
}

func TestSpinnerPlainLifecycle(t *testing.T) {
	var out bytes.Buffer
	d := newPlainDisplay(&out, &out)

	clock := time.Unix(0, 0)
	orig := now
	now = func() time.Time { return clock }
	defer func() { now = orig }()

	sp := d.Spinner("Installing eslint")
	// Within the throttle interval: SetDetail must NOT emit a line.
	sp.SetDetail("100 pkgs")
	if strings.Contains(out.String(), "100 pkgs") {
		t.Errorf("SetDetail emitted within throttle window: %q", out.String())
	}
	// Advance past the throttle interval: now it emits.
	clock = clock.Add(plainMinInterval + time.Second)
	sp.SetDetail("200 pkgs")
	if !strings.Contains(out.String(), "200 pkgs") {
		t.Errorf("SetDetail did not emit after throttle window: %q", out.String())
	}

	sp.Done("Installed eslint")
	got := out.String()
	if !strings.Contains(got, "Installing eslint") {
		t.Errorf("spinner missing name announce: %q", got)
	}
	if !strings.Contains(got, "Installed eslint") {
		t.Errorf("spinner missing final line: %q", got)
	}
}

// TestSpinnerDoneEmptyAndFail covers Done("") (no final line) and Fail().
func TestSpinnerDoneEmptyAndFail(t *testing.T) {
	var out bytes.Buffer
	d := newPlainDisplay(&out, &out)

	orig := now
	now = func() time.Time { return time.Unix(0, 0) }
	defer func() { now = orig }()

	d.Spinner("step-a").Done("") // no success line
	d.Spinner("step-b").Fail()

	got := out.String()
	if !strings.Contains(got, "step-a") || !strings.Contains(got, "step-b") {
		t.Errorf("spinner name announces missing: %q", got)
	}
	// Done("") must not print a ✓ success line for step-a.
	if strings.Contains(got, "✓") {
		t.Errorf("Done(\"\") should not emit a success line: %q", got)
	}
}

// --- Interactive (bar-backed) branches --------------------------------------

func TestInteractiveTaskLifecycle(t *testing.T) {
	var buf bytes.Buffer
	d := newInteractiveDisplay(&buf)

	task := d.Task("work", 4)
	task.SetLabel("work (eslint)")
	task.Increment()
	task.Add(2)
	task.Complete()
	d.Close()
	// Bar bookkeeping must have released the active-bar count.
	d.mu.Lock()
	bars := d.bars
	d.mu.Unlock()
	if bars != 0 {
		t.Errorf("active bars not released: %d", bars)
	}
}

// TestInteractiveTaskIndeterminate covers Complete's SetTotal(-1) branch for an
// indeterminate (total <= 0) interactive task.
func TestInteractiveTaskIndeterminate(t *testing.T) {
	var buf bytes.Buffer
	d := newInteractiveDisplay(&buf)

	task := d.Task("indef", 0)
	task.Add(3)
	task.Complete()
	d.Close()
	// Completing an indeterminate task must still release its active bar.
	d.mu.Lock()
	bars := d.bars
	d.mu.Unlock()
	if bars != 0 {
		t.Errorf("indeterminate task bar not released: %d", bars)
	}
}

func TestInteractiveSpinnerLifecycle(t *testing.T) {
	var buf bytes.Buffer
	d := newInteractiveDisplay(&buf)

	sp := d.Spinner("Installing")
	sp.SetDetail("50 pkgs") // bar != nil: stores detail, no emit
	sp.Done("Installed")

	sp2 := d.Spinner("Building")
	sp2.Fail()

	d.Close()
	d.mu.Lock()
	bars := d.bars
	d.mu.Unlock()
	if bars != 0 {
		t.Errorf("active bars not released after spinner lifecycle: %d", bars)
	}
}

// TestInteractiveDownloadAbortReleasesBar drives the barReader.Close path: a
// download closed before reaching total must abort the bar and release the
// active-bar count so Close does not hang.
func TestInteractiveDownloadAbortReleasesBar(t *testing.T) {
	var buf bytes.Buffer
	d := newInteractiveDisplay(&buf)

	src := strings.NewReader(strings.Repeat("a", 1000))
	rc := d.Download("artifact", 1000, src)
	// Read only part of it, then close (aborted transfer).
	one := make([]byte, 10)
	if _, err := rc.Read(one); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Double close must be a no-op (sync.Once) and not double-decrement.
	_ = rc.Close()

	d.Close()
	d.mu.Lock()
	bars := d.bars
	d.mu.Unlock()
	if bars != 0 {
		t.Errorf("download bar not released: %d", bars)
	}
}
