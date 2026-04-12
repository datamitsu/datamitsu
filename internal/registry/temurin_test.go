package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetLatestTemurinMajorVersion(t *testing.T) {
	t.Run("successful fetch returns most_recent_feature_release", func(t *testing.T) {
		releases := temurinReleaseVersions{
			MostRecentFeatureRelease: 25,
			AvailableReleases:        []int{25, 24, 21, 17},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(releases)
		}))
		defer server.Close()

		origClient := temurinHTTPClient
		temurinHTTPClient = server.Client()
		defer func() { temurinHTTPClient = origClient }()

		version, err := getLatestTemurinMajorVersionFromURL(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if version != "25" {
			t.Errorf("expected '25', got '%s'", version)
		}
	})

	t.Run("falls back to available_releases when most_recent is zero", func(t *testing.T) {
		releases := temurinReleaseVersions{
			MostRecentFeatureRelease: 0,
			AvailableReleases:        []int{24, 21, 17},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(releases)
		}))
		defer server.Close()

		origClient := temurinHTTPClient
		temurinHTTPClient = server.Client()
		defer func() { temurinHTTPClient = origClient }()

		version, err := getLatestTemurinMajorVersionFromURL(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if version != "24" {
			t.Errorf("expected '24', got '%s'", version)
		}
	})

	t.Run("empty releases returns fallback", func(t *testing.T) {
		releases := temurinReleaseVersions{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(releases)
		}))
		defer server.Close()

		origClient := temurinHTTPClient
		temurinHTTPClient = server.Client()
		defer func() { temurinHTTPClient = origClient }()

		version, err := getLatestTemurinMajorVersionFromURL(server.URL)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != temurinFallbackMajorVersion {
			t.Errorf("expected fallback '%s', got '%s'", temurinFallbackMajorVersion, version)
		}
	})

	t.Run("server error returns fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		}))
		defer server.Close()

		origClient := temurinHTTPClient
		temurinHTTPClient = server.Client()
		defer func() { temurinHTTPClient = origClient }()

		version, err := getLatestTemurinMajorVersionFromURL(server.URL)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != temurinFallbackMajorVersion {
			t.Errorf("expected fallback '%s', got '%s'", temurinFallbackMajorVersion, version)
		}
	})

	t.Run("invalid JSON returns fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not json"))
		}))
		defer server.Close()

		origClient := temurinHTTPClient
		temurinHTTPClient = server.Client()
		defer func() { temurinHTTPClient = origClient }()

		version, err := getLatestTemurinMajorVersionFromURL(server.URL)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != temurinFallbackMajorVersion {
			t.Errorf("expected fallback '%s', got '%s'", temurinFallbackMajorVersion, version)
		}
	})

	t.Run("connection error returns fallback", func(t *testing.T) {
		version, err := getLatestTemurinMajorVersionFromURL("http://127.0.0.1:1")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if version != temurinFallbackMajorVersion {
			t.Errorf("expected fallback '%s', got '%s'", temurinFallbackMajorVersion, version)
		}
	})
}

func TestExtractMajorVersion(t *testing.T) {
	t.Run("prefers most_recent_feature_release", func(t *testing.T) {
		releases := temurinReleaseVersions{
			MostRecentFeatureRelease: 25,
			AvailableReleases:        []int{24, 21},
		}
		got := extractMajorVersion(releases)
		if got != "25" {
			t.Errorf("expected '25', got '%s'", got)
		}
	})

	t.Run("falls back to first available_release", func(t *testing.T) {
		releases := temurinReleaseVersions{
			MostRecentFeatureRelease: 0,
			AvailableReleases:        []int{21, 17},
		}
		got := extractMajorVersion(releases)
		if got != "21" {
			t.Errorf("expected '21', got '%s'", got)
		}
	})

	t.Run("empty returns empty", func(t *testing.T) {
		got := extractMajorVersion(temurinReleaseVersions{})
		if got != "" {
			t.Errorf("expected empty, got '%s'", got)
		}
	})

	t.Run("negative most_recent ignored", func(t *testing.T) {
		releases := temurinReleaseVersions{
			MostRecentFeatureRelease: -1,
			AvailableReleases:        []int{25},
		}
		got := extractMajorVersion(releases)
		if got != "25" {
			t.Errorf("expected '25', got '%s'", got)
		}
	})
}
