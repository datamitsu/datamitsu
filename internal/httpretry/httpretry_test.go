package httpretry

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestPermanentWrapping(t *testing.T) {
	base := errors.New("boom")
	wrapped := Permanent(base)

	if !IsPermanent(wrapped) {
		t.Error("IsPermanent(Permanent(err)) = false, want true")
	}
	if IsPermanent(base) {
		t.Error("IsPermanent(plain err) = true, want false")
	}
	if !errors.Is(wrapped, base) {
		t.Error("Permanent must unwrap to the original error")
	}
	if wrapped.Error() != base.Error() {
		t.Errorf("Error() = %q, want %q", wrapped.Error(), base.Error())
	}
	if !IsPermanent(fmt.Errorf("context: %w", wrapped)) {
		t.Error("IsPermanent must see through further wrapping")
	}
}

func TestRetryableStatus(t *testing.T) {
	for _, code := range []int{500, 502, 503, 504, http.StatusRequestTimeout, http.StatusTooManyRequests} {
		if !RetryableStatus(code) {
			t.Errorf("RetryableStatus(%d) = false, want true", code)
		}
	}
	for _, code := range []int{200, 301, 400, 401, 403, 404, 410} {
		if RetryableStatus(code) {
			t.Errorf("RetryableStatus(%d) = true, want false", code)
		}
	}
}

func TestDelay(t *testing.T) {
	origBase, origMax := RetryBase, RetryMax
	RetryBase, RetryMax = time.Second, 8*time.Second
	t.Cleanup(func() { RetryBase, RetryMax = origBase, origMax })

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for i, expected := range want {
		if got := Delay(i + 1); got != expected {
			t.Errorf("Delay(%d) = %v, want %v", i+1, got, expected)
		}
	}
}
