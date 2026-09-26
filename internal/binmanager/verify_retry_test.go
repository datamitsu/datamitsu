package binmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpretry"
)

func fastVerifyRetries(t *testing.T) {
	t.Helper()
	base, maxDelay := httpretry.RetryBase, httpretry.RetryMax
	httpretry.RetryBase, httpretry.RetryMax = time.Millisecond, 4*time.Millisecond
	prev := VerifyRetryNotifier
	t.Cleanup(func() {
		httpretry.RetryBase, httpretry.RetryMax = base, maxDelay
		VerifyRetryNotifier = prev
	})
}

// A verification download fails like any other request: transiently, so it
// is retried and reported, and when the attempts run out the error says the
// asset was never seen rather than that it was bad.
func TestVerifyBinaryExtraction_DownloadRetries(t *testing.T) {
	fastVerifyRetries(t)
	body := []byte("#!/bin/sh\necho ok\n")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])

	t.Run("a transient failure is retried and reported", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			if attempts == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write(body)
		}))
		defer srv.Close()

		var seen []httpretry.Attempt
		VerifyRetryNotifier = func(a httpretry.Attempt) { seen = append(seen, a) }

		if err := VerifyBinaryExtraction(context.Background(), srv.URL, hash, BinHashTypeSHA256, BinContentTypeBinary, nil); err != nil {
			t.Fatalf("VerifyBinaryExtraction() = %v", err)
		}
		if attempts != 2 || len(seen) != 1 || seen[0].What != "GET "+srv.URL || !strings.Contains(seen[0].Err.Error(), "502") {
			t.Errorf("attempts = %d, notifier saw %+v", attempts, seen)
		}
	})

	t.Run("exhausted attempts are a download error", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer srv.Close()

		err := VerifyBinaryExtraction(context.Background(), srv.URL, hash, BinHashTypeSHA256, BinContentTypeBinary, nil)
		if !IsDownloadError(err) || attempts != httpretry.DefaultMaxAttempts {
			t.Fatalf("VerifyBinaryExtraction() = %v after %d attempts, want a DownloadError after %d", err, attempts, httpretry.DefaultMaxAttempts)
		}
		if !strings.Contains(err.Error(), "download failed: giving up after") {
			t.Errorf("error = %v, want the attempts named", err)
		}
	})

	t.Run("a 404 is a download error without retries", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		err := VerifyBinaryExtraction(context.Background(), srv.URL, hash, BinHashTypeSHA256, BinContentTypeBinary, nil)
		if !IsDownloadError(err) || attempts != 1 {
			t.Fatalf("VerifyBinaryExtraction() = %v after %d attempts, want a DownloadError after 1", err, attempts)
		}
	})

	t.Run("a wrong hash is the asset's fault, not a download error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(body)
		}))
		defer srv.Close()

		err := VerifyBinaryExtraction(context.Background(), srv.URL, strings.Repeat("0", 64), BinHashTypeSHA256, BinContentTypeBinary, nil)
		if err == nil || IsDownloadError(err) || !strings.Contains(err.Error(), "hash verification failed") {
			t.Fatalf("VerifyBinaryExtraction() = %v, want a hash mismatch that is not a DownloadError", err)
		}
	})

	t.Run("a Retry-After is waited out", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			if attempts == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(body)
		}))
		defer srv.Close()

		var seen []httpretry.Attempt
		VerifyRetryNotifier = func(a httpretry.Attempt) { seen = append(seen, a) }

		if err := VerifyBinaryExtraction(context.Background(), srv.URL, hash, BinHashTypeSHA256, BinContentTypeBinary, nil); err != nil {
			t.Fatalf("VerifyBinaryExtraction() = %v", err)
		}
		if attempts != 2 || len(seen) != 1 || seen[0].Delay != time.Second {
			t.Errorf("attempts = %d, notifier saw %+v, want one attempt waiting the second asked for", attempts, seen)
		}
	})

	t.Run("a Retry-After beyond the cap is not waited for", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		err := VerifyBinaryExtraction(context.Background(), srv.URL, hash, BinHashTypeSHA256, BinContentTypeBinary, nil)
		if !IsDownloadError(err) || attempts != 1 || !strings.Contains(err.Error(), "asks to wait 1h0m0s") {
			t.Fatalf("VerifyBinaryExtraction() = %v after %d attempts", err, attempts)
		}
	})

	t.Run("DownloadFileForVerify retries the same way", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			if attempts == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(body)
		}))
		defer srv.Close()

		if _, err := DownloadFileForVerify(context.Background(), srv.URL, t.TempDir()); err != nil || attempts != 2 {
			t.Fatalf("DownloadFileForVerify() = %v after %d attempts", err, attempts)
		}
	})
}
