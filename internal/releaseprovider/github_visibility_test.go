package releaseprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/releaseasset"
)

func TestGitHubVisibilityForPinnedLatestAndCachedMetadata(t *testing.T) {
	for _, private := range []bool{false, true} {
		name := "public"
		if private {
			name = "private"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("TEST_GITHUB_TOKEN", "fixture-token")
			var repoCalls atomic.Int32
			var base string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("metadata API lacks Authorization")
				}
				if strings.HasSuffix(r.URL.Path, "/repos/o/tool") {
					repoCalls.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"private": private, "description": "fixture"})
					return
				}
				release := releaseasset.Release{TagName: "v1", PublishedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Assets: []releaseasset.Asset{{Name: "tool-linux-amd64", APIURL: base + "/repos/o/tool/releases/assets/1", BrowserDownloadURL: "https://github.com/o/tool/releases/download/v1/tool-linux-amd64", Digest: "sha256:" + strings.Repeat("a", 64)}}}
				if r.URL.Query().Get("page") != "" {
					rows := []releaseasset.Release{}
					if r.URL.Query().Get("page") == "1" {
						rows = append(rows, release)
					}
					_ = json.NewEncoder(w).Encode(rows)
					return
				}
				_ = json.NewEncoder(w).Encode(release)
			}))
			defer srv.Close()
			base = srv.URL
			c, err := New(Source{Type: "github", URL: base, APIURL: base, TokenEnv: "TEST_GITHUB_TOKEN"})
			if err != nil {
				t.Fatal(err)
			}
			pinned, err := c.GetRelease(context.Background(), "o/tool", "v1")
			if err != nil {
				t.Fatal(err)
			}
			latest, err := c.GetLatestReleaseWithMinAge(context.Background(), "o/tool", 60)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.GetRepository(context.Background(), "o/tool"); err != nil {
				t.Fatal(err)
			}
			if repoCalls.Load() != 1 {
				t.Fatal("repository visibility was not cached")
			}
			for _, release := range []*releaseasset.Release{pinned, latest} {
				asset := release.Assets[0]
				if private {
					if asset.Auth == nil || asset.BrowserDownloadURL != asset.APIURL {
						t.Fatal("private policy missing")
					}
				} else {
					if asset.Auth != nil || asset.BrowserDownloadURL != "https://github.com/o/tool/releases/download/v1/tool-linux-amd64" {
						t.Fatal("public download changed")
					}
				}
			}
		})
	}
}

func TestGitHubUnknownVisibilityFailsWithoutCaching(t *testing.T) {
	t.Setenv("TEST_GITHUB_TOKEN", "fixture-token")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/repos/o/tool") {
			if calls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"description":"unknown"}`))
			} else {
				_, _ = w.Write([]byte(`{"private":false}`))
			}
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1","assets":[]}`))
	}))
	defer srv.Close()
	c, _ := New(Source{Type: "github", URL: srv.URL, APIURL: srv.URL, TokenEnv: "TEST_GITHUB_TOKEN"})
	if _, err := c.GetRelease(context.Background(), "o/tool", "v1"); err == nil || !strings.Contains(err.Error(), "visibility") {
		t.Fatalf("unknown visibility accepted: %v", err)
	}
	if _, err := c.GetRelease(context.Background(), "o/tool", "v1"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("failed metadata was cached")
	}
}
