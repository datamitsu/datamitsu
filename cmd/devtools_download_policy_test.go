package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/digest"

	"github.com/datamitsu/datamitsu/internal/appstate"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/releaseasset"
	"github.com/datamitsu/datamitsu/internal/releaseprovider"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/target"
)

func TestForgeDownloadPolicyInstallAndVerify(t *testing.T) {
	for _, kind := range []string{"gitlab", "gitea", "forgejo"} {
		for _, private := range []bool{false, true} {
			for _, verify := range []bool{false, true} {
				name := kind + "/public"
				if private {
					name = kind + "/private"
				}
				if verify {
					name += "/verify"
				}
				t.Run(name, func(t *testing.T) {
					if err := runtimeconfig.Init(); err != nil {
						t.Fatal(err)
					}
					oldUpdate, oldVerify := updateFlag, verifyExtractionFlag
					updateFlag, verifyExtractionFlag = false, verify
					t.Cleanup(func() { updateFlag, verifyExtractionFlag = oldUpdate, oldVerify })
					t.Setenv("TEST_DOWNLOAD_TOKEN", "fixture")
					t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
					data := []byte("#!/bin/sh\necho tool\n")
					sum := digest.SHA256Of(data)
					hash := "sha256:" + sum.Hex()
					var downloads atomic.Int32
					var base string
					downloadPath := "/group/tool/releases/download/v1/tool-linux-amd64"
					if kind == "gitlab" {
						downloadPath = "/api/v4/projects/42/packages/generic/tool/v1/tool-linux-amd64"
					}
					cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Header.Get("Authorization") != "" || r.Header.Get("Private-Token") != "" {
							t.Error("redirect leaked credential")
						}
						_, _ = w.Write(data)
					}))
					defer cdn.Close()
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == downloadPath {
							downloads.Add(1)
							got := r.Header.Get("Authorization") != "" || r.Header.Get("Private-Token") != ""
							if got != private {
								t.Error("download credentials differ from visibility")
							}
							http.Redirect(w, r, cdn.URL, http.StatusFound)
							return
						}
						if r.Header.Get("Authorization") == "" && r.Header.Get("Private-Token") == "" {
							t.Error("discovery lacks configured token")
						}
						if strings.Contains(r.URL.Path, "/releases/") {
							if kind == "gitlab" {
								_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1", "assets": map[string]any{"links": []map[string]string{{"name": "tool-linux-amd64", "url": base + downloadPath}}}})
							} else {
								_ = json.NewEncoder(w).Encode(releaseasset.Release{TagName: "v1", Assets: []releaseasset.Asset{{Name: "tool-linux-amd64", BrowserDownloadURL: base + downloadPath}}})
							}
							return
						}
						visibility := "public"
						if private {
							visibility = "private"
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"private": private, "visibility": visibility, "id": 42, "package_registry_access_level": "enabled"})
					}))
					defer srv.Close()
					base = srv.URL
					source := releaseprovider.Source{Type: kind, URL: base, TokenEnv: "TEST_DOWNLOAD_TOKEN"}
					state := &appstate.State{Sources: map[string]releaseprovider.Source{"s": source}, Apps: map[string]*appstate.AppMetadata{"tool": {Source: "s", Repository: "group/tool", Tag: "v1", Hashes: map[string]string{"tool-linux-amd64": hash}}}, Platforms: []string{"linux/amd64/glibc"}, Binaries: map[string]*appstate.BinariesEntry{}}
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
					if info.URL != base+downloadPath || (info.Auth != nil) != private || info.Hash != hash {
						t.Fatalf("generated info=%+v", info)
					}
					bm := binmanager.NewWithResolver(binmanager.MapOfApps{"tool": {Binary: &binmanager.AppConfigBinary{Binaries: saved.Binaries["tool"].Binaries}}}, nil, nil, target.NewResolver(target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcGlibc}))
					t.Setenv("TEST_DOWNLOAD_TOKEN", "")
					before := downloads.Load()
					if private {
						if _, err := bm.GetCommandInfo(context.Background(), "tool"); err == nil || !strings.Contains(err.Error(), "TEST_DOWNLOAD_TOKEN") {
							t.Fatalf("missing credential error=%v", err)
						}
						if downloads.Load() != before {
							t.Fatal("missing token contacted endpoint")
						}
						t.Setenv("TEST_DOWNLOAD_TOKEN", "fixture")
					}
					if _, err := bm.GetCommandInfo(context.Background(), "tool"); err != nil {
						t.Fatal(err)
					}
					want := int32(1)
					if verify {
						want++
					}
					if downloads.Load() != want {
						t.Fatalf("downloads=%d", downloads.Load())
					}
				})
			}
		}
	}
}
