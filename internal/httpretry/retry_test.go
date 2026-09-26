package httpretry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func fastRetries(t *testing.T) {
	t.Helper()
	base, maxDelay, maxAfter := RetryBase, RetryMax, MaxRetryAfter
	RetryBase, RetryMax, MaxRetryAfter = time.Millisecond, 4*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { RetryBase, RetryMax, MaxRetryAfter = base, maxDelay, maxAfter })
}

type askedError struct{ wait time.Duration }

func (e askedError) Error() string             { return "asked to wait" }
func (e askedError) RetryAfter() time.Duration { return e.wait }

func TestRetry(t *testing.T) {
	fastRetries(t)
	transient := errors.New("connection reset")

	t.Run("transient failures are retried until success and reported", func(t *testing.T) {
		var seen []Attempt
		calls := 0
		err := Retry(context.Background(), "GET x", func(a Attempt) { seen = append(seen, a) }, func() error {
			calls++
			if calls < 3 {
				return transient
			}
			return nil
		})
		if err != nil || calls != 3 {
			t.Fatalf("Retry() = %v after %d calls, want nil after 3", err, calls)
		}
		if len(seen) != 2 || seen[0].Attempt != 1 || seen[1].Attempt != 2 || seen[0].What != "GET x" || seen[0].Max != DefaultMaxAttempts {
			t.Errorf("notifier saw %+v", seen)
		}
		if seen[0].Delay != Delay(1) || seen[1].Delay != Delay(2) {
			t.Errorf("delays = %v, %v, want the backoff schedule", seen[0].Delay, seen[1].Delay)
		}
	})

	t.Run("attempts run out", func(t *testing.T) {
		calls := 0
		err := Retry(context.Background(), "GET x", nil, func() error { calls++; return transient })
		if calls != DefaultMaxAttempts || !errors.Is(err, transient) || !strings.Contains(err.Error(), "giving up after 4 attempts") {
			t.Fatalf("Retry() = %v after %d calls", err, calls)
		}
	})

	t.Run("permanent errors are not retried", func(t *testing.T) {
		calls := 0
		notified := false
		want := Permanent(errors.New("404"))
		err := Retry(context.Background(), "GET x", func(Attempt) { notified = true }, func() error { calls++; return want })
		if calls != 1 || !errors.Is(err, want) || notified {
			t.Fatalf("Retry() = %v after %d calls, notified=%v", err, calls, notified)
		}
	})

	t.Run("a requested wait replaces the backoff", func(t *testing.T) {
		calls := 0
		var delay time.Duration
		start := time.Now()
		err := Retry(context.Background(), "GET x", func(a Attempt) { delay = a.Delay }, func() error {
			calls++
			if calls == 1 {
				return askedError{wait: 20 * time.Millisecond}
			}
			return nil
		})
		if err != nil || calls != 2 || delay != 20*time.Millisecond {
			t.Fatalf("Retry() = %v after %d calls with delay %v", err, calls, delay)
		}
		if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
			t.Errorf("returned after %v, want at least the requested wait", elapsed)
		}
	})

	t.Run("a requested wait shorter than the backoff is used as it is", func(t *testing.T) {
		RetryBase, RetryMax = 20*time.Millisecond, 20*time.Millisecond
		defer func() { RetryBase, RetryMax = time.Millisecond, 4*time.Millisecond }()
		calls := 0
		var delay time.Duration
		err := Retry(context.Background(), "GET x", func(a Attempt) { delay = a.Delay }, func() error {
			calls++
			if calls == 1 {
				return askedError{wait: 2 * time.Millisecond}
			}
			return nil
		})
		if err != nil || calls != 2 || delay != 2*time.Millisecond {
			t.Fatalf("Retry() = %v after %d calls with delay %v, want the 2ms the server asked for", err, calls, delay)
		}
	})

	t.Run("a wait beyond the cap gives up at once", func(t *testing.T) {
		calls := 0
		asked := askedError{wait: time.Hour}
		err := Retry(context.Background(), "GET x", nil, func() error { calls++; return asked })
		if calls != 1 || !errors.Is(err, asked) || !strings.Contains(err.Error(), "asks to wait 1h0m0s") {
			t.Fatalf("Retry() = %v after %d calls", err, calls)
		}
	})

	t.Run("a cancelled context stops the wait", func(t *testing.T) {
		RetryBase, RetryMax = time.Hour, time.Hour
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
		err := Retry(ctx, "GET x", nil, func() error { calls++; return transient })
		if calls != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, transient) {
			t.Fatalf("Retry() = %v after %d calls", err, calls)
		}
	})
}

func TestRetryAfterHeader(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"absent", "", 0},
		{"seconds", "60", time.Minute},
		{"zero seconds", "0", 0},
		{"http date", now.Add(90 * time.Second).UTC().Format(http.TimeFormat), 90 * time.Second},
		{"http date in the past", now.Add(-time.Hour).UTC().Format(http.TimeFormat), time.Second},
		{"garbage", "soon", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.value != "" {
				h.Set("Retry-After", tt.value)
			}
			if got := RetryAfterHeader(h, now); got != tt.want {
				t.Errorf("RetryAfterHeader(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}

	base := errors.New("429")
	resp := &http.Response{Header: http.Header{"Retry-After": {"5"}}}
	wrapped := WithRetryAfter(base, resp)
	if asked, ok := errors.AsType[RetryAfterError](wrapped); !ok || asked.RetryAfter() != 5*time.Second || !errors.Is(wrapped, base) {
		t.Errorf("WithRetryAfter() = %#v, want a 5s wait wrapping the error", wrapped)
	}
	if got := WithRetryAfter(base, &http.Response{Header: http.Header{}}); got != base { //nolint:errorlint // identity is the point: nothing may wrap it
		t.Errorf("WithRetryAfter() without the header = %#v, want the error itself", got)
	}
}

func TestClassifyDecode(t *testing.T) {
	var target struct {
		N int `json:"N"`
	}
	malformed := json.Unmarshal([]byte(`{"N": nope}`), &target)
	mismatch := json.Unmarshal([]byte(`{"N": "x"}`), &target)
	if !IsPermanent(ClassifyDecode(malformed)) || !IsPermanent(ClassifyDecode(mismatch)) {
		t.Error("a malformed body must be permanent")
	}
	for _, err := range []error{io.ErrUnexpectedEOF, io.EOF, errors.New("read tcp: connection reset by peer")} {
		if IsPermanent(ClassifyDecode(fmt.Errorf("decode: %w", err))) {
			t.Errorf("%v must stay transient", err)
		}
	}
}
