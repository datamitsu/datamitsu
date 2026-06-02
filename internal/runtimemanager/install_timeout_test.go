package runtimemanager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
)

func TestNewInstallContext(t *testing.T) {
	t.Run("timeout disabled means no deadline", func(t *testing.T) {
		t.Setenv("DATAMITSU_INSTALL_TIMEOUT", "0")

		ctx, cancel, sec := newInstallContext(context.Background())
		defer cancel()

		if _, ok := ctx.Deadline(); ok {
			t.Error("expected no deadline when install timeout is disabled (0)")
		}
		if sec != 0 {
			t.Errorf("timeoutSec = %d, want 0", sec)
		}
	})

	t.Run("positive timeout sets a deadline", func(t *testing.T) {
		t.Setenv("DATAMITSU_INSTALL_TIMEOUT", "120")

		ctx, cancel, sec := newInstallContext(context.Background())
		defer cancel()

		dl, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected a deadline for a positive install timeout")
		}
		if sec != 120 {
			t.Errorf("timeoutSec = %d, want 120", sec)
		}
		if remaining := time.Until(dl); remaining <= 0 || remaining > 120*time.Second {
			t.Errorf("unexpected deadline remaining: %v", remaining)
		}
	})
}

func TestWrapInstallTimeout(t *testing.T) {
	if got := wrapInstallTimeout(nil, 600); got != nil {
		t.Errorf("nil error should pass through, got %v", got)
	}

	other := errors.New("boom")
	if got := wrapInstallTimeout(other, 600); got != other {
		t.Errorf("non-timeout error should pass through unchanged, got %v", got)
	}

	timeoutErr := fmt.Errorf("uv sync killed: %w", context.DeadlineExceeded)
	got := wrapInstallTimeout(timeoutErr, 5)
	if got == nil || got.Error() != "installation timed out after 5s: uv sync killed: context deadline exceeded" {
		t.Errorf("unexpected wrapped message: %v", got)
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("wrapped timeout should still match DeadlineExceeded, got %v", got)
	}
}

// TestRunInstallCmd_TimeoutKillsChild proves the subprocess install path uses an
// exec.CommandContext whose deadline actually kills the child: a long sleep is
// terminated well before it would finish, and the deadline is surfaced as
// context.DeadlineExceeded so wrapInstallTimeout can render a clear message.
func TestRunInstallCmd_TimeoutKillsChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses the POSIX sleep command")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sleep", "30")

	start := time.Now()
	err := runInstallCmd(ctx, cmd)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected the sleep to be killed by the context deadline, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("subprocess was not killed promptly: ran for %v", elapsed)
	}
	// wrapInstallTimeout must recognize the surfaced deadline as a timeout.
	wrapped := wrapInstallTimeout(err, 1)
	if wrapped == nil || !errors.Is(wrapped, context.DeadlineExceeded) {
		t.Errorf("wrapInstallTimeout should preserve the deadline, got %v", wrapped)
	}
}

// TestRunInstallCmd_Success confirms a subprocess that completes within the
// deadline returns no error and is not mistaken for a timeout.
func TestRunInstallCmd_Success(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses the POSIX true command")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "true")
	if err := runInstallCmd(ctx, cmd); err != nil {
		t.Errorf("expected nil error for a fast command, got %v", err)
	}
}

// TestRunInstallCmd_FailurePassesThrough confirms a genuine command failure
// (non-zero exit, not a timeout) is returned as-is and not wrapped as a deadline.
func TestRunInstallCmd_FailurePassesThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses the POSIX false command")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "false")
	err := runInstallCmd(ctx, cmd)
	if err == nil {
		t.Fatal("expected a non-zero exit error, got nil")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a normal failure must not be reported as a timeout: %v", err)
	}
}

// TestDownloadPNPMFromRegistry_ContextPropagated proves the install-timeout
// context reaches the pnpm registry HTTP request: against a server that never
// responds, a short-deadline context aborts the metadata fetch with
// context.DeadlineExceeded instead of blocking for the client's 5-minute budget.
func TestDownloadPNPMFromRegistry_ContextPropagated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // hang until the client cancels
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	rm := New(config.MapOfRuntimes{})
	destDir := t.TempDir()

	start := time.Now()
	err := rm.downloadPNPMFromRegistryURL(ctx, server.URL, "9.15.0", destDir, "deadbeef")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a context-deadline error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 30*time.Second {
		t.Errorf("download did not honor the context deadline: ran for %v", elapsed)
	}
}

// TestDownloadAndVerifyJAR_ContextPropagated proves the install-timeout context
// reaches the JAR download request the same way: a hanging server is aborted by
// the context deadline rather than the client's overall budget.
func TestDownloadAndVerifyJAR_ContextPropagated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	destPath := filepath.Join(t.TempDir(), "app.jar")

	start := time.Now()
	err := downloadAndVerifyJAR(ctx, server.URL, "deadbeef", destPath)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a context-deadline error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 30*time.Second {
		t.Errorf("JAR download did not honor the context deadline: ran for %v", elapsed)
	}
}
