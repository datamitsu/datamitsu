package httpx

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestStallGuardTripsOnSilence(t *testing.T) {
	guard, ctx := NewStallGuard(context.Background(), 50*time.Millisecond)
	defer guard.Stop()

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("guard did not trip on a silent stream")
	}
	if !guard.Stalled() {
		t.Error("Stalled() = false after the guard tripped")
	}
	if !errors.Is(context.Cause(ctx), ErrStalled) {
		t.Errorf("cause = %v, want ErrStalled", context.Cause(ctx))
	}
}

func TestStallGuardReaderRearms(t *testing.T) {
	guard, ctx := NewStallGuard(context.Background(), 120*time.Millisecond)
	defer guard.Stop()

	reader := guard.Reader(&slowTrickle{chunks: 6, gap: 60 * time.Millisecond})
	buf := make([]byte, 8)
	total := 0
	for {
		n, err := reader.Read(buf)
		total += n
		if err != nil {
			break
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("guard tripped on a moving stream (read %d bytes): %v", total, context.Cause(ctx))
	}
	if total != 6 {
		t.Errorf("read %d bytes, want 6", total)
	}
}

func TestStallGuardStopDisarms(t *testing.T) {
	guard, ctx := NewStallGuard(context.Background(), 30*time.Millisecond)
	guard.Stop()
	time.Sleep(80 * time.Millisecond)
	if guard.Stalled() {
		t.Error("guard tripped after Stop")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("ctx after Stop = %v, want plain cancellation", ctx.Err())
	}
}

// slowTrickle emits one byte per Read with a fixed gap, then EOF.
type slowTrickle struct {
	chunks int
	gap    time.Duration
	sent   int
}

func (s *slowTrickle) Read(p []byte) (int, error) {
	if s.sent >= s.chunks {
		return 0, io.EOF
	}
	time.Sleep(s.gap)
	s.sent++
	p[0] = 'z'
	return 1, nil
}
