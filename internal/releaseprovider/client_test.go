package releaseprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpretry"
	"github.com/datamitsu/datamitsu/internal/releaseasset"
)

func digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func TestForgeAdapters(t *testing.T) {
	for _, kind := range []string{"github", "gitea", "forgejo", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			repository := "group/tool"
			if kind == "gitlab" {
				repository = "group/subgroup/tool"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "gitlab" {
					if strings.Contains(r.URL.Path, "/releases/") {
						_, _ = w.Write([]byte(`{"tag_name":"v1.0.0","released_at":"2020-01-01T00:00:00Z","assets":{"links":[{"name":"tool-linux-amd64.tar.gz","url":"https://files.example.test/tool-linux-amd64.tar.gz"}],"sources":[{"url":"https://example.test/source.tar.gz"}]}}`))
						return
					}
				} else if strings.Contains(r.URL.Path, "/releases/") {
					_ = json.NewEncoder(w).Encode(releaseasset.Release{TagName: "v1.0.0", PublishedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Assets: []releaseasset.Asset{{Name: "tool-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.test/tool.tar.gz", Digest: "sha256:" + strings.Repeat("a", 64)}}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"description": "tool", "id": 42, "private": false, "visibility": "public"})
			}))
			defer srv.Close()
			c, err := New(Source{Type: kind, URL: srv.URL, APIURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			r, err := c.GetRelease(context.Background(), repository, "v1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			if r.TagName != "v1.0.0" || len(r.Assets) != 1 {
				t.Fatalf("release=%+v", r)
			}
			repo, err := c.GetRepository(context.Background(), repository)
			if err != nil || repo.Description != "tool" {
				t.Fatalf("repo=%+v err=%v", repo, err)
			}
		})
	}
}

func TestLatestAcrossPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/repos/group/tool") {
			_, _ = w.Write([]byte(`{"private":false}`))
			return
		}
		if page := r.URL.Query().Get("page"); page != "1" && page != "2" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		rows := make([]releaseasset.Release, 100)
		for i := range rows {
			rows[i] = releaseasset.Release{TagName: "v1.0.0", PublishedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
		}
		if r.URL.Query().Get("page") == "2" {
			rows = []releaseasset.Release{{TagName: "v2.0.0", PublishedAt: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)}}
		}
		_ = json.NewEncoder(w).Encode(rows)
	}))
	defer srv.Close()
	c, err := New(Source{Type: "forgejo", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.GetLatestReleaseWithMinAge(context.Background(), "group/tool", 60)
	if err != nil || r == nil || r.TagName != "v2.0.0" {
		t.Fatalf("latest=%+v err=%v", r, err)
	}
}

func TestPinnedChecksumAndMissingHash(t *testing.T) {
	payload := []byte(strings.Repeat("a", 64) + "  tool-linux-amd64\n")
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; _, _ = w.Write(payload) }))
	defer srv.Close()
	c, _ := New(Source{Type: "gitea", URL: srv.URL})
	assets := []releaseasset.Asset{{Name: "tool-linux-amd64", BrowserDownloadURL: srv.URL + "/binary"}, {Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/checksums"}}
	asset := assets[0]
	if err := c.ResolveDigest(context.Background(), "group/tool", assets, &asset, nil, nil); err == nil {
		t.Fatal("missing digest accepted")
	}
	if requests != 0 {
		t.Fatal("hash-less candidate was downloaded")
	}
	pins := map[string]string{"checksums.txt": digest(payload)}
	if err := c.ResolveDigest(context.Background(), "group/tool", assets, &asset, nil, pins); err != nil {
		t.Fatal(err)
	}
	if asset.Digest != "sha256:"+strings.Repeat("a", 64) || requests != 1 {
		t.Fatalf("digest=%s requests=%d", asset.Digest, requests)
	}
	asset = assets[0]
	if err := c.ResolveDigest(context.Background(), "group/tool", assets, &asset, nil, pins); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatal("checksum was not memoized")
	}
	asset = assets[0]
	if err := c.ResolveDigest(context.Background(), "group/tool", assets, &asset, nil, map[string]string{"checksums.txt": strings.Repeat("b", 64)}); err == nil {
		t.Fatal("bad checksum pin accepted")
	}
}

func TestGitLabPackageMetadataDigest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "" && r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		if strings.Contains(r.URL.Path, "package_files") {
			_, _ = w.Write([]byte(`[{"file_name":"tool_linux_amd64.tar.gz","file_sha256":"` + strings.Repeat("a", 64) + `"}]`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/packages") {
			_, _ = w.Write([]byte(`[{"id":7,"name":"tool","version":"1.0.0"}]`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 42})
	}))
	defer srv.Close()
	c, _ := New(Source{Type: "gitlab", URL: srv.URL})
	asset := releaseasset.Asset{Name: "tool_linux_amd64.tar.gz", BrowserDownloadURL: srv.URL + "/api/v4/projects/42/packages/generic/tool/1%2E0%2E0/tool_linux_amd64.tar.gz"}
	if err := c.ResolveDigest(context.Background(), "group/tool", nil, &asset, nil, nil); err != nil {
		t.Fatal(err)
	}
	if asset.Digest != "sha256:"+strings.Repeat("a", 64) {
		t.Fatal("missing GitLab digest")
	}
	foreign := releaseasset.Asset{Name: asset.Name, BrowserDownloadURL: strings.Replace(asset.BrowserDownloadURL, "/42/", "/43/", 1)}
	if err := c.ResolveDigest(context.Background(), "group/tool", nil, &foreign, nil, nil); err == nil {
		t.Fatal("foreign project accepted")
	}
}

func TestCredentialNotForwardedOnRedirect(t *testing.T) {
	t.Setenv("TEST_FORGE_TOKEN", "credential")
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Private-Token") != "" || r.Header.Get("Authorization") != "" {
			t.Error("credential leaked across origins")
		}
		_, _ = w.Write([]byte(`{"description":"tool"}`))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Private-Token") != "credential" {
			t.Error("missing source credential")
		}
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	c, err := New(Source{Type: "gitlab", URL: source.URL, TokenEnv: "TEST_FORGE_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetRepository(context.Background(), "group/tool"); err != nil {
		t.Fatal(err)
	}
}

func TestValidationAndChecksumAmbiguity(t *testing.T) {
	for _, s := range []Source{{Type: "unknown", URL: "https://example.test"}, {Type: "gitea", URL: "http://example.test"}, {Type: "github", URL: "https://u:p@example.test"}, {Type: "forgejo", URL: "https://example.test", TokenEnv: "DATAMITSU_TOKEN"}} {
		if _, err := New(s); err == nil {
			t.Fatalf("accepted source %+v", s)
		}
	}
	for _, repository := range []string{"group/../tool", "group/tool?x", "group//tool", "group/%2f"} {
		if err := ValidateRepository("gitlab", repository); err == nil {
			t.Fatal("unsafe repository accepted")
		}
	}
	if _, err := parseChecksums([]byte(strings.Repeat("a", 64) + "  tool\n" + strings.Repeat("b", 64) + "  tool\n")); err == nil {
		t.Fatal("conflicting checksum accepted")
	}
	table, err := parseChecksums([]byte("SHA256 (tool) = " + strings.Repeat("a", 64)))
	if err != nil || table["tool"] != strings.Repeat("a", 64) {
		t.Fatalf("BSD checksum=%v err=%v", table, err)
	}
}

func TestTransientMetadataRetry(t *testing.T) {
	prevBase, prevMax := httpretry.RetryBase, httpretry.RetryMax
	httpretry.RetryBase, httpretry.RetryMax = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { httpretry.RetryBase, httpretry.RetryMax = prevBase, prevMax })
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1"}`))
	}))
	defer srv.Close()
	c, _ := New(Source{Type: "gitea", URL: srv.URL})
	if _, err := c.GetRelease(context.Background(), "group/tool", "v1"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("transient failure was not retried")
	}
}

func TestLatestRejectsUnflaggedPrereleaseAndYoungRelease(t *testing.T) {
	for _, tc := range []struct {
		tag, date string
		age       int
	}{{"v1.0.0-rc1", "2020-01-01T00:00:00Z", 0}, {"v1.0.0", time.Now().Add(time.Hour).Format(time.RFC3339), 60}} {
		t.Run(tc.tag+tc.date, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") != "1" {
					_, _ = w.Write([]byte("[]"))
					return
				}
				_ = json.NewEncoder(w).Encode([]map[string]any{{"tag_name": tc.tag, "published_at": tc.date}})
			}))
			defer srv.Close()
			c, _ := New(Source{Type: "forgejo", URL: srv.URL})
			r, err := c.GetLatestReleaseWithMinAge(context.Background(), "group/tool", tc.age)
			if err != nil || r != nil {
				t.Fatalf("ineligible release=%+v err=%v", r, err)
			}
		})
	}
}

func TestPublicGitHubDefaultOriginAndTagMismatch(t *testing.T) {
	for _, raw := range []string{"https://github.com", "https://GITHUB.com:443/"} {
		if got := (Source{Type: "github", URL: raw}).APIBase(); got != "https://api.github.com" {
			t.Fatalf("API root=%s", got)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"tag_name":"v2"}`)) }))
	defer srv.Close()
	c, _ := New(Source{Type: "gitea", URL: srv.URL})
	if _, err := c.GetRelease(context.Background(), "group/tool", "v1"); err == nil {
		t.Fatal("mismatched pinned tag accepted")
	}
}

func TestLatestRejectsUnusableTagMetadata(t *testing.T) {
	for _, tag := range []string{"", "v1.0.0\x1b[31m", "v1.0.0\n"} {
		t.Run(tag, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") != "1" {
					_, _ = w.Write([]byte("[]"))
					return
				}
				_ = json.NewEncoder(w).Encode([]map[string]string{{"tag_name": tag, "published_at": "2020-01-01T00:00:00Z"}})
			}))
			defer srv.Close()
			c, _ := New(Source{Type: "gitea", URL: srv.URL})
			if r, err := c.GetLatestReleaseWithMinAge(context.Background(), "group/tool", 0); err == nil || r != nil {
				t.Fatalf("unusable tag accepted: release=%v err=%v", r, err)
			}
		})
	}
}
