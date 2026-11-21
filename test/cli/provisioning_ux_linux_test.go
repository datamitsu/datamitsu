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
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/digest"
	"golang.org/x/sys/unix"
)

func TestColdProvisioningTTYShowsByteProgressBeforeExecution(t *testing.T) {
	p := clitest.NewProject(t)
	payload := []byte("#!/bin/sh\nexit 0\n" + strings.Repeat("# download fixture padding\n", 40000))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		for offset := 0; offset < len(payload); offset += 32768 {
			end := min(offset+32768, len(payload))
			if _, err := w.Write(payload[offset:end]); err != nil {
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
globalThis.getConfig = () => ({ version: "fixture", runtimes: {},
  apps: { fixture: { binary: { binaries: { linux: { %q: { unknown: { url: %q, hash: %q, contentType: "binary" } } } } } } },
  projectTypes: { sample: { markers: ["sample.txt"] } },
  tools: { fixture: { projectTypes: ["sample"], operations: { fix: { app: "fixture", args: ["{files}"], globs: ["*.txt"] } } } }
});`, runtime.GOARCH, server.URL, digest.SHA256Of(payload).String()))
	masterFD, slave := openRuntimePTY(t)
	master := os.NewFile(uintptr(masterFD), "provision-pty")
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 100}); err != nil {
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
	output := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(master); output <- data }()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	_ = slave.Close()
	text := string(<-output)
	if err != nil {
		t.Fatalf("PTY fix failed: %v\n%s", err, text)
	}
	before, _, ok := strings.Cut(text, "┏━ fix")
	if !ok {
		t.Fatalf("execution frame missing:\n%s", text)
	}
	preparation := before
	if !strings.Contains(preparation, "\x1b[") || !strings.Contains(preparation, "KiB") {
		t.Fatalf("TTY preparation has no live byte progress:\n%s", preparation)
	}
}
