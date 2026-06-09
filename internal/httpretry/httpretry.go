// Package httpretry holds the shared download retry policy: the
// permanent-vs-transient error classification, the retryable HTTP status set,
// and the exponential backoff schedule. It is used by binmanager (artifact
// downloads) and ocidigest (OCI blob pulls) so both paths retry transient
// network failures identically.
package httpretry

import (
	"errors"
	"net/http"
	"time"
)

// DefaultMaxAttempts bounds a retried download: flaky mirrors and transient
// TLS/connection blips fail individual attempts; a bounded retry turns those
// into success instead of failing the whole operation.
const DefaultMaxAttempts = 4

// Backoff bounds are vars (not consts) only so tests can shrink them;
// production keeps the 1s base / 8s cap.
var (
	RetryBase = time.Second
	RetryMax  = 8 * time.Second
)

// PermanentError marks a failure that retrying cannot fix (a 4xx response, an
// oversized file, a hash or digest mismatch). Retry loops give up on it
// immediately.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }

// Unwrap exposes the wrapped error for errors.Is/As chains.
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err so IsPermanent reports true for it.
func Permanent(err error) error { return &PermanentError{Err: err} }

// IsPermanent reports whether err (or anything it wraps) was marked permanent.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// RetryableStatus reports whether an HTTP status is worth retrying: 5xx server
// errors plus 408 (Request Timeout) and 429 (Too Many Requests). Other 4xx are
// permanent (a bad URL won't become good on retry).
func RetryableStatus(code int) bool {
	return code >= 500 || code == http.StatusRequestTimeout || code == http.StatusTooManyRequests
}

// Delay is the exponential backoff before attempt+1 (1s, 2s, 4s, … capped).
func Delay(attempt int) time.Duration {
	return min(RetryBase<<(attempt-1), RetryMax)
}
