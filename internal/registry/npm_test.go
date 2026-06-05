package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func rfc3339(t time.Time) string { return t.Format(time.RFC3339) }

// minAgeServer mounts /{pkg}/latest and /{pkg} for a package, and records
// whether the full-metadata endpoint was hit. When the full response has no
// dist-tags, "latest" is defaulted to latestVersion so the served metadata
// matches what npm returns in practice (a "latest" dist-tag always exists).
func minAgeServer(t *testing.T, pkg, latestVersion string, full npmFullResponse) (*httptest.Server, *bool) {
	t.Helper()
	if full.DistTags == nil {
		full.DistTags = map[string]string{"latest": latestVersion}
	}
	fullHit := false
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Match on the (decoded) path suffix to support scoped package encoding.
		if strings.HasSuffix(r.URL.Path, "/latest") {
			_ = json.NewEncoder(w).Encode(npmLatestResponse{
				Name:        pkg,
				Version:     latestVersion,
				Description: full.Description,
			})
			return
		}
		fullHit = true
		_ = json.NewEncoder(w).Encode(full)
	}
	return httptest.NewServer(http.HandlerFunc(handler)), &fullHit
}

func TestGetNPMPackageInfoWithMinAge(t *testing.T) {
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour) // 30 days ago
	fresh := now.Add(-1 * time.Hour)     // 1 hour ago
	minAge := 7 * 24 * 60                // 7 days in minutes

	setup := func(t *testing.T, srv *httptest.Server) {
		t.Helper()
		origClient := npmHTTPClient
		origBase := npmRegistryBaseURL
		npmHTTPClient = srv.Client()
		npmRegistryBaseURL = srv.URL
		t.Cleanup(func() {
			npmHTTPClient = origClient
			npmRegistryBaseURL = origBase
			srv.Close()
		})
	}

	t.Run("minAge 0 returns latest without full fetch", func(t *testing.T) {
		srv, fullHit := minAgeServer(t, "pkg", "2.0.0", npmFullResponse{Name: "pkg"})
		setup(t, srv)

		info, err := GetNPMPackageInfoWithMinAge("pkg", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "2.0.0" {
			t.Errorf("expected 2.0.0, got %s", info.Version)
		}
		if *fullHit {
			t.Error("full metadata should not be fetched for minAge 0")
		}
	})

	t.Run("latest old enough - fast path", func(t *testing.T) {
		srv, _ := minAgeServer(t, "pkg", "2.0.0", npmFullResponse{
			Name:     "pkg",
			Versions: map[string]npmVersionMeta{"2.0.0": {}, "1.0.0": {}},
			Time:     map[string]string{"2.0.0": rfc3339(old), "1.0.0": rfc3339(old)},
		})
		setup(t, srv)

		info, err := GetNPMPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "2.0.0" {
			t.Errorf("expected 2.0.0, got %s", info.Version)
		}
	})

	t.Run("latest too fresh - falls back to older", func(t *testing.T) {
		srv, _ := minAgeServer(t, "pkg", "2.0.0", npmFullResponse{
			Name:     "pkg",
			Versions: map[string]npmVersionMeta{"2.0.0": {}, "1.0.0": {}},
			Time:     map[string]string{"2.0.0": rfc3339(fresh), "1.0.0": rfc3339(old)},
		})
		setup(t, srv)

		info, err := GetNPMPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "1.0.0" {
			t.Errorf("expected 1.0.0, got %s", info.Version)
		}
	})

	t.Run("skips pre-release versions", func(t *testing.T) {
		srv, _ := minAgeServer(t, "pkg", "2.0.0-beta.1", npmFullResponse{
			Name: "pkg",
			Versions: map[string]npmVersionMeta{
				"2.0.0-beta.1": {}, "2.0.0-rc.0": {}, "1.0.0": {},
			},
			Time: map[string]string{
				"2.0.0-beta.1": rfc3339(old), "2.0.0-rc.0": rfc3339(old), "1.0.0": rfc3339(old),
			},
		})
		setup(t, srv)

		info, err := GetNPMPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "1.0.0" {
			t.Errorf("expected 1.0.0 (pre-releases skipped), got %s", info.Version)
		}
	})

	t.Run("build metadata is not pre-release", func(t *testing.T) {
		srv, _ := minAgeServer(t, "pkg", "1.2.3+build.1", npmFullResponse{
			Name:     "pkg",
			Versions: map[string]npmVersionMeta{"1.2.3+build.1": {}, "1.0.0": {}},
			Time:     map[string]string{"1.2.3+build.1": rfc3339(old), "1.0.0": rfc3339(old.Add(-time.Hour))},
		})
		setup(t, srv)

		info, err := GetNPMPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "1.2.3+build.1" {
			t.Errorf("expected 1.2.3+build.1 (build metadata kept), got %s", info.Version)
		}
	})

	t.Run("missing time entry is skipped", func(t *testing.T) {
		srv, _ := minAgeServer(t, "pkg", "2.0.0", npmFullResponse{
			Name:     "pkg",
			Versions: map[string]npmVersionMeta{"2.0.0": {}, "1.5.0": {}, "1.0.0": {}},
			Time:     map[string]string{"2.0.0": rfc3339(fresh), "1.0.0": rfc3339(old)},
		})
		setup(t, srv)

		info, err := GetNPMPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "1.0.0" {
			t.Errorf("expected 1.0.0 (1.5.0 missing time skipped), got %s", info.Version)
		}
	})

	t.Run("latest dist-tag points to pre-release", func(t *testing.T) {
		srv, _ := minAgeServer(t, "pkg", "3.0.0-rc.1", npmFullResponse{
			Name:     "pkg",
			DistTags: map[string]string{"latest": "3.0.0-rc.1"},
			Versions: map[string]npmVersionMeta{"3.0.0-rc.1": {}, "2.0.0": {}},
			Time:     map[string]string{"3.0.0-rc.1": rfc3339(old), "2.0.0": rfc3339(old)},
		})
		setup(t, srv)

		info, err := GetNPMPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "2.0.0" {
			t.Errorf("expected 2.0.0 (pre-release latest skipped), got %s", info.Version)
		}
	})

	t.Run("no version old enough returns nil", func(t *testing.T) {
		srv, _ := minAgeServer(t, "pkg", "2.0.0", npmFullResponse{
			Name:     "pkg",
			Versions: map[string]npmVersionMeta{"2.0.0": {}, "1.0.0": {}},
			Time:     map[string]string{"2.0.0": rfc3339(fresh), "1.0.0": rfc3339(fresh)},
		})
		setup(t, srv)

		info, err := GetNPMPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info != nil {
			t.Errorf("expected nil, got %+v", info)
		}
	})

	t.Run("scoped package", func(t *testing.T) {
		srv, _ := minAgeServer(t, "@scope/name", "2.0.0", npmFullResponse{
			Name:     "@scope/name",
			Versions: map[string]npmVersionMeta{"2.0.0": {}, "1.0.0": {}},
			Time:     map[string]string{"2.0.0": rfc3339(fresh), "1.0.0": rfc3339(old)},
		})
		setup(t, srv)

		info, err := GetNPMPackageInfoWithMinAge("@scope/name", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "1.0.0" {
			t.Errorf("expected 1.0.0, got %s", info.Version)
		}
	})
}
