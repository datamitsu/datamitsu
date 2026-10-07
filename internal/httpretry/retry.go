package httpretry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Attempt describes one failed attempt that Retry is about to repeat.
type Attempt struct {
	// What names the request, e.g. "GET https://api.github.com/repos/o/r/releases".
	What string
	// Attempt is the number of the attempt that failed, counted from 1; Max is
	// how many Retry makes in total.
	Attempt, Max int
	Err          error
	// Delay is the wait before the next attempt.
	Delay time.Duration
}

// Notifier receives every retried attempt, so a command can show what it is
// waiting for. A nil Notifier retries silently.
type Notifier func(Attempt)

// RetryAfterError is an error that carries the wait a server asked for, such as
// a rate limit's Retry-After. Retry honours it in place of its own backoff and
// gives up when it exceeds MaxRetryAfter.
type RetryAfterError interface {
	error
	RetryAfter() time.Duration
}

// MaxRetryAfter bounds the wait a server may ask of a retry. A GitHub secondary
// rate limit asks for a minute; a primary limit resets within the hour, which is
// not a wait a command should sit through. A var only so tests can shrink it.
var MaxRetryAfter = 2 * time.Minute

// WaitError carries the wait a response asked for through Retry-After.
type WaitError struct {
	Err  error
	Wait time.Duration
}

func (e *WaitError) Error() string { return e.Err.Error() }

// Unwrap exposes the wrapped error for errors.Is/As chains.
func (e *WaitError) Unwrap() error { return e.Err }

// RetryAfter is the wait the response asked for; see RetryAfterError.
func (e *WaitError) RetryAfter() time.Duration { return e.Wait }

// WithRetryAfter attaches the wait a response's Retry-After header asks for to
// err, so Retry honours it. Without the header err is returned as it is.
func WithRetryAfter(err error, resp *http.Response) error {
	if wait := RetryAfterHeader(resp.Header, time.Now()); wait > 0 {
		return &WaitError{Err: err, Wait: wait}
	}
	return err
}

// RetryAfterHeader reads a Retry-After header, given in seconds or as an HTTP
// date, as the wait it asks for; zero when there is none.
func RetryAfterHeader(h http.Header, now time.Time) time.Duration {
	value := h.Get("Retry-After")
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		return max(time.Duration(secs)*time.Second, 0)
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(at.Sub(now), time.Second)
	}
	return 0
}

// ClassifyDecode marks a JSON decode error permanent when the body itself was
// malformed, and leaves it transient when the read failed: a stream cut short
// or a connection reset is the network's doing and worth another attempt.
func ClassifyDecode(err error) error {
	var syntax *json.SyntaxError
	var mismatch *json.UnmarshalTypeError
	var invalid *json.InvalidUnmarshalError
	if errors.As(err, &syntax) || errors.As(err, &mismatch) || errors.As(err, &invalid) {
		return Permanent(err)
	}
	return err
}

// Retry runs op until it succeeds, returns a permanent error, exceeds
// DefaultMaxAttempts or ctx ends. A failed attempt that will be repeated is
// reported to notify before the wait: the backoff schedule, or the wait the
// server asked for when the error names one. The error of the last attempt is
// returned as is when it is permanent, and wrapped with the attempt count when
// the attempts ran out, so a report can tell the two apart.
func Retry(ctx context.Context, what string, notify Notifier, op func() error) error {
	for attempt := 1; ; attempt++ {
		err := op()
		if err == nil {
			return nil
		}
		if IsPermanent(err) {
			return err
		}
		if ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}
		if attempt >= DefaultMaxAttempts {
			return fmt.Errorf("giving up after %d attempts: %w", attempt, err)
		}

		delay := Delay(attempt)
		if asked, ok := errors.AsType[RetryAfterError](err); ok {
			wait := asked.RetryAfter()
			if wait > MaxRetryAfter {
				return fmt.Errorf("not retried, the server asks to wait %s: %w", wait.Round(time.Second), err)
			}
			if wait > 0 {
				delay = wait
			}
		}

		if notify != nil {
			notify(Attempt{What: what, Attempt: attempt, Max: DefaultMaxAttempts, Err: err, Delay: delay})
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
	}
}
