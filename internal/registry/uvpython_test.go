package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// metadataFixture mirrors the shape of uv's download-metadata.json: one record
// per interpreter/platform combination, keyed by a descriptive slug.
func metadataFixture() map[string]uvPythonEntry {
	return map[string]uvPythonEntry{
		"cpython-3.14.6-linux-x86_64-gnu":  {Name: "cpython", Major: 3, Minor: 14, Patch: 6},
		"cpython-3.14.6-darwin-aarch64":    {Name: "cpython", Major: 3, Minor: 14, Patch: 6},
		"cpython-3.14.5-linux-x86_64-gnu":  {Name: "cpython", Major: 3, Minor: 14, Patch: 5},
		"cpython-3.13.12-linux-x86_64-gnu": {Name: "cpython", Major: 3, Minor: 13, Patch: 12},
		"cpython-3.15.0b4-linux-x86_64":    {Name: "cpython", Major: 3, Minor: 15, Patch: 0, Prerelease: "b4"},
		"pypy-3.11.15-linux-x86_64-gnu":    {Name: "pypy", Major: 3, Minor: 11, Patch: 15},
		"graalpy-3.12.0-linux-x86_64-gnu":  {Name: "graalpy", Major: 3, Minor: 12, Patch: 0},
	}
}

func serveMetadata(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	orig := uvPythonHTTPClient
	uvPythonHTTPClient = server.Client()
	t.Cleanup(func() { uvPythonHTTPClient = orig })

	return server.URL
}

func TestGetUVSupportedPythonVersions(t *testing.T) {
	t.Run("collects stable CPython versions only", func(t *testing.T) {
		url := serveMetadata(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(metadataFixture())
		})

		versions, err := getUVSupportedPythonVersionsFromURL(context.Background(), url)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, want := range []string{"3.14.6", "3.14.5", "3.13.12"} {
			if !versions[want] {
				t.Errorf("expected %s to be supported", want)
			}
		}
		// Pre-releases and non-CPython interpreters must not become pin candidates.
		for _, unwanted := range []string{"3.15.0", "3.11.15", "3.12.0"} {
			if versions[unwanted] {
				t.Errorf("expected %s to be excluded", unwanted)
			}
		}
		if len(versions) != 3 {
			t.Errorf("expected 3 versions, got %d: %v", len(versions), versions)
		}
	})

	t.Run("server error is an error", func(t *testing.T) {
		url := serveMetadata(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("404: Not Found"))
		})

		if _, err := getUVSupportedPythonVersionsFromURL(context.Background(), url); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("invalid JSON is an error", func(t *testing.T) {
		url := serveMetadata(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not json"))
		})

		if _, err := getUVSupportedPythonVersionsFromURL(context.Background(), url); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	// An empty result would silently disable the guard in pullUVRuntime, so it
	// must surface as an error rather than an empty set.
	t.Run("metadata without stable CPython is an error", func(t *testing.T) {
		url := serveMetadata(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]uvPythonEntry{
				"pypy-3.11.15-linux-x86_64-gnu": {Name: "pypy", Major: 3, Minor: 11, Patch: 15},
			})
		})

		if _, err := getUVSupportedPythonVersionsFromURL(context.Background(), url); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("connection error is an error", func(t *testing.T) {
		if _, err := getUVSupportedPythonVersionsFromURL(context.Background(), "http://127.0.0.1:1"); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestLatestSupportedPythonPatch(t *testing.T) {
	supported := map[string]bool{
		"3.14.6":  true,
		"3.14.5":  true,
		"3.13.12": true,
	}

	tests := []struct {
		name      string
		want      string
		supported map[string]bool
		expected  string
	}{
		// The regression this whole guard exists for: uv 0.12.1 predates CPython
		// 3.14.7, so the pin must land on 3.14.6.
		{"downgrades to newest patch on the line", "3.14.7", supported, "3.14.6"},
		{"exact match returns itself", "3.14.6", supported, "3.14.6"},
		{"skips patches newer than requested", "3.14.5", supported, "3.14.5"},
		{"unknown minor line yields nothing", "3.15.1", supported, ""},
		{"never crosses to an older minor line", "3.16.0", supported, ""},
		{"empty support set yields nothing", "3.14.7", map[string]bool{}, ""},
		{"malformed version yields nothing", "3", supported, ""},
		{"empty version yields nothing", "", supported, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LatestSupportedPythonPatch(tt.supported, tt.want)
			if got != tt.expected {
				t.Errorf("LatestSupportedPythonPatch(_, %q) = %q, want %q", tt.want, got, tt.expected)
			}
		})
	}

	// Double-digit patches must compare numerically, not lexically: a string
	// comparison would rank "3.13.9" above "3.13.12".
	t.Run("compares patches numerically", func(t *testing.T) {
		got := LatestSupportedPythonPatch(map[string]bool{"3.13.9": true, "3.13.12": true}, "3.13.20")
		if got != "3.13.12" {
			t.Errorf("expected '3.13.12', got '%s'", got)
		}
	})
}
