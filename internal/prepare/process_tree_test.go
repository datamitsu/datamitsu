package prepare

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPrepareProcessHelper(t *testing.T) {
	mode := os.Getenv("PREPARE_PROCESS_TEST_MODE")
	if mode == "" {
		return
	}
	if mode == "child" {
		output := os.Getenv("PREPARE_PROCESS_TEST_OUTPUT")
		ready := os.Getenv("PREPARE_PROCESS_TEST_READY")
		f, err := os.OpenFile(output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
			if closeErr := f.Close(); closeErr != nil {
				os.Exit(7)
			}
			os.Exit(4)
		}
		for {
			_, _ = f.WriteString("x")
			_, _ = fmt.Fprintln(os.Stderr, "held descriptor")
			time.Sleep(5 * time.Millisecond)
		}
	}
	if mode == "leader" {
		child := exec.Command(os.Args[0], "-test.run=^TestPrepareProcessHelper$")
		child.Env = append(os.Environ(), "PREPARE_PROCESS_TEST_MODE=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(5)
		}
		ready := os.Getenv("PREPARE_PROCESS_TEST_READY")
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(6)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestRunStepStopsDescendantHoldingStderrOpen(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "output")
	ready := filepath.Join(root, "ready")
	stderr, err := os.OpenFile(filepath.Join(root, "step.stderr"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stderr.Close(); err != nil {
			t.Errorf("close step stderr file: %v", err)
		}
	})
	lay := &layout{root: root, out: filepath.Join(root, "out"), work: root, tmp: filepath.Join(root, "tmp")}
	if err := os.MkdirAll(lay.out, 0o755); err != nil {
		t.Fatal(err)
	}
	req := Request{
		App: "prepared-probe", Command: []string{os.Args[0], "-test.run=^TestPrepareProcessHelper$"},
		Env:    []string{"PREPARE_PROCESS_TEST_MODE=leader", "PREPARE_PROCESS_TEST_OUTPUT=" + output, "PREPARE_PROCESS_TEST_READY=" + ready},
		Stderr: stderr, Timeout: 5 * time.Second,
	}
	if err := lay.runStep(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	after, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("descendant continued writing after prepare step returned")
	}
}
