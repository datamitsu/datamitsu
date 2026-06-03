package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func isoFile(t time.Time, yanked bool) pypiReleaseFile {
	return pypiReleaseFile{UploadTimeISO8601: t.Format(time.RFC3339), Yanked: yanked}
}

func TestGetPyPIPackageInfoWithMinAge(t *testing.T) {
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour) // 30 days ago
	fresh := now.Add(-1 * time.Hour)     // 1 hour ago
	minAge := 7 * 24 * 60                // 7 days in minutes

	setup := func(t *testing.T, full pypiFullResponse) {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(full)
		}))
		origClient := pypiHTTPClient
		origBase := pypiBaseURL
		pypiHTTPClient = srv.Client()
		pypiBaseURL = srv.URL
		t.Cleanup(func() {
			pypiHTTPClient = origClient
			pypiBaseURL = origBase
			srv.Close()
		})
	}

	t.Run("minAge 0 returns latest", func(t *testing.T) {
		setup(t, pypiFullResponse{
			Info: pypiInfo{Name: "pkg", Version: "2.0.0", Summary: "desc"},
			Releases: map[string][]pypiReleaseFile{
				"2.0.0": {isoFile(fresh, false)},
				"1.0.0": {isoFile(old, false)},
			},
		})

		info, err := GetPyPIPackageInfoWithMinAge("pkg", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "2.0.0" {
			t.Errorf("expected 2.0.0, got %s", info.Version)
		}
	})

	t.Run("selects older when latest too fresh", func(t *testing.T) {
		setup(t, pypiFullResponse{
			Info: pypiInfo{Name: "pkg", Version: "2.0.0"},
			Releases: map[string][]pypiReleaseFile{
				"2.0.0": {isoFile(fresh, false)},
				"1.0.0": {isoFile(old, false)},
			},
		})

		info, err := GetPyPIPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "1.0.0" {
			t.Errorf("expected 1.0.0, got %s", info.Version)
		}
	})

	t.Run("skips PEP 440 pre-release versions", func(t *testing.T) {
		setup(t, pypiFullResponse{
			Info: pypiInfo{Name: "pkg", Version: "2.0.0rc1"},
			Releases: map[string][]pypiReleaseFile{
				"2.0.0rc1":   {isoFile(old, false)},
				"2.0.0b2":    {isoFile(old, false)},
				"2.0.0a1":    {isoFile(old, false)},
				"2.0.0.dev3": {isoFile(old, false)},
				"1.0.0":      {isoFile(old, false)},
			},
		})

		info, err := GetPyPIPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "1.0.0" {
			t.Errorf("expected 1.0.0 (pre-releases skipped), got %s", info.Version)
		}
	})

	t.Run("post-release is not skipped", func(t *testing.T) {
		setup(t, pypiFullResponse{
			Info: pypiInfo{Name: "pkg", Version: "1.0.0.post1"},
			Releases: map[string][]pypiReleaseFile{
				"1.0.0.post1": {isoFile(old, false)},
				"1.0.0":       {isoFile(old.Add(-time.Hour), false)},
			},
		})

		info, err := GetPyPIPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "1.0.0.post1" {
			t.Errorf("expected 1.0.0.post1 (post-release kept), got %s", info.Version)
		}
	})

	t.Run("skips fully yanked releases", func(t *testing.T) {
		setup(t, pypiFullResponse{
			Info: pypiInfo{Name: "pkg", Version: "2.0.0"},
			Releases: map[string][]pypiReleaseFile{
				"2.0.0": {isoFile(old, true), isoFile(old, true)},
				"1.0.0": {isoFile(old, false)},
			},
		})

		info, err := GetPyPIPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "1.0.0" {
			t.Errorf("expected 1.0.0 (fully yanked 2.0.0 skipped), got %s", info.Version)
		}
	})

	t.Run("mixed yanked/non-yanked files is not skipped", func(t *testing.T) {
		setup(t, pypiFullResponse{
			Info: pypiInfo{Name: "pkg", Version: "2.0.0"},
			Releases: map[string][]pypiReleaseFile{
				"2.0.0": {isoFile(old, true), isoFile(old, false)},
				"1.0.0": {isoFile(old.Add(-time.Hour), false)},
			},
		})

		info, err := GetPyPIPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "2.0.0" {
			t.Errorf("expected 2.0.0 (has a non-yanked file), got %s", info.Version)
		}
	})

	t.Run("legacy upload_time fallback", func(t *testing.T) {
		setup(t, pypiFullResponse{
			Info: pypiInfo{Name: "pkg", Version: "2.0.0"},
			Releases: map[string][]pypiReleaseFile{
				"2.0.0": {{UploadTime: fresh.UTC().Format("2006-01-02T15:04:05")}},
				"1.0.0": {{UploadTime: old.UTC().Format("2006-01-02T15:04:05")}},
			},
		})

		info, err := GetPyPIPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Version != "1.0.0" {
			t.Errorf("expected 1.0.0 via upload_time fallback, got %s", info.Version)
		}
	})

	t.Run("version with no parsable upload time is skipped", func(t *testing.T) {
		setup(t, pypiFullResponse{
			Info: pypiInfo{Name: "pkg", Version: "2.0.0"},
			Releases: map[string][]pypiReleaseFile{
				// 2.0.0 has neither upload_time_iso_8601 nor a parsable
				// upload_time, so pypiVersionReleaseTime returns ok=false.
				"2.0.0": {{}},
				"1.0.0": {isoFile(old, false)},
			},
		})

		info, err := GetPyPIPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info == nil || info.Version != "1.0.0" {
			t.Errorf("expected 1.0.0 (2.0.0 has no parsable time), got %+v", info)
		}
	})

	t.Run("no version old enough returns nil", func(t *testing.T) {
		setup(t, pypiFullResponse{
			Info: pypiInfo{Name: "pkg", Version: "2.0.0"},
			Releases: map[string][]pypiReleaseFile{
				"2.0.0": {isoFile(fresh, false)},
				"1.0.0": {isoFile(fresh, false)},
			},
		})

		info, err := GetPyPIPackageInfoWithMinAge("pkg", minAge)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info != nil {
			t.Errorf("expected nil, got %+v", info)
		}
	})
}
