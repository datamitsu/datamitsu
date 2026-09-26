package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpretry"
)

func fastRetries(t *testing.T) {
	t.Helper()
	base, maxDelay := httpretry.RetryBase, httpretry.RetryMax
	httpretry.RetryBase, httpretry.RetryMax = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { httpretry.RetryBase, httpretry.RetryMax = base, maxDelay })
}

func TestGetJSON(t *testing.T) {
	fastRetries(t)

	t.Run("a server error is retried and reported", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			if attempts < 3 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"name":"demo"}`))
		}))
		defer srv.Close()

		var seen []httpretry.Attempt
		prev := RetryNotifier
		RetryNotifier = func(a httpretry.Attempt) { seen = append(seen, a) }
		defer func() { RetryNotifier = prev }()

		var out struct{ Name string }
		if err := getJSON(context.Background(), srv.Client(), "demo registry", srv.URL, 1<<20, &out); err != nil || out.Name != "demo" {
			t.Fatalf("getJSON() = %v, name %q", err, out.Name)
		}
		if attempts != 3 || len(seen) != 2 || !strings.Contains(seen[0].Err.Error(), "demo registry returned status 502") {
			t.Errorf("attempts = %d, notified %+v", attempts, seen)
		}
	})

	t.Run("a 404 is permanent and recognizable", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		var out struct{}
		err := getJSON(context.Background(), srv.Client(), "demo registry", srv.URL, 1<<20, &out)
		if !errors.Is(err, errNotFound) || attempts != 1 {
			t.Fatalf("getJSON() = %v after %d attempts", err, attempts)
		}
	})

	t.Run("another 4xx is permanent", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		var out struct{}
		err := getJSON(context.Background(), srv.Client(), "demo registry", srv.URL, 1<<20, &out)
		if err == nil || attempts != 1 || !strings.Contains(err.Error(), "status 401") {
			t.Fatalf("getJSON() = %v after %d attempts", err, attempts)
		}
	})

	t.Run("attempts run out on a persistent failure", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		var out struct{}
		err := getJSON(context.Background(), srv.Client(), "demo registry", srv.URL, 1<<20, &out)
		if err == nil || attempts != httpretry.DefaultMaxAttempts || !strings.Contains(err.Error(), "giving up after") {
			t.Fatalf("getJSON() = %v after %d attempts", err, attempts)
		}
	})

	t.Run("malformed JSON is permanent", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			_, _ = w.Write([]byte(`{"name": nope}`))
		}))
		defer srv.Close()

		var out struct{ Name string }
		err := getJSON(context.Background(), srv.Client(), "demo registry", srv.URL, 1<<20, &out)
		if err == nil || attempts != 1 || !strings.Contains(err.Error(), "failed to decode demo registry response") {
			t.Fatalf("getJSON() = %v after %d attempts", err, attempts)
		}
	})

	t.Run("a body cut short is retried", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			if attempts == 1 {
				_, _ = w.Write([]byte(`{"name":`))
				return
			}
			_, _ = w.Write([]byte(`{"name":"demo"}`))
		}))
		defer srv.Close()

		var out struct{ Name string }
		if err := getJSON(context.Background(), srv.Client(), "demo registry", srv.URL, 1<<20, &out); err != nil || attempts != 2 || out.Name != "demo" {
			t.Fatalf("getJSON() = %v after %d attempts, name %q", err, attempts, out.Name)
		}
	})

	t.Run("a 429 waits out its Retry-After", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			if attempts == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`{"name":"demo"}`))
		}))
		defer srv.Close()

		var seen []httpretry.Attempt
		prev := RetryNotifier
		RetryNotifier = func(a httpretry.Attempt) { seen = append(seen, a) }
		defer func() { RetryNotifier = prev }()

		var out struct{ Name string }
		if err := getJSON(context.Background(), srv.Client(), "demo registry", srv.URL, 1<<20, &out); err != nil || attempts != 2 {
			t.Fatalf("getJSON() = %v after %d attempts", err, attempts)
		}
		if len(seen) != 1 || seen[0].Delay != time.Second {
			t.Errorf("notifier saw %+v, want one attempt waiting the second asked for", seen)
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

		var out struct{}
		err := getJSON(context.Background(), srv.Client(), "demo registry", srv.URL, 1<<20, &out)
		if err == nil || attempts != 1 || !strings.Contains(err.Error(), "asks to wait 1h0m0s") {
			t.Fatalf("getJSON() = %v after %d attempts", err, attempts)
		}
	})
}

// The npm and PyPI lookups keep naming the missing package, since a 404 from
// the registry is what a mistyped packageName looks like.
func TestNotFoundMessagesNameThePackage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	npmPrev := npmHTTPClient
	npmHTTPClient = srv.Client()
	pypiPrev := pypiHTTPClient
	pypiHTTPClient = srv.Client()
	defer func() { npmHTTPClient, pypiHTTPClient = npmPrev, pypiPrev }()

	if _, err := getNPMPackageInfoFromURL(context.Background(), srv.URL+"/nope/latest", "nope"); err == nil || !strings.Contains(err.Error(), `npm package "nope" not found`) {
		t.Errorf("npm: %v", err)
	}
	if _, err := getPyPIPackageInfoFromURL(context.Background(), srv.URL+"/pypi/nope/json", "nope"); err == nil || !strings.Contains(err.Error(), `PyPI package "nope" not found`) {
		t.Errorf("PyPI: %v", err)
	}
}
