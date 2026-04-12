package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetLatestPythonStableVersion(t *testing.T) {
	t.Run("successful fetch returns latest stable", func(t *testing.T) {
		releases := []pythonRelease{
			{Cycle: "3.14", Latest: "3.14.3", EOL: false},
			{Cycle: "3.13", Latest: "3.13.5", EOL: false},
			{Cycle: "3.12", Latest: "3.12.11", EOL: false},
			{Cycle: "3.11", Latest: "3.11.13", EOL: "2027-10-24"},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(releases)
		}))
		defer server.Close()

		origClient := pythonHTTPClient
		pythonHTTPClient = server.Client()
		defer func() { pythonHTTPClient = origClient }()

		version, err := getLatestPythonStableVersionFromURL(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if version != "3.14.3" {
			t.Errorf("expected '3.14.3', got '%s'", version)
		}
	})

	t.Run("skips EOL versions with string date", func(t *testing.T) {
		releases := []pythonRelease{
			{Cycle: "3.9", Latest: "3.9.22", EOL: "2025-10-05"},
			{Cycle: "3.13", Latest: "3.13.5", EOL: false},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(releases)
		}))
		defer server.Close()

		origClient := pythonHTTPClient
		pythonHTTPClient = server.Client()
		defer func() { pythonHTTPClient = origClient }()

		version, err := getLatestPythonStableVersionFromURL(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if version != "3.13.5" {
			t.Errorf("expected '3.13.5', got '%s'", version)
		}
	})

	t.Run("skips EOL versions with bool true", func(t *testing.T) {
		releases := []pythonRelease{
			{Cycle: "2.7", Latest: "2.7.18", EOL: true},
			{Cycle: "3.14", Latest: "3.14.3", EOL: false},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(releases)
		}))
		defer server.Close()

		origClient := pythonHTTPClient
		pythonHTTPClient = server.Client()
		defer func() { pythonHTTPClient = origClient }()

		version, err := getLatestPythonStableVersionFromURL(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if version != "3.14.3" {
			t.Errorf("expected '3.14.3', got '%s'", version)
		}
	})

	t.Run("all EOL returns fallback", func(t *testing.T) {
		releases := []pythonRelease{
			{Cycle: "2.7", Latest: "2.7.18", EOL: true},
			{Cycle: "3.8", Latest: "3.8.20", EOL: "2024-10-07"},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(releases)
		}))
		defer server.Close()

		origClient := pythonHTTPClient
		pythonHTTPClient = server.Client()
		defer func() { pythonHTTPClient = origClient }()

		version, err := getLatestPythonStableVersionFromURL(server.URL)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != pythonFallbackStableVersion {
			t.Errorf("expected fallback '%s', got '%s'", pythonFallbackStableVersion, version)
		}
	})

	t.Run("server error returns fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		}))
		defer server.Close()

		origClient := pythonHTTPClient
		pythonHTTPClient = server.Client()
		defer func() { pythonHTTPClient = origClient }()

		version, err := getLatestPythonStableVersionFromURL(server.URL)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != pythonFallbackStableVersion {
			t.Errorf("expected fallback '%s', got '%s'", pythonFallbackStableVersion, version)
		}
	})

	t.Run("invalid JSON returns fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not json"))
		}))
		defer server.Close()

		origClient := pythonHTTPClient
		pythonHTTPClient = server.Client()
		defer func() { pythonHTTPClient = origClient }()

		version, err := getLatestPythonStableVersionFromURL(server.URL)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != pythonFallbackStableVersion {
			t.Errorf("expected fallback '%s', got '%s'", pythonFallbackStableVersion, version)
		}
	})

	t.Run("connection error returns fallback", func(t *testing.T) {
		version, err := getLatestPythonStableVersionFromURL("http://127.0.0.1:1")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != pythonFallbackStableVersion {
			t.Errorf("expected fallback '%s', got '%s'", pythonFallbackStableVersion, version)
		}
	})

	t.Run("empty releases returns fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		}))
		defer server.Close()

		origClient := pythonHTTPClient
		pythonHTTPClient = server.Client()
		defer func() { pythonHTTPClient = origClient }()

		version, err := getLatestPythonStableVersionFromURL(server.URL)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != pythonFallbackStableVersion {
			t.Errorf("expected fallback '%s', got '%s'", pythonFallbackStableVersion, version)
		}
	})
}

func TestFilterLatestStablePython(t *testing.T) {
	t.Run("returns first non-EOL version", func(t *testing.T) {
		releases := []pythonRelease{
			{Cycle: "3.14", Latest: "3.14.3", EOL: false},
			{Cycle: "3.13", Latest: "3.13.5", EOL: false},
		}
		got := filterLatestStablePython(releases)
		if got != "3.14.3" {
			t.Errorf("expected '3.14.3', got '%s'", got)
		}
	})

	t.Run("empty list returns empty", func(t *testing.T) {
		got := filterLatestStablePython(nil)
		if got != "" {
			t.Errorf("expected empty, got '%s'", got)
		}
	})

	t.Run("all EOL returns empty", func(t *testing.T) {
		releases := []pythonRelease{
			{Cycle: "2.7", Latest: "2.7.18", EOL: true},
			{Cycle: "3.8", Latest: "3.8.20", EOL: "2024-10-07"},
		}
		got := filterLatestStablePython(releases)
		if got != "" {
			t.Errorf("expected empty, got '%s'", got)
		}
	})
}

func TestIsEOL(t *testing.T) {
	tests := []struct {
		name string
		eol  interface{}
		want bool
	}{
		{"bool true", true, true},
		{"bool false", false, false},
		{"past date is EOL", "2020-01-01", true},
		{"future date is not EOL", "2099-12-31", false},
		{"empty string", "", false},
		{"string false", "false", false},
		{"invalid date string treated as EOL", "not-a-date", true},
		{"TBD treated as EOL", "TBD", true},
		{"nil", nil, false},
		{"number", float64(1), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEOLField(tt.eol)
			if got != tt.want {
				t.Errorf("parseEOLField(%v) = %v, want %v", tt.eol, got, tt.want)
			}
		})
	}
}
