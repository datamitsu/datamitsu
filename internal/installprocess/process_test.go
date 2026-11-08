package installprocess

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const verboseOutputBytesPerStream = 64 * 1024 * 1024

func TestInstallprocessHelper(t *testing.T) {
	mode := os.Getenv("INSTALLPROCESS_TEST_MODE")
	if mode == "" {
		return
	}
	if mode == "verbose" {
		chunk := bytes.Repeat([]byte("x"), 32*1024)
		for range verboseOutputBytesPerStream / len(chunk) {
			if _, err := os.Stdout.Write(chunk); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stderr.Write(chunk); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if mode == "allocation-budget" {
		cmd := helperCommand(t, "verbose", "", "", false)
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if err := Run(t.Context(), cmd); err != nil {
			t.Fatal(err)
		}
		runtime.ReadMemStats(&after)
		allocated := after.TotalAlloc - before.TotalAlloc
		if allocated > verboseOutputBytesPerStream/2 {
			t.Fatalf("discarded output retained in memory: allocated %d bytes for %d output bytes", allocated, 2*verboseOutputBytesPerStream)
		}
		return
	}
	if mode == "oversized-lines" || mode == "oversized-eof" || mode == "line-boundary" {
		chunk := bytes.Repeat([]byte("z"), 32*1024)
		for range maxStderrLineBytes / len(chunk) {
			if _, err := os.Stderr.Write(chunk); err != nil {
				t.Fatal(err)
			}
		}
		if mode != "line-boundary" {
			if _, err := os.Stderr.Write(chunk); err != nil {
				t.Fatal(err)
			}
		}
		if mode != "oversized-eof" {
			if _, err := io.WriteString(os.Stderr, "\nvalid-line\ntrailing"); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if mode == "child" {
		path := os.Getenv("INSTALLPROCESS_TEST_OUTPUT")
		ready := os.Getenv("INSTALLPROCESS_TEST_READY")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
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
			_, _ = fmt.Fprint(os.Stderr, "descendant\n")
			time.Sleep(5 * time.Millisecond)
		}
	}
	if mode == "streams" {
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("stdout-line\n", 100))
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("stderr-line\n", 100))
		return
	}
	if mode == "leader" {
		ready := os.Getenv("INSTALLPROCESS_TEST_READY")
		child := exec.Command(os.Args[0], "-test.run=^TestInstallprocessHelper$")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		child.Env = append(os.Environ(), "INSTALLPROCESS_TEST_MODE=child")
		if err := child.Start(); err != nil {
			os.Exit(5)
		}
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
		_, _ = fmt.Fprint(os.Stdout, "leader-out\n")
		_, _ = fmt.Fprint(os.Stderr, "leader-err\n")
		if os.Getenv("INSTALLPROCESS_TEST_CANCEL") == "1" {
			for {
				time.Sleep(time.Second)
			}
		}
	}
}

func helperCommand(t *testing.T, mode, output, ready string, cancel bool) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstallprocessHelper$")
	cmd.Env = append(os.Environ(), "INSTALLPROCESS_TEST_MODE="+mode, "INSTALLPROCESS_TEST_OUTPUT="+output, "INSTALLPROCESS_TEST_READY="+ready)
	if cancel {
		cmd.Env = append(cmd.Env, "INSTALLPROCESS_TEST_CANCEL=1")
	}
	return cmd
}

func TestRunDrainsBothStreamsWithSharedBuffer(t *testing.T) {
	cmd := helperCommand(t, "streams", "", "", false)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := Run(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "stdout-line") || !strings.Contains(output.String(), "stderr-line") {
		t.Fatalf("incomplete output: %q", output.String())
	}
}

func TestRunStreamingCapturesStreams(t *testing.T) {
	cmd := helperCommand(t, "streams", "", "", false)
	stdout, stderr, err := RunStreaming(context.Background(), cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(stdout, "stdout-line") != 100 || strings.Count(stderr, "stderr-line") != 100 {
		t.Fatalf("unexpected stream counts: stdout=%d stderr=%d", strings.Count(stdout, "stdout-line"), strings.Count(stderr, "stderr-line"))
	}
}

func TestRunStopsDescendantHoldingOutputAfterLeaderExit(t *testing.T) {
	root := t.TempDir()
	output, ready := filepath.Join(root, "writes"), filepath.Join(root, "ready")
	cmd := helperCommand(t, "leader", output, ready, false)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := Run(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "leader-out\n") || !strings.Contains(stderr.String(), "leader-err") {
		t.Fatalf("leader output lost: stdout=%q stderr=%q", stdout.String(), stderr.String())
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
	if !bytes.Equal(before, after) {
		t.Fatal("descendant continued writing after Run returned")
	}
}

func TestRunCancellationStopsDescendantHoldingOutput(t *testing.T) {
	root := t.TempDir()
	output, ready := filepath.Join(root, "writes"), filepath.Join(root, "ready")
	cmd := helperCommand(t, "leader", output, ready, true)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := Run(ctx, cmd); err == nil {
		t.Fatal("Run returned nil after cancellation")
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
	if !bytes.Equal(before, after) {
		t.Fatal("descendant continued writing after cancellation")
	}
}

func TestRunDiscardsVerboseOutputWithoutRetainingIt(t *testing.T) {
	cmd := helperCommand(t, "allocation-budget", "", "", false)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("discarded-output allocation budget: %v\n%s", err, output)
	}
}

func TestStreamingProgressLineLimitPreservesCaptureAndRecovers(t *testing.T) {
	for _, mode := range []string{"oversized-lines", "oversized-eof", "line-boundary"} {
		t.Run(mode, func(t *testing.T) {
			cmd := helperCommand(t, mode, "", "", false)
			var lines []string
			_, captured, err := RunStreaming(t.Context(), cmd, func(line string) { lines = append(lines, line) })
			if err != nil {
				t.Fatal(err)
			}
			payloadSize := maxStderrLineBytes + 32*1024
			if mode == "line-boundary" {
				payloadSize = maxStderrLineBytes
			}
			wantCapture := strings.Repeat("z", payloadSize)
			wantLines := []string(nil)
			if mode != "oversized-eof" {
				wantCapture += "\nvalid-line\ntrailing"
				wantLines = []string{"valid-line", "trailing"}
			}
			if mode == "line-boundary" {
				wantLines = append([]string{strings.Repeat("z", maxStderrLineBytes)}, wantLines...)
			}
			if captured != wantCapture {
				t.Fatalf("raw stderr capture changed: got %d bytes, want %d", len(captured), len(wantCapture))
			}
			if !slices.Equal(lines, wantLines) {
				t.Fatalf("unexpected progress framing: got %d lines, want %d", len(lines), len(wantLines))
			}
		})
	}
}
