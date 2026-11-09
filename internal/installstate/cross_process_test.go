package installstate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestInstallStateProcessHelper(t *testing.T) {
	mode := os.Getenv("DATAMITSU_INSTALLSTATE_HELPER")
	if mode == "" {
		return
	}
	root := os.Getenv("DATAMITSU_INSTALLSTATE_ROOT")
	switch mode {
	case "owner", "crash-owner":
		ctx, release, err := Acquire(context.Background(), root, "apps/probe", "probe")
		if err != nil {
			t.Fatal(err)
		}
		Progress(ctx, PhaseDownload, 7, 10, "bytes")
		fmt.Println("HELD")
		if mode == "crash-owner" {
			select {}
		}
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		release()
	case "waiter":
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_, release, err := Acquire(ctx, root, "apps/probe", "probe")
		if err == nil {
			release()
			t.Fatal("waiter unexpectedly acquired held target")
		}
		fmt.Println("CANCELED")
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

type statusProcess struct {
	cmd   *exec.Cmd
	lines chan string
	stdin *os.File
}

func startStatusProcess(t *testing.T, mode, root string) *statusProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstallStateProcessHelper$")
	cmd.Env = append(os.Environ(), "DATAMITSU_INSTALLSTATE_HELPER="+mode, "DATAMITSU_INSTALLSTATE_ROOT="+root)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	proc := &statusProcess{cmd: cmd, lines: lines, stdin: stdin.(*os.File)}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return proc
}

func (p *statusProcess) line(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got, ok := <-p.lines:
			if !ok {
				t.Fatalf("process exited before %q", want)
			}
			if strings.Contains(got, want) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

func TestCrossProcessWaitStatusAndCancel(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(base, "cache"))
	root := filepath.Join(base, "store")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := startStatusProcess(t, "owner", root)
	owner.line(t, "HELD")
	entries, err := os.ReadDir(filepath.Join(env.GetCachePath(), "store-locks", "status"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("status files = %d, want 1", len(entries))
	}
	statusPath := filepath.Join(env.GetCachePath(), "store-locks", "status", entries[0].Name())
	info, err := os.Stat(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("status permissions = %o, want 600", info.Mode().Perm())
	}
	if err := os.WriteFile(statusPath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	waiter := startStatusProcess(t, "waiter", root)
	waiter.line(t, "Waiting for installation: probe")
	waiter.line(t, "CANCELED")
	if owner.cmd.ProcessState != nil {
		t.Fatal("owner stopped while waiter canceled")
	}
	_, _ = owner.stdin.Write([]byte("release\n"))
	if err := owner.cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestCrashLeftStatusIsOverwritten(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(base, "cache"))
	root := filepath.Join(base, "store")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := startStatusProcess(t, "crash-owner", root)
	owner.line(t, "HELD")
	statusDir := filepath.Join(env.GetCachePath(), "store-locks", "status")
	entries, err := os.ReadDir(statusDir)
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(filepath.Join(statusDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = owner.cmd.Wait()
	ctx, release, err := Acquire(context.Background(), root, "apps/probe", "probe")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	Progress(ctx, PhaseVerify, -1, -1, "")
	newBytes, err := os.ReadFile(filepath.Join(statusDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var before, after status
	if err := json.Unmarshal(oldBytes, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(newBytes, &after); err != nil {
		t.Fatal(err)
	}
	if before.Owner == after.Owner {
		t.Fatal("crash status owner was not replaced")
	}
}
