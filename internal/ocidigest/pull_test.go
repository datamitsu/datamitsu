package ocidigest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpretry"
)

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// setFastBlobRetries shrinks the shared backoff so retry tests run instantly.
func setFastBlobRetries(t *testing.T) {
	t.Helper()
	origBase, origMax := httpretry.RetryBase, httpretry.RetryMax
	httpretry.RetryBase, httpretry.RetryMax = time.Millisecond, time.Millisecond
	t.Cleanup(func() { httpretry.RetryBase, httpretry.RetryMax = origBase, origMax })
}

func TestPullManifest_VerifiesBody(t *testing.T) {
	body := []byte(`{"schemaVersion":2,"layers":[]}`)
	digest := sha256Digest(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	got, err := r.PullManifest(context.Background(), "owner/repo", digest)
	if err != nil {
		t.Fatalf("PullManifest: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("PullManifest body = %q, want %q", got, body)
	}
}

func TestPullManifest_BodyDigestMismatchIsFatal(t *testing.T) {
	body := []byte(`{"schemaVersion":2}`)
	otherDigest := sha256Digest([]byte("something else entirely"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Lie in the header too — the body hash is the only trusted input.
		w.Header().Set("Docker-Content-Digest", otherDigest)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	_, err := r.PullManifest(context.Background(), "owner/repo", otherDigest)
	if err == nil {
		t.Fatal("expected digest mismatch error, got nil")
	}
	if !IsDigestMismatch(err) {
		t.Errorf("IsDigestMismatch(%v) = false, want true", err)
	}
}

func TestPullManifest_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	_, err := r.PullManifest(context.Background(), "owner/repo", sha256Digest([]byte("x")))
	if !errors.Is(err, ErrManifestNotFound) {
		t.Errorf("err = %v, want ErrManifestNotFound", err)
	}
}

func TestPullBlob_VerifiesDigestAndSize(t *testing.T) {
	blob := []byte("blob content here")
	digest := sha256Digest(blob)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(blob)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	path, err := r.PullBlob(context.Background(), "owner/repo", digest, int64(len(blob)), 1<<20, "test", t.TempDir())
	if err != nil {
		t.Fatalf("PullBlob: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pulled blob: %v", err)
	}
	if string(data) != string(blob) {
		t.Errorf("blob content = %q, want %q", data, blob)
	}
}

func TestPullBlob_DigestMismatchIsPermanentNoRetry(t *testing.T) {
	setFastBlobRetries(t)
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("tampered content"))
	}))
	defer srv.Close()

	wanted := sha256Digest([]byte("original content"))
	r := newTestResolver(srv.URL, t.TempDir())
	_, err := r.PullBlob(context.Background(), "owner/repo", wanted, int64(len("tampered content")), 1<<20, "test", t.TempDir())
	if err == nil {
		t.Fatal("expected digest mismatch error, got nil")
	}
	if !IsDigestMismatch(err) {
		t.Errorf("IsDigestMismatch(%v) = false, want true", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("server hits = %d, want 1 (no retry on digest mismatch)", got)
	}
}

func TestPullBlob_SizeMismatchIsPermanent(t *testing.T) {
	setFastBlobRetries(t)
	blob := []byte("content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(blob)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	_, err := r.PullBlob(context.Background(), "owner/repo", sha256Digest(blob), int64(len(blob))+5, 1<<20, "test", t.TempDir())
	if err == nil {
		t.Fatal("expected size mismatch error, got nil")
	}
	if !IsDigestMismatch(err) {
		t.Errorf("IsDigestMismatch(%v) = false, want true", err)
	}
	if !strings.Contains(err.Error(), "expected") {
		t.Errorf("error %q should carry expected/got detail", err)
	}
}

func TestPullBlob_RetriesTransientFailures(t *testing.T) {
	setFastBlobRetries(t)
	blob := []byte("eventually served")
	digest := sha256Digest(blob)

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(blob)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	path, err := r.PullBlob(context.Background(), "owner/repo", digest, int64(len(blob)), 1<<20, "test", t.TempDir())
	if err != nil {
		t.Fatalf("PullBlob after retries: %v", err)
	}
	if got := hits.Load(); got != 3 {
		t.Errorf("server hits = %d, want 3", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("pulled blob missing: %v", err)
	}
}

func TestAuthedGet_TokenIsCachedAcrossRequests(t *testing.T) {
	body := []byte(`{"schemaVersion":2,"layers":[]}`)
	digest := sha256Digest(body)

	var tokenHits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		tokenHits.Add(1)
		_, _ = w.Write([]byte(`{"token":"cached-token"}`))
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cached-token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="http://`+r.Host+`/token",service="t"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(body)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	for range 3 {
		if _, err := r.PullManifest(context.Background(), "owner/repo", digest); err != nil {
			t.Fatalf("PullManifest: %v", err)
		}
	}
	if got := tokenHits.Load(); got != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (token cached in process)", got)
	}
}

func TestPullBlob_OfflineRefused(t *testing.T) {
	t.Setenv("DATAMITSU_OFFLINE", "1")
	r := newTestResolver("http://127.0.0.1:1", t.TempDir())
	_, err := r.PullBlob(context.Background(), "owner/repo", sha256Digest([]byte("x")), 1, 1<<20, "test", t.TempDir())
	if err == nil {
		t.Fatal("expected offline error, got nil")
	}
	if !strings.Contains(err.Error(), "DATAMITSU_OFFLINE") {
		t.Errorf("error %q should mention DATAMITSU_OFFLINE", err)
	}
}
