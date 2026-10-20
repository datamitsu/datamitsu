package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/appstate"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/releaseasset"
	"github.com/datamitsu/datamitsu/internal/releaseprovider"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/target"
)

func TestGitHubVisibilityDownloadPaths(t *testing.T) {
	for _, tc := range []struct {
		name           string
		private, token bool
	}{
		{"public-token", false, true}, {"public-anonymous", false, false}, {"private", true, true},
	} {
		for _, verify := range []bool{false, true} {
			name := tc.name
			if verify {
				name += "-verify"
			}
			t.Run(name, func(t *testing.T) {
				if err := runtimeconfig.Init(); err != nil {
					t.Fatal(err)
				}
				oldUpdate, oldVerify := updateFlag, verifyExtractionFlag
				updateFlag, verifyExtractionFlag = false, verify
				t.Cleanup(func() { updateFlag, verifyExtractionFlag = oldUpdate, oldVerify })
				t.Setenv("GITHUB_TOKEN", "fixture-github-credential")
				t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
				data := []byte("#!/bin/sh\necho fixture\n")
				sum := digest.SHA256Of(data)
				expected := "sha256:" + sum.Hex()
				var browserRequests, assetRequests, repoRequests, discoveryRequests atomic.Int32
				var corrupt atomic.Bool
				writeBinary := func(w http.ResponseWriter) {
					if corrupt.Load() {
						_, _ = w.Write([]byte("#!/bin/sh\necho tampered\n"))
					} else {
						_, _ = w.Write(data)
					}
				}
				browser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					browserRequests.Add(1)
					if r.Header.Get("Authorization") != "" {
						t.Error("public binary received credentials")
					}
					if tc.private {
						t.Error("private binary used browser URL")
						w.WriteHeader(http.StatusForbidden)
						return
					}
					writeBinary(w)
				}))
				defer browser.Close()
				cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "" {
						t.Error("private redirect leaked Authorization")
					}
					writeBinary(w)
				}))
				defer cdn.Close()
				var apiURL string
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/releases/assets/1") {
						assetRequests.Add(1)
						if !tc.private {
							t.Error("public install spent API asset quota")
							w.WriteHeader(http.StatusForbidden)
							return
						}
						if r.Header.Get("Authorization") != "Bearer fixture-github-credential" || r.Header.Get("Accept") != "application/octet-stream" {
							t.Error("private asset lacked download auth")
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						http.Redirect(w, r, cdn.URL+"/binary", http.StatusFound)
						return
					}
					discoveryRequests.Add(1)
					auth := ""
					if tc.token {
						auth = "Bearer fixture-github-credential"
					}
					if r.Header.Get("Authorization") != auth {
						t.Errorf("discovery Authorization=%q", r.Header.Get("Authorization"))
					}
					if strings.HasSuffix(r.URL.Path, "/repos/o/tool") {
						repoRequests.Add(1)
						_ = json.NewEncoder(w).Encode(map[string]any{"description": "fixture", "private": tc.private, "id": 1})
						return
					}
					_ = json.NewEncoder(w).Encode(releaseasset.Release{TagName: "v1", PublishedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Assets: []releaseasset.Asset{{Name: "tool-linux-amd64", APIURL: apiURL + "/repos/o/tool/releases/assets/1", BrowserDownloadURL: browser.URL + "/tool-linux-amd64", Digest: expected}}})
				}))
				defer api.Close()
				apiURL = api.URL
				source := releaseprovider.Source{Type: "github", URL: api.URL, APIURL: api.URL}
				if tc.token {
					source.TokenEnv = "GITHUB_TOKEN"
				}
				state := &appstate.State{Sources: map[string]releaseprovider.Source{"upstream": source}, Apps: map[string]*appstate.AppMetadata{"tool": {Source: "upstream", Repository: "o/tool", Tag: "v1"}}, Platforms: []string{"linux/amd64/glibc"}, Binaries: map[string]*appstate.BinariesEntry{}}
				path := filepath.Join(t.TempDir(), "binaryApps.json")
				if err := appstate.Save(path, state); err != nil {
					t.Fatal(err)
				}
				if err := runPullReleases(pullReleasesCmd, []string{path}); err != nil {
					t.Fatal(err)
				}
				saved, err := appstate.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				info := saved.Binaries["tool"].Binaries["linux"]["amd64"]["glibc"]
				text, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(text), "fixture-github-credential") {
					t.Fatal("token serialized")
				}
				if info.Hash != expected {
					t.Fatal("GitHub native digest was not preserved")
				}
				if repoRequests.Load() != 1 {
					t.Fatalf("visibility/description metadata fetched %d times", repoRequests.Load())
				}
				verifiedDownloads := int32(0)
				if verify {
					verifiedDownloads = 1
				}
				if tc.private {
					if info.URL != api.URL+"/repos/o/tool/releases/assets/1" || info.Auth == nil || info.Auth.TokenEnv != "GITHUB_TOKEN" {
						t.Fatalf("private download info=%+v", info)
					}
					if assetRequests.Load() != verifiedDownloads || browserRequests.Load() != 0 {
						t.Fatal("private verification used incorrect endpoint")
					}
				} else {
					if info.URL != browser.URL+"/tool-linux-amd64" || info.Auth != nil {
						t.Fatalf("public download info=%+v", info)
					}
					if assetRequests.Load() != 0 || browserRequests.Load() != verifiedDownloads {
						t.Fatal("public verification used incorrect endpoint")
					}
				}
				manager := func() *binmanager.BinManager {
					return binmanager.NewWithResolver(binmanager.MapOfApps{"tool": {Binary: &binmanager.AppConfigBinary{Binaries: saved.Binaries["tool"].Binaries}}}, nil, nil, target.NewResolver(target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcGlibc}))
				}
				t.Setenv("GITHUB_TOKEN", "")
				beforeAPI := assetRequests.Load()
				bm := manager()
				if tc.private {
					if _, err := bm.GetCommandInfo(context.Background(), "tool"); err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") || !strings.Contains(err.Error(), "not set") {
						t.Fatalf("missing credential error=%v", err)
					}
					if assetRequests.Load() != beforeAPI {
						t.Fatal("missing credential still contacted asset API")
					}
					t.Setenv("GITHUB_TOKEN", "fixture-github-credential")
				}
				if command, err := bm.GetCommandInfo(context.Background(), "tool"); err != nil || command == nil {
					t.Fatalf("install command=%v err=%v", command, err)
				}
				if tc.private {
					if assetRequests.Load() != verifiedDownloads+1 || browserRequests.Load() != 0 {
						t.Fatal("private install path wrong")
					}
				} else {
					if assetRequests.Load() != 0 || browserRequests.Load() != verifiedDownloads+1 {
						t.Fatal("public installation used API asset endpoint")
					}
					if discoveryRequests.Load() != 2 {
						t.Fatal("public install contacted metadata API")
					}
				}
				corrupt.Store(true)
				t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
				if _, err := manager().GetCommandInfo(context.Background(), "tool"); err == nil || !strings.Contains(err.Error(), "hash verification failed") {
					t.Fatalf("tampered download accepted: %v", err)
				}
			})
		}
	}
}

func TestGitHubPreviouslyGeneratedAPIEntryIsRefreshed(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatal(err)
	}
	oldUpdate, oldVerify := updateFlag, verifyExtractionFlag
	updateFlag, verifyExtractionFlag = false, false
	t.Cleanup(func() { updateFlag, verifyExtractionFlag = oldUpdate, oldVerify })
	t.Setenv("GITHUB_TOKEN", "fixture-github-credential")
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/repos/o/tool") {
			_, _ = w.Write([]byte(`{"private":false,"description":"fixture"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(releaseasset.Release{TagName: "v1", Assets: []releaseasset.Asset{{Name: "tool-linux-amd64", APIURL: base + "/repos/o/tool/releases/assets/1", BrowserDownloadURL: base + "/releases/download/v1/tool-linux-amd64", Digest: "sha256:" + strings.Repeat("a", 64)}}})
	}))
	defer srv.Close()
	base = srv.URL
	source := releaseprovider.Source{Type: "github", URL: base, APIURL: base, TokenEnv: "GITHUB_TOKEN"}
	metadata := &appstate.AppMetadata{Source: "upstream", Repository: "o/tool", Tag: "v1"}
	selection := []string{"linux/amd64/glibc"}
	// This is the published PR #367 fingerprint representation, before visibility became a generation input.
	legacy := struct {
		App       *appstate.AppMetadata  `json:"app"`
		Source    releaseprovider.Source `json:"source"`
		Platforms []string               `json:"platforms"`
	}{metadata, source, selection}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	oldHash := digest.XXH3Of(data).Hex()
	if oldHash == appstate.ComputeConfigHash(metadata, selection, source) {
		t.Fatal("old generation fingerprint still matches")
	}
	state := &appstate.State{Sources: map[string]releaseprovider.Source{"upstream": source}, Apps: map[string]*appstate.AppMetadata{"tool": metadata}, Platforms: selection, Binaries: map[string]*appstate.BinariesEntry{"tool": {ConfigHash: oldHash, Binaries: binmanager.MapOfBinaries{"linux": {"amd64": {"glibc": {URL: base + "/repos/o/tool/releases/assets/1", Auth: &httpx.RequestAuth{TokenEnv: "GITHUB_TOKEN", Origin: base, Header: "Authorization", Scheme: "Bearer", Accept: "application/octet-stream"}, Hash: strings.Repeat("a", 64), ContentType: binmanager.BinContentTypeBinary}}}}}}}
	path := filepath.Join(t.TempDir(), "binaryApps.json")
	if err := appstate.Save(path, state); err != nil {
		t.Fatal(err)
	}
	if err := runPullReleases(pullReleasesCmd, []string{path}); err != nil {
		t.Fatal(err)
	}
	saved, err := appstate.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	info := saved.Binaries["tool"].Binaries["linux"]["amd64"]["glibc"]
	if info.URL != base+"/releases/download/v1/tool-linux-amd64" || info.Auth != nil {
		t.Fatalf("regressive entry was not repaired: %+v", info)
	}
}
