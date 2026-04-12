package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetPyPIPackageInfo(t *testing.T) {
	t.Run("successful fetch", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/pypi/yamllint/json" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(pypiResponse{
				Info: pypiInfo{
					Name:    "yamllint",
					Version: "1.38.0",
					Summary: "A linter for YAML files",
				},
			})
		}))
		defer server.Close()

		origClient := pypiHTTPClient
		pypiHTTPClient = server.Client()
		defer func() { pypiHTTPClient = origClient }()

		info, err := getPyPIPackageInfoFromURL(server.URL+"/pypi/yamllint/json", "yamllint")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info.Name != "yamllint" {
			t.Errorf("expected name 'yamllint', got '%s'", info.Name)
		}
		if info.Version != "1.38.0" {
			t.Errorf("expected version '1.38.0', got '%s'", info.Version)
		}
		if info.Description != "A linter for YAML files" {
			t.Errorf("expected description 'A linter for YAML files', got '%s'", info.Description)
		}
	})

	t.Run("package not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		origClient := pypiHTTPClient
		pypiHTTPClient = server.Client()
		defer func() { pypiHTTPClient = origClient }()

		_, err := getPyPIPackageInfoFromURL(server.URL+"/pypi/nonexistent/json", "nonexistent")
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

		origClient := pypiHTTPClient
		pypiHTTPClient = server.Client()
		defer func() { pypiHTTPClient = origClient }()

		_, err := getPyPIPackageInfoFromURL(server.URL+"/pypi/pkg/json", "pkg")
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

		origClient := pypiHTTPClient
		pypiHTTPClient = server.Client()
		defer func() { pypiHTTPClient = origClient }()

		_, err := getPyPIPackageInfoFromURL(server.URL+"/pypi/pkg/json", "pkg")
		if err == nil {
			t.Fatal("expected error for invalid JSON, got nil")
		}
	})
}
