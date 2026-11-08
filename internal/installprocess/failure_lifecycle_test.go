package installprocess

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type failedInstallerOutput struct{ err error }

func (w failedInstallerOutput) Write([]byte) (int, error) { return 0, w.err }

func TestInstallerOutputFailureStillDrainsAndReapsChild(t *testing.T) {
	writeErr := errors.New("output destination closed")
	for _, failure := range []error{writeErr, nil} {
		name := "destination error"
		want := writeErr
		if failure == nil {
			name, want = "zero write", io.ErrShortWrite
		}
		t.Run(name, func(t *testing.T) {
			cmd := helperCommand(t, "verbose", "", "", false)
			cmd.Stdout = failedInstallerOutput{err: failure}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := Run(ctx, cmd); !errors.Is(err, want) {
				t.Fatalf("installer output error = %v, want %v", err, want)
			}
			if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
				t.Fatalf("output failure prevented normal child completion: %v", cmd.ProcessState)
			}
		})
	}
}

func TestInstallerCanceledBeforeStartDoesNotCreateProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := helperCommand(t, "streams", "", "", false)
	if err := Run(ctx, cmd); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled installer = %v", err)
	}
	if cmd.Process != nil {
		t.Fatal("pre-canceled installer started a process")
	}
}

func TestInstallerMissingExecutableReturnsStartFailure(t *testing.T) {
	cmd := exec.Command(filepath.Join(t.TempDir(), "missing-installer"))
	if err := Run(context.Background(), cmd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing installer error = %v", err)
	}
	if cmd.Process != nil {
		t.Fatal("missing installer created a process")
	}
}
