package ui

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// newPlainDisplay builds a Plain-mode Display writing to the given buffers.
func newPlainDisplay(out, errOut io.Writer) *Display {
	return &Display{mode: term.Plain, out: out, err: errOut}
}

func TestPlainPrintlnAndStatus(t *testing.T) {
	var out, errOut bytes.Buffer
	d := newPlainDisplay(&out, &errOut)

	d.Println("hello")
	d.Statusf(SymOK, "done %s", "x")
	d.Errorln("boom")

	if !strings.Contains(out.String(), "hello") {
		t.Errorf("Println missing: %q", out.String())
	}
	if !strings.Contains(out.String(), "done x") {
		t.Errorf("Status missing: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "boom") {
		t.Errorf("Errorln missing: %q", errOut.String())
	}
}

func TestSymSkipRender(t *testing.T) {
	got := SymSkip.render()
	if !strings.Contains(got, "⊘") {
		t.Errorf("SymSkip.render() = %q, want it to contain ⊘", got)
	}
}

func TestPlainDownloadEmitsFinalLine(t *testing.T) {
	var out bytes.Buffer
	d := newPlainDisplay(&out, &out)

	// Freeze the clock so only the final "done" line is emitted.
	orig := now
	now = func() time.Time { return time.Unix(0, 0) }
	defer func() { now = orig }()

	src := strings.NewReader(strings.Repeat("a", 1000))
	rc := d.Download("artifact", 1000, src)
	if _, err := io.Copy(io.Discard, rc); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if !strings.Contains(out.String(), "artifact done") {
		t.Errorf("expected final download line, got: %q", out.String())
	}
}

func TestPlainDownloadThrottlesByPercent(t *testing.T) {
	var out bytes.Buffer
	d := newPlainDisplay(&out, &out)

	// Freeze time so only percent-based steps (>=10%) trigger emissions.
	orig := now
	now = func() time.Time { return time.Unix(100, 0) }
	defer func() { now = orig }()

	src := strings.NewReader(strings.Repeat("a", 100))
	rc := d.Download("art", 100, src)
	buf := make([]byte, 1) // 1 byte == 1% per read
	for {
		if _, err := rc.Read(buf); err != nil {
			break
		}
	}
	_ = rc.Close()

	// 1%..99% reads should not each print; emissions are gated to ~10% steps
	// plus a final line. Far fewer than 100 lines proves throttling.
	lines := strings.Count(strings.TrimSpace(out.String()), "\n") + 1
	if lines > 20 {
		t.Errorf("download not throttled: %d lines\n%s", lines, out.String())
	}
	if !strings.Contains(out.String(), "art done") {
		t.Errorf("missing final line: %q", out.String())
	}
}

func TestPlainTask(t *testing.T) {
	var out bytes.Buffer
	d := newPlainDisplay(&out, &out)

	orig := now
	now = func() time.Time { return time.Unix(0, 0) }
	defer func() { now = orig }()

	task := d.Task("linting", 4)
	task.Add(2)
	task.SetLabel("linting (eslint)")
	task.Complete()

	if !strings.Contains(out.String(), "linting") {
		t.Errorf("task output missing label: %q", out.String())
	}
	if !strings.Contains(out.String(), "4/4") {
		t.Errorf("task output missing completion: %q", out.String())
	}
}

// TestInteractiveLineDirectWhenNoActiveBar locks in the fix for the dropped
// "Installed …" line: once a bar has completed (no active bars), a status/log
// line must be written straight to the stream, not buffered through mpb where a
// subsequent direct write by non-ui code would drop it.
func TestInteractiveLineDirectWhenNoActiveBar(t *testing.T) {
	var buf bytes.Buffer
	d := &Display{mode: term.Interactive, out: &buf, err: &buf}

	task := d.Task("work", 2) // creates the mpb container, bars == 1
	task.Complete()           // bars == 0, container still alive
	d.Println("after-bar-line")
	d.Close()

	if !strings.Contains(buf.String(), "after-bar-line") {
		t.Fatalf("line not written directly with no active bar: %q", buf.String())
	}
}

func TestActivateRestoresPrevious(t *testing.T) {
	prev := Current()
	d := New(term.Plain)
	restore := Activate(d)
	if Current() != d {
		t.Fatal("Activate did not install display")
	}
	restore()
	if Current() != prev {
		t.Fatal("restore did not reinstate previous display")
	}
}

func TestProvisionRowsTerminalAndClose(t *testing.T) {
	var out, errOut bytes.Buffer
	d := newPlainDisplay(&out, &errOut)
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "install-1", Status: uievent.StatusStart, Name: "eslint"})
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "install-1", Status: uievent.StatusProgress, Name: "eslint", Msg: "packages", Index: 2, Total: 4})
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "install-1", Status: uievent.StatusDone, Name: "eslint"})
	if strings.Count(errOut.String(), "Installing eslint") != 1 || out.Len() != 0 {
		t.Fatalf("plain progress should be sparse on stderr only, stdout=%q stderr=%q", out.String(), errOut.String())
	}
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypePrepare, OpID: "prepare-1", Status: uievent.StatusStart, Name: "tool"})
	d.Close() // incomplete correlated rows must not hold mpb.Wait open
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypePrepare, OpID: "prepare-1", Status: uievent.StatusDone, Name: "tool"})
	if strings.Count(errOut.String(), "Preparing tool") != 1 || out.Len() != 0 {
		t.Fatalf("closed renderer accepted a late event or polluted stdout: stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestProvisionInteractivePhaseTransitions(t *testing.T) {
	var out, errOut bytes.Buffer
	d := &Display{mode: term.Interactive, out: &out, err: &errOut}
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeDownload, OpID: "app-1", Status: uievent.StatusStart, Name: "shared-app"})
	assertActive := func(wantDeterminate bool, wantPhase uievent.Type) {
		t.Helper()
		d.mu.Lock()
		defer d.mu.Unlock()
		row := d.provision["app-1"]
		if row == nil || row.bar == nil || d.bars != 1 || row.determinate != wantDeterminate || row.phase != wantPhase {
			t.Fatalf("row=%+v bars=%d, want active phase %s determinate=%v", row, d.bars, wantPhase, wantDeterminate)
		}
	}
	assertActive(false, uievent.TypeDownload) // unknown size starts as a spinner
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeDownload, OpID: "app-1", Status: uievent.StatusProgress, Name: "shared-app", BytesDone: 12, BytesTotal: 100})
	assertActive(true, uievent.TypeDownload) // known size upgrades to a byte bar
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "app-1", Status: uievent.StatusProgress, Name: "shared-app", Msg: "verify"})
	assertActive(false, uievent.TypeInstall) // verification is not download completion
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "app-1", Status: uievent.StatusDone, Name: "shared-app"})
	d.mu.Lock()
	remaining, bars := len(d.provision), d.bars
	d.mu.Unlock()
	if remaining != 0 || bars != 0 {
		t.Fatalf("terminal event left provision state: rows=%d bars=%d", remaining, bars)
	}
	d.Close()
}

func TestProvisionEventsCorrelateConcurrently(t *testing.T) {
	var out, errOut bytes.Buffer
	d := newPlainDisplay(&out, &errOut)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("op-%d", i)
			d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: id, Status: uievent.StatusStart, Name: id})
			d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: id, Status: uievent.StatusFail, Name: id, Msg: "failed"})
		}(i)
	}
	wg.Wait()
	if got := strings.Count(errOut.String(), "Installing op-"); got != 32 {
		t.Fatalf("got %d starts, want 32", got)
	}
	if strings.Contains(errOut.String(), "failed") || out.Len() != 0 {
		t.Fatalf("progress duplicated diagnostic text or polluted stdout: stdout=%q stderr=%q", out.String(), errOut.String())
	}

	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "wait", Status: uievent.StatusStart, Name: "shared-tool"})
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "wait", Status: uievent.StatusProgress, Name: "shared-tool", Msg: "Waiting for installation: another process"})
	if !strings.Contains(errOut.String(), "Installing shared-tool") || !strings.Contains(errOut.String(), "Waiting for installation: shared-tool") {
		t.Fatalf("waiting row omitted its owning app: %q", errOut.String())
	}
}

func TestProvisionRendererSuppressedInMachineAndMutedModes(t *testing.T) {
	var out, errOut bytes.Buffer
	d := newPlainDisplay(&out, &errOut)
	SetMuted(true)
	t.Cleanup(func() { SetMuted(false) })
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "muted", Status: uievent.StatusStart, Name: "hidden"})
	SetMuted(false)
	SetEventSink(&captureSink{}, true)
	t.Cleanup(func() { SetEventSink(nil, false) })
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "machine", Status: uievent.StatusStart, Name: "hidden"})
	SetEventSink(nil, false)
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("suppressed event rendered output: stdout=%q stderr=%q", out.String(), errOut.String())
	}
}
