//go:build linux

package cli_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/mattn/go-runewidth"
	"golang.org/x/sys/unix"
)

func TestColdProvisioningTTYShowsByteProgressBeforeExecution(t *testing.T) {
	p := clitest.NewProject(t)
	payload := []byte("#!/bin/sh\nexit 0\n" + strings.Repeat("# download fixture padding\n", 40000))
	largePayload := []byte(string(payload) + strings.Repeat("#", 11<<20))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, chunk := payload, 32768
		if r.URL.Path == "/large" {
			body, chunk = largePayload, 256<<10
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		for offset := 0; offset < len(body); offset += chunk {
			end := min(offset+chunk, len(body))
			if _, err := w.Write(body[offset:end]); err != nil {
				return
			}
			if flush, ok := w.(http.Flusher); ok {
				flush.Flush()
			}
			time.Sleep(45 * time.Millisecond)
		}
	}))
	defer server.Close()
	p.WriteFile("sample.txt", "fixture\n")
	p.WriteFile("datamitsu.config.js", fmt.Sprintf(`
globalThis.getMinVersion = () => "0.0.0";
const binary = (url, hash) => ({ binaries: { linux: { %q: { unknown: { url, hash, contentType: "binary" } } } } });
const longName = "dm-internal-lefthook-upstream";
const names = ["fixture", longName, "pending-a", "pending-b", "pending-c"];
globalThis.getConfig = () => ({ version: "fixture", runtimes: {},
  apps: Object.fromEntries(names.map(name => [name, { binary: name === longName ? binary(%q, %q) : binary(%q, %q) }])),
  projectTypes: { sample: { markers: ["sample.txt"] } },
  tools: Object.fromEntries(names.map(app => [app, { projectTypes: ["sample"], operations: { fix: { app, args: ["{files}"], globs: ["*.txt"] } } }]))
});`, runtime.GOARCH, server.URL+"/large", digest.SHA256Of(largePayload).String(), server.URL, digest.SHA256Of(payload).String()))
	for _, width := range []uint16{104, 64} {
		t.Run(fmt.Sprintf("%d-columns", width), func(t *testing.T) {
			assertProvisioningTTY(t, p.Dir, width)
		})
	}
}

func TestInitTTYClearsProgressBeforeSummary(t *testing.T) {
	p := clitest.NewProject(t)
	payload := []byte("#!/bin/sh\nexit 0\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload[:1])
		w.(http.Flusher).Flush()
		time.Sleep(350 * time.Millisecond) // Let the terminal draw a live row.
		_, _ = w.Write(payload[1:])
	}))
	defer server.Close()
	p.WriteFile("datamitsu.config.js", fmt.Sprintf(`
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({ version: "fixture", runtimes: {}, tools: {},
  apps: { fixture: { required: true, binary: { binaries: { linux: { %q: { unknown: { url: %q, hash: %q, contentType: "binary" } } } } } } }
});`, runtime.GOARCH, server.URL, digest.SHA256Of(payload).String()))
	text := runProvisioningTTY(t, p.Dir, 104, "init", "--fail-on-download-error")
	before, summary, ok := strings.Cut(text, "┃ tools\r\n")
	if !ok || !strings.Contains(summary, "✓ fixture  downloaded") || !strings.Contains(summary, "┗━ ready") {
		t.Fatalf("init lost the installation summary or footer:\n%s", text)
	}
	lastRow := strings.LastIndex(before, "┃ fixture")
	lastErase := strings.LastIndex(before, "\x1b[J")
	if lastRow < 0 || lastErase < lastRow || strings.Contains(summary, "\x1b[J") {
		t.Fatalf("live progress was not cleared before the init summary:\n%q", text)
	}
}

func TestProvisioningTTYResizeKeepsDownloadBar(t *testing.T) {
	p := clitest.NewProject(t)
	payload := []byte("#!/bin/sh\nexit 0\n" + strings.Repeat("# download fixture padding\n", 40000) + strings.Repeat("#", 8<<20))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		for offset := 0; offset < len(payload); offset += 64 << 10 {
			end := min(offset+64<<10, len(payload))
			if _, err := w.Write(payload[offset:end]); err != nil {
				return
			}
			if flush, ok := w.(http.Flusher); ok {
				flush.Flush()
			}
			time.Sleep(25 * time.Millisecond)
		}
	}))
	defer server.Close()
	p.WriteFile("sample.txt", "fixture\n")
	p.WriteFile("datamitsu.config.js", fmt.Sprintf(`
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({ version: "fixture", runtimes: {},
  apps: { fixture: { binary: { binaries: { linux: { %q: { unknown: { url: %q, hash: %q, contentType: "binary" } } } } } } },
  projectTypes: { sample: { markers: ["sample.txt"] } },
  tools: { fixture: { projectTypes: ["sample"], operations: { fix: { app: "fixture", args: ["{files}"], globs: ["*.txt"] } } } }
});`, runtime.GOARCH, server.URL, digest.SHA256Of(payload).String()))
	masterFD, slave := openRuntimePTY(t)
	master := os.NewFile(uintptr(masterFD), "resize-pty")
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 104}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, clitest.BuildOnce(t), "fix")
	cmd.Dir = p.Dir
	cmd.Env = append(clitest.BaseEnv(t.TempDir()), "DATAMITSU_OFFLINE=", "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	chunks := make(chan []byte, 512)

	go func() {
		defer close(chunks)
		for {
			part := make([]byte, 4096)
			n, err := master.Read(part)
			if n > 0 {
				chunks <- part[:n]
			}
			if err != nil {
				return
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Resize as soon as output proves the transfer bar is live.
	buf := make([]byte, 0, 1<<20)
	deadline := time.After(20 * time.Second)
	bar := regexp.MustCompile(`┃ fixture +[0-9]+%[^\r\n]* +\[[=>-]+\]`)
	for !bar.Match(buf) {
		select {
		case <-deadline:
			_ = cmd.Process.Kill()
			t.Fatalf("download did not start:\n%s", buf)
		case part, ok := <-chunks:
			if !ok {
				t.Fatalf("PTY closed before download started:\n%s", buf)
			}
			buf = append(buf, part...)
		}
	}
	beforeResize := len(buf)
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 64}); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("PTY fix failed: %v\n%s", err, buf)
	}
	for part := range chunks {
		buf = append(buf, part...)
	}
	clean := regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`).ReplaceAllString(string(buf), "")
	afterResize := regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`).ReplaceAllString(string(buf[beforeResize:]), "")
	compact := regexp.MustCompile(`(?m)^┃ fixture +[0-9]+% +[0-9.]+ [KM]iB +\[[=>-]+\] *\r?$`)
	if line := compact.FindString(afterResize); line == "" || runewidth.StringWidth(strings.TrimSuffix(line, "\r")) > 64 {
		t.Fatalf("compact download bar disappeared after resize:\n%s", clean)
	}
}

func runProvisioningTTY(t *testing.T, dir string, width uint16, args ...string) string {
	t.Helper()
	masterFD, slave := openRuntimePTY(t)
	master := os.NewFile(uintptr(masterFD), "provision-pty")
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: width}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, clitest.BuildOnce(t), args...)
	cmd.Dir = dir
	cmd.Env = append(clitest.BaseEnv(t.TempDir()), "DATAMITSU_OFFLINE=", "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	output := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(master); output <- data }()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	_ = slave.Close()
	text := string(<-output)
	if err != nil {
		t.Fatalf("PTY %v failed: %v\n%s", args, err, text)
	}
	return text
}

func assertProvisioningTTY(t *testing.T, dir string, width uint16) {
	t.Helper()
	text := runProvisioningTTY(t, dir, width, "fix")
	before, _, ok := strings.Cut(text, "┏━ fix")
	if !ok {
		t.Fatalf("execution frame missing:\n%s", text)
	}
	preparation := before
	if !strings.Contains(preparation, "\x1b[") || !strings.Contains(preparation, "KiB") {
		t.Fatalf("TTY preparation has no live byte progress:\n%s", preparation)
	}
	clean := regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`).ReplaceAllString(preparation, "")
	if !strings.Contains(clean, "queued 1: pending-c") {
		t.Fatalf("missing pending tools while workers are busy:\n%s", clean)
	}
	if width == 104 && !regexp.MustCompile(`[KM]iB/s ~[0-9]+s`).MatchString(clean) {
		t.Fatalf("missing measured download rate and ETA:\n%s", clean)
	}
	rows := regexp.MustCompile(`(?:^|[\r\n])(┃ (fixture|dm-internal-lefthook-[^ \r\n]+)( +[0-9]+%[^\r\n]*) +\[[=>-]+\])`).FindAllStringSubmatch(clean, -1)
	if width == 64 {
		compact := regexp.MustCompile(`^ +[0-9]{1,3}% +[0-9.]+ [KM]iB +$`)
		seen := make(map[string]bool)
		for _, row := range rows {
			if !compact.MatchString(row[3]) || runewidth.StringWidth(row[1]) > int(width) {
				t.Fatalf("expected transferred size and a visible bar without total: %q", row[1])
			}
			seen[row[2]] = true
		}
		if len(seen) != 2 {
			t.Fatalf("expected bars for both downloads at %d columns, got %v\n%s", width, seen, preparation)
		}
		return
	}
	counters := regexp.MustCompile(`^ +[0-9]{1,3}%  +[0-9.]+ [KM]iB / +([0-9.]+ MiB)`)
	totals := make(map[string]string)
	var columns [5]int
	for i, row := range rows {
		counter := counters.FindStringSubmatch(row[3])
		if counter == nil {
			t.Fatalf("expected one fixed-width percentage followed by byte counters: %q", row[1])
		}
		totals[row[2]] = counter[1]
		var current [5]int
		for j, mark := range []byte{'[', ']', '%', '/'} {
			current[j] = runewidth.StringWidth(row[1][:strings.IndexByte(row[1], mark)])
		}
		current[4] = runewidth.StringWidth(row[1][:strings.LastIndex(row[1], "MiB")+len("MiB")])
		if i == 0 {
			columns = current
		} else if current != columns {
			t.Fatalf("progress columns = %v, want %v: %q", current, columns, row[1])
		}
	}
	const longLabel = "dm-internal-lefthook-upstream"
	if short, long := totals["fixture"], totals[runewidth.Truncate(longLabel, 28, "…")]; short == "" || long == "" || len(short) == len(long) {
		t.Fatalf("expected both downloads with different counter widths, got %v\n%s", totals, preparation)
	}
}
