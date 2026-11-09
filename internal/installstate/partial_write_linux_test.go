//go:build linux

package installstate

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestStatusPartialWriteHelper(t *testing.T) {
	if os.Getenv("INSTALL_STATUS_PARTIAL_WRITE_HELPER") != "1" {
		return
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	store := filepath.Join(t.TempDir(), "store")
	ctx, release, err := Acquire(context.Background(), store, "apps/probe", "probe")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var previous syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &previous); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Setrlimit(syscall.RLIMIT_FSIZE, &previous) }()
	ignored := signal.Ignored(syscall.SIGXFSZ)
	signal.Ignore(syscall.SIGXFSZ)
	if !ignored {
		defer signal.Reset(syscall.SIGXFSZ)
	}
	limit := previous
	limit.Cur = 16
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}
	Progress(ctx, PhaseVerify, -1, -1, "")
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &previous); err != nil {
		t.Fatal(err)
	}
	writer := ctx.Value(statusKey{}).([]*statusWriter)[0]
	writer.mu.Lock()
	defer writer.mu.Unlock()
	directory := filepath.Join(env.GetCachePath(), "store-locks", "status")
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("partial status write left temporary data: entries=%v, error=%v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(directory, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var record status
	if err := json.Unmarshal(data, &record); err != nil || record.Owner != writer.owner || record.Schema != schema {
		t.Fatalf("failed write damaged the previously published status: %+v, %v", record, err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := Acquire(waitCtx, store, "apps/probe", "waiter"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("partial metadata write lost target ownership: %v", err)
	}
}

func TestPartialStatusWritePreservesPublishedRecordAndCleansTemporaryFile(t *testing.T) {
	args := []string{"-test.run=^TestStatusPartialWriteHelper$"}
	if directory := flag.Lookup("test.gocoverdir"); directory != nil && directory.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+directory.Value.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], args...)
	command.Env = append(os.Environ(), "INSTALL_STATUS_PARTIAL_WRITE_HELPER=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("partial-write fixture: %v\n%s", err, output)
	}
}
