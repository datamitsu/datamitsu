package ui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/term"
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
