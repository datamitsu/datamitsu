//go:build linux

package provision

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestWorkerFDExhaustionHelper(t *testing.T) {
	mode := os.Getenv("PROVISION_FD_BUDGET_TEST")
	if mode == "" {
		return
	}
	available, err := strconv.Atoi(mode)
	if err != nil || available < 0 || available > 10 || available%2 != 0 {
		t.Fatalf("invalid descriptor fixture %q", mode)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	_ = writer.Close()
	var previous syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &previous); err != nil {
		t.Fatal(err)
	}
	limit := previous
	limit.Cur = min(previous.Max, 64)
	if limit.Cur < 16 {
		t.Skip("descriptor hard limit cannot support the bounded exhaustion fixture")
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	var fillers, probes []*os.File
	defer func() {
		_ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &previous)
		for _, file := range append(fillers, probes...) {
			_ = file.Close()
		}
	}()
	for {
		file, err := os.Open(os.DevNull)
		if err != nil {
			if !errors.Is(err, syscall.EMFILE) {
				t.Fatalf("unexpected resource exhaustion error: %v", err)
			}
			break
		}
		fillers = append(fillers, file)
	}
	releaseSlots := func(count int) {
		for range count {
			last := len(fillers) - 1
			if last < 0 {
				t.Fatal("insufficient fixture descriptors")
			}
			if err := fillers[last].Close(); err != nil {
				t.Fatal(err)
			}
			fillers = fillers[:last]
		}
	}
	releaseSlots(available)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker, err := StartWorker(ctx)
	if worker != nil {
		cancel()
		_ = worker.Close()
		t.Fatal("worker started despite an exhausted startup descriptor budget")
	}
	if !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("worker startup did not preserve descriptor exhaustion: %v", err)
	}
	if available == 0 {
		releaseSlots(2)
		available = 2
	}
	for range available / 2 {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatalf("failed worker startup leaked a private pipe: %v", err)
		}
		probes = append(probes, reader, writer)
	}
}

func TestWorkerStartupExhaustionReleasesEveryAllocatedPipe(t *testing.T) {
	for _, available := range []int{0, 2, 4, 6, 8, 10} {
		t.Run(strconv.Itoa(available), func(t *testing.T) {
			args := []string{"-test.run=^TestWorkerFDExhaustionHelper$"}
			if directory := flag.Lookup("test.gocoverdir"); directory != nil && directory.Value.String() != "" {
				args = append(args, "-test.gocoverdir="+directory.Value.String())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			cmd.Env = append(os.Environ(), "PROVISION_FD_BUDGET_TEST="+strconv.Itoa(available))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("descriptor exhaustion helper: %v\n%s", err, output)
			}
		})
	}
}
