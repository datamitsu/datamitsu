package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetNPMPackageInfo(t *testing.T) {
	t.Run("successful fetch", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/cspell/latest" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(npmLatestResponse{
				Name:        "cspell",
				Version:     "9.7.0",
				Description: "A spell checker for code",
			})
		}))
		defer server.Close()

		origClient := npmHTTPClient
		npmHTTPClient = server.Client()
		defer func() { npmHTTPClient = origClient }()

		info, err := getNPMPackageInfoFromURL(server.URL+"/cspell/latest", "cspell")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info.Name != "cspell" {
			t.Errorf("expected name 'cspell', got '%s'", info.Name)
		}
		if info.Version != "9.7.0" {
			t.Errorf("expected version '9.7.0', got '%s'", info.Version)
		}
		if info.Description != "A spell checker for code" {
			t.Errorf("expected description 'A spell checker for code', got '%s'", info.Description)
		}
	})

	t.Run("package not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		origClient := npmHTTPClient
		npmHTTPClient = server.Client()
		defer func() { npmHTTPClient = origClient }()

		_, err := getNPMPackageInfoFromURL(server.URL+"/nonexistent/latest", "nonexistent")
		if err == nil {
			t.Fatal("expected error for 404, got nil")
		}
	})

	t.Run("server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		}))
		defer server.Close()

		origClient := npmHTTPClient
		npmHTTPClient = server.Client()
		defer func() { npmHTTPClient = origClient }()

		_, err := getNPMPackageInfoFromURL(server.URL+"/pkg/latest", "pkg")
		if err == nil {
			t.Fatal("expected error for 500, got nil")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not json"))
		}))
		defer server.Close()

		origClient := npmHTTPClient
		npmHTTPClient = server.Client()
		defer func() { npmHTTPClient = origClient }()

		_, err := getNPMPackageInfoFromURL(server.URL+"/pkg/latest", "pkg")
		if err == nil {
			t.Fatal("expected error for invalid JSON, got nil")
		}
	})

	t.Run("scoped package name", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/@mermaid-js/mermaid-cli/latest" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(npmLatestResponse{
				Name:        "@mermaid-js/mermaid-cli",
				Version:     "11.12.0",
				Description: "Mermaid CLI",
			})
		}))
		defer server.Close()

		origClient := npmHTTPClient
		npmHTTPClient = server.Client()
		defer func() { npmHTTPClient = origClient }()

		info, err := getNPMPackageInfoFromURL(server.URL+"/@mermaid-js/mermaid-cli/latest", "@mermaid-js/mermaid-cli")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info.Name != "@mermaid-js/mermaid-cli" {
			t.Errorf("expected name '@mermaid-js/mermaid-cli', got '%s'", info.Name)
		}
		if info.Version != "11.12.0" {
			t.Errorf("expected version '11.12.0', got '%s'", info.Version)
		}
	})
}
