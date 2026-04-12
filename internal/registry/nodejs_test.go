package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetLatestNodeLTSVersion(t *testing.T) {
	t.Run("successful fetch returns latest LTS", func(t *testing.T) {
		releases := []nodejsRelease{
			{Cycle: "25", Latest: "25.1.0", LTS: false, EOL: false},
			{Cycle: "24", Latest: "24.14.0", LTS: "Nomad", EOL: false},
			{Cycle: "22", Latest: "22.16.0", LTS: "Jod", EOL: false},
			{Cycle: "20", Latest: "20.19.0", LTS: "Iron", EOL: "2026-04-30"},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(releases)
		}))
		defer server.Close()

		origClient := nodejsHTTPClient
		nodejsHTTPClient = server.Client()
		defer func() { nodejsHTTPClient = origClient }()

		version, err := getLatestNodeLTSVersionFromURL(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if version != "24.14.0" {
			t.Errorf("expected '24.14.0', got '%s'", version)
		}
	})

	t.Run("LTS with boolean true", func(t *testing.T) {
		releases := []nodejsRelease{
			{Cycle: "24", Latest: "24.14.0", LTS: true, EOL: false},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(releases)
		}))
		defer server.Close()

		origClient := nodejsHTTPClient
		nodejsHTTPClient = server.Client()
		defer func() { nodejsHTTPClient = origClient }()

		version, err := getLatestNodeLTSVersionFromURL(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if version != "24.14.0" {
			t.Errorf("expected '24.14.0', got '%s'", version)
		}
	})

	t.Run("skips non-LTS versions", func(t *testing.T) {
		releases := []nodejsRelease{
			{Cycle: "25", Latest: "25.1.0", LTS: false, EOL: false},
			{Cycle: "23", Latest: "23.11.0", LTS: false, EOL: true},
			{Cycle: "22", Latest: "22.16.0", LTS: "Jod", EOL: false},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(releases)
		}))
		defer server.Close()

		origClient := nodejsHTTPClient
		nodejsHTTPClient = server.Client()
		defer func() { nodejsHTTPClient = origClient }()

		version, err := getLatestNodeLTSVersionFromURL(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if version != "22.16.0" {
			t.Errorf("expected '22.16.0', got '%s'", version)
		}
	})

	t.Run("no LTS versions returns fallback", func(t *testing.T) {
		releases := []nodejsRelease{
			{Cycle: "25", Latest: "25.1.0", LTS: false, EOL: false},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(releases)
		}))
		defer server.Close()

		origClient := nodejsHTTPClient
		nodejsHTTPClient = server.Client()
		defer func() { nodejsHTTPClient = origClient }()

		version, err := getLatestNodeLTSVersionFromURL(server.URL)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != nodejsFallbackLTSVersion {
			t.Errorf("expected fallback '%s', got '%s'", nodejsFallbackLTSVersion, version)
		}
	})

	t.Run("server error returns fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		}))
		defer server.Close()

		origClient := nodejsHTTPClient
		nodejsHTTPClient = server.Client()
		defer func() { nodejsHTTPClient = origClient }()

		version, err := getLatestNodeLTSVersionFromURL(server.URL)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != nodejsFallbackLTSVersion {
			t.Errorf("expected fallback '%s', got '%s'", nodejsFallbackLTSVersion, version)
		}
	})

	t.Run("invalid JSON returns fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not json"))
		}))
		defer server.Close()

		origClient := nodejsHTTPClient
		nodejsHTTPClient = server.Client()
		defer func() { nodejsHTTPClient = origClient }()

		version, err := getLatestNodeLTSVersionFromURL(server.URL)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != nodejsFallbackLTSVersion {
			t.Errorf("expected fallback '%s', got '%s'", nodejsFallbackLTSVersion, version)
		}
	})

	t.Run("connection error returns fallback", func(t *testing.T) {
		version, err := getLatestNodeLTSVersionFromURL("http://127.0.0.1:1")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != nodejsFallbackLTSVersion {
			t.Errorf("expected fallback '%s', got '%s'", nodejsFallbackLTSVersion, version)
		}
	})

	t.Run("empty releases returns fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		}))
		defer server.Close()

		origClient := nodejsHTTPClient
		nodejsHTTPClient = server.Client()
		defer func() { nodejsHTTPClient = origClient }()

		version, err := getLatestNodeLTSVersionFromURL(server.URL)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != nodejsFallbackLTSVersion {
			t.Errorf("expected fallback '%s', got '%s'", nodejsFallbackLTSVersion, version)
		}
	})
}

func TestFilterLatestLTS(t *testing.T) {
	t.Run("returns first LTS version", func(t *testing.T) {
		releases := []nodejsRelease{
			{Cycle: "25", Latest: "25.1.0", LTS: false},
			{Cycle: "24", Latest: "24.14.0", LTS: "Nomad"},
			{Cycle: "22", Latest: "22.16.0", LTS: "Jod"},
		}
		got := filterLatestLTS(releases)
		if got != "24.14.0" {
			t.Errorf("expected '24.14.0', got '%s'", got)
		}
	})

	t.Run("empty list returns empty", func(t *testing.T) {
		got := filterLatestLTS(nil)
		if got != "" {
			t.Errorf("expected empty, got '%s'", got)
		}
	})

	t.Run("all non-LTS returns empty", func(t *testing.T) {
		releases := []nodejsRelease{
			{Cycle: "25", Latest: "25.1.0", LTS: false},
			{Cycle: "23", Latest: "23.11.0", LTS: false},
		}
		got := filterLatestLTS(releases)
		if got != "" {
			t.Errorf("expected empty, got '%s'", got)
		}
	})

	t.Run("skips EOL LTS versions", func(t *testing.T) {
		releases := []nodejsRelease{
			{Cycle: "20", Latest: "20.19.0", LTS: "Iron", EOL: "2024-01-01"},
			{Cycle: "22", Latest: "22.16.0", LTS: "Jod", EOL: false},
		}
		got := filterLatestLTS(releases)
		if got != "22.16.0" {
			t.Errorf("expected '22.16.0', got '%s'", got)
		}
	})

	t.Run("all LTS versions EOL returns empty", func(t *testing.T) {
		releases := []nodejsRelease{
			{Cycle: "20", Latest: "20.19.0", LTS: "Iron", EOL: true},
			{Cycle: "18", Latest: "18.20.0", LTS: "Hydrogen", EOL: "2024-01-01"},
		}
		got := filterLatestLTS(releases)
		if got != "" {
			t.Errorf("expected empty, got '%s'", got)
		}
	})

	t.Run("future EOL date is not EOL", func(t *testing.T) {
		releases := []nodejsRelease{
			{Cycle: "24", Latest: "24.14.0", LTS: "Nomad", EOL: "2030-04-30"},
		}
		got := filterLatestLTS(releases)
		if got != "24.14.0" {
			t.Errorf("expected '24.14.0', got '%s'", got)
		}
	})
}

func TestIsLTS(t *testing.T) {
	tests := []struct {
		name string
		lts  interface{}
		want bool
	}{
		{"bool true", true, true},
		{"bool false", false, false},
		{"string codename", "Nomad", true},
		{"empty string", "", false},
		{"string false", "false", false},
		{"nil", nil, false},
		{"number", float64(1), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := nodejsRelease{LTS: tt.lts}
			got := isLTS(r)
			if got != tt.want {
				t.Errorf("isLTS(%v) = %v, want %v", tt.lts, got, tt.want)
			}
		})
	}
}
