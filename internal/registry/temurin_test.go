package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveTemurin(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	origClient := temurinHTTPClient
	temurinHTTPClient = server.Client()
	t.Cleanup(func() { temurinHTTPClient = origClient })
	return server.URL
}

func TestGetTemurinMajorVersions(t *testing.T) {
	t.Run("lists feature releases newest first from the most recent one", func(t *testing.T) {
		// The real API lists available_releases oldest first.
		url := serveTemurin(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(temurinReleaseVersions{
				MostRecentFeatureRelease: 27,
				AvailableReleases:        []int{8, 11, 17, 21, 25, 26, 27},
			})
		})

		versions, err := getTemurinMajorVersionsFromURL(context.Background(), url)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := strings.Join(versions, ","); got != "27,26,25,21,17,11,8" {
			t.Errorf("versions = %s, want 27,26,25,21,17,11,8", got)
		}
	})

	t.Run("empty releases is an error", func(t *testing.T) {
		url := serveTemurin(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(temurinReleaseVersions{})
		})

		versions, err := getTemurinMajorVersionsFromURL(context.Background(), url)
		if err == nil {
			t.Fatalf("expected error, got versions %v", versions)
		}
	})

	t.Run("server error is an error", func(t *testing.T) {
		url := serveTemurin(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		})

		versions, err := getTemurinMajorVersionsFromURL(context.Background(), url)
		if err == nil {
			t.Fatalf("expected error, got versions %v", versions)
		}
	})

	t.Run("invalid JSON is an error", func(t *testing.T) {
		url := serveTemurin(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json"))
		})

		versions, err := getTemurinMajorVersionsFromURL(context.Background(), url)
		if err == nil {
			t.Fatalf("expected error, got versions %v", versions)
		}
	})

	t.Run("connection error is an error", func(t *testing.T) {
		versions, err := getTemurinMajorVersionsFromURL(context.Background(), "http://127.0.0.1:1")
		if err == nil {
			t.Fatalf("expected error, got versions %v", versions)
		}
	})
}

func TestExtractMajorVersions(t *testing.T) {
	tests := []struct {
		name     string
		releases temurinReleaseVersions
		want     string
	}{
		{
			name:     "leaves out versions above the most recent feature release",
			releases: temurinReleaseVersions{MostRecentFeatureRelease: 25, AvailableReleases: []int{21, 25, 26}},
			want:     "25,21",
		},
		{
			name:     "adds a most recent release the list lacks",
			releases: temurinReleaseVersions{MostRecentFeatureRelease: 25, AvailableReleases: []int{21, 17}},
			want:     "25,21,17",
		},
		{
			name:     "orders the list without a most recent release",
			releases: temurinReleaseVersions{AvailableReleases: []int{17, 24, 21}},
			want:     "24,21,17",
		},
		{
			name:     "negative most recent is ignored",
			releases: temurinReleaseVersions{MostRecentFeatureRelease: -1, AvailableReleases: []int{25}},
			want:     "25",
		},
		{
			name:     "duplicates and non-positive values are dropped",
			releases: temurinReleaseVersions{MostRecentFeatureRelease: 25, AvailableReleases: []int{25, 0, 21, 21}},
			want:     "25,21",
		},
		{
			name: "empty is empty",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.Join(extractMajorVersions(tt.releases), ","); got != tt.want {
				t.Errorf("extractMajorVersions() = %q, want %q", got, tt.want)
			}
		})
	}
}
