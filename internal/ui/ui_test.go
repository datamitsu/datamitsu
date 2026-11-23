package ui

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/provision"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

func TestTransferEstimateExpiresAndResumes(t *testing.T) {
	var row provisionRow
	start := time.Unix(100, 0)
	sample := func(seconds int, done int64) {
		row.observeTransfer(uievent.Event{BytesDone: done << 20, BytesTotal: 10 << 20}, start.Add(time.Duration(seconds)*time.Second))
	}
	check := func(seconds int, want string) {
		t.Helper()
		if got := row.transferDetail(start.Add(time.Duration(seconds) * time.Second)); got != want {
			t.Fatalf("estimate at %ds = %q, want %q", seconds, got, want)
		}
	}
	sample(0, 0)
	sample(1, 1)
	check(3, "1.0 MiB/s ~9s")
	check(4, "")
	sample(5, 1)
	check(5, "")
	sample(6, 2)
	check(6, "204.8 KiB/s ~40s")
	sample(7, 4)
	check(7, "849.9 KiB/s ~8s")
}

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

// A completed group of bars gives back the terminal before the next status line.
func TestInteractiveLineDirectWhenNoActiveBar(t *testing.T) {
	var buf bytes.Buffer
	d := &Display{mode: term.Interactive, out: &buf, err: &buf}

	task := d.Task("work", 2) // creates the mpb container, bars == 1
	task.Complete()           // bars == 0; the last repaint is drained
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

func TestProvisionInstallDetail(t *testing.T) {
	for _, tc := range []struct {
		name     string
		msg      string
		want     string
		artifact string
	}{
		{"installer", "publish", "finalizing install", "binary fixture"},
		{"waiter", "Waiting for installation: binary fixture (publish)", "Waiting: (finalizing install)", "binary fixture"},
		{"incomplete runtime download", "Waiting for installation: runtime bun (download 999/1000 bytes)", "Waiting: download 99%", "runtime bun"},
		{"complete runtime download", "Waiting for installation: runtime bun (download 1000/1000 bytes)", "Waiting: download 100%", "runtime bun"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			d := &Display{mode: term.Interactive, out: &out, err: &out}
			t.Cleanup(d.Close)
			d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "fixture", Status: uievent.StatusProgress, Name: tc.artifact, Msg: tc.msg})
			row := d.provision["fixture"]
			if row == nil || row.bar == nil {
				t.Fatal("expected a visible install row")
			}
			if got := row.detail.Load(); got != tc.want {
				t.Fatalf("visible detail = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProvisionWaitSharesOnlyMatchingOwner(t *testing.T) {
	d := &Display{mode: term.Interactive, out: io.Discard, err: io.Discard}
	t.Cleanup(d.Close)
	wait := provision.InstallProgress{
		Type: uievent.TypeInstall, OpID: "wait", Status: uievent.StatusProgress, Name: "runtime bun",
		WaitOwner: "runtime-lease", WaitBytesDone: 1, WaitBytesTotal: 2,
	}
	d.RenderInstallProgress(wait)
	if row := d.provision["wait"]; row == nil || !row.determinate || row.bar == nil {
		t.Fatal("external owner with known bytes must show a download bar")
	}
	owner := provision.InstallProgress{Type: uievent.TypeDownload, OpID: "app", Name: "bun", BytesTotal: 2, Owners: []string{"unrelated"}}
	d.RenderInstallProgress(owner)
	if d.provision["wait"] == nil {
		t.Fatal("a same-name app hid an unrelated runtime wait")
	}
	fresh := wait
	fresh.OpID = "fresh-waiter"
	fresh.WaitUpdatedAt, fresh.WaitBytesDone = 20, 2
	d.RenderInstallProgress(fresh)
	owner.OpID = "runtime-binary"
	owner.Owners = []string{"runtime-lease", "nested-binary-lease"}
	d.RenderInstallProgress(owner)
	d.RenderInstallProgress(wait)
	if d.provision["wait"] != nil || d.provision["runtime-binary"] == nil {
		t.Fatal("a nested owner and its waiter must share one visible indicator")
	}
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: fresh.OpID, Status: uievent.StatusSkip})
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: owner.OpID, Status: uievent.StatusSkip})
	if row := d.provision["wait"]; row == nil || !row.determinate || row.bar.Current() != 2 {
		t.Fatal("ending the nested owner must restore the latest wait progress")
	}
}

func TestProvisionWaitBarSurvivesFirstWaiter(t *testing.T) {
	d := &Display{mode: term.Interactive, out: io.Discard, err: io.Discard}
	t.Cleanup(d.Close)
	wait := provision.InstallProgress{
		Type: uievent.TypeInstall, OpID: "first", Status: uievent.StatusProgress, Name: "runtime bun",
		WaitOwner: "external-owner",
	}
	d.RenderInstallProgress(wait)
	wait.OpID = "second"
	d.RenderInstallProgress(wait)
	if row := d.provision["second"]; row == nil || row.determinate || row.bar == nil || d.provision["first"] != row || d.bars != 1 {
		t.Fatal("unknown download size must show one shared spinner")
	}
	wait.WaitBytesDone, wait.WaitBytesTotal = 1, 2
	d.RenderInstallProgress(wait)
	row := d.provision["second"]
	if row == nil || !row.determinate || row.bar == nil || d.provision["first"] != row || d.bars != 1 {
		t.Fatal("both waiters must have one download bar")
	}
	wait.WaitUpdatedAt = 20
	d.RenderInstallProgress(wait)
	older := wait
	older.OpID = "first"
	older.WaitUpdatedAt, older.WaitBytesDone = 10, 0
	d.RenderInstallProgress(older)
	if got := row.bar.Current(); got != 1 {
		t.Fatalf("delayed snapshot rolled progress back to %d", got)
	}
	wait.WaitBytesDone = wait.WaitBytesTotal
	d.RenderInstallProgress(wait)
	if row.bar.AbortedOrCompleted() {
		t.Fatal("100% transferred must not end a wait")
	}
	wait.WaitOwner = "replacement-owner"
	d.RenderInstallProgress(wait)
	if d.provision["first"] != row || d.provision["second"] == row || d.bars != 2 {
		t.Fatal("a waiter changing owners must not take the old owner's bar with it")
	}
	wait.WaitOwner = "external-owner"
	d.RenderInstallProgress(wait)
	if d.provision["second"] != row || d.bars != 1 {
		t.Fatal("returning to the same owner must reuse its bar")
	}
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "first", Status: uievent.StatusSkip})
	if d.provision["first"] != nil || d.provision["second"] != row || d.bars != 1 {
		t.Fatal("ending one wait must leave the other wait and its bar active")
	}
	d.RenderProvisionEvent(uievent.Event{Type: uievent.TypeInstall, OpID: "second", Status: uievent.StatusSkip})
	if d.provision["second"] != nil || d.bars != 0 {
		t.Fatal("ending the last wait must remove the shared bar")
	}
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
