package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/appstate"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/releaseasset"
	"github.com/datamitsu/datamitsu/internal/releaseprovider"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/target"
)

func TestPullReleasesPrivateAssetsInstallAndVerify(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_PRIVATE_FORGE_TOKEN", "private-fixture-value")
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	oldUpdate, oldVerify := updateFlag, verifyExtractionFlag
	updateFlag, verifyExtractionFlag = false, true
	t.Cleanup(func() { updateFlag, verifyExtractionFlag = oldUpdate, oldVerify })
	data := []byte("#!/bin/sh\necho tool\n")
	h := sha256.Sum256(data)
	hash := hex.EncodeToString(h[:])
	downloads := 0
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantAuth := "token private-fixture-value"
		if strings.Contains(r.URL.Path, "/releases/download/") {
			wantAuth = "Bearer private-fixture-value"
		}
		if r.Header.Get("Authorization") != wantAuth {
			t.Error("private request lacks credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/group/tool/releases/download/v1/") {
			downloads++
			_, _ = w.Write(data)
			return
		}
		if strings.Contains(r.URL.Path, "/releases/") {
			_ = json.NewEncoder(w).Encode(releaseasset.Release{TagName: "v1", PublishedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Assets: []releaseasset.Asset{{Name: "tool-linux-amd64", BrowserDownloadURL: base + "/group/tool/releases/download/v1/tool-linux-amd64"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"description": "tool", "private": true})
	}))
	defer srv.Close()
	base = srv.URL
	state := &appstate.State{Sources: map[string]releaseprovider.Source{"private": {Type: "forgejo", URL: base, TokenEnv: "TEST_PRIVATE_FORGE_TOKEN"}}, Platforms: []string{"linux/amd64/glibc", "linux/amd64/musl"}, Apps: map[string]*appstate.AppMetadata{"tool": {Source: "private", Repository: "group/tool", Tag: "v1", Hashes: map[string]string{"tool-linux-amd64": hash}}}, Binaries: map[string]*appstate.BinariesEntry{}}
	path := filepath.Join(t.TempDir(), "binaryApps.json")
	if err := appstate.Save(path, state); err != nil {
		t.Fatal(err)
	}
	if err := runPullReleases(pullReleasesCmd, []string{path}); err != nil {
		t.Fatal(err)
	}
	if downloads != 1 {
		t.Fatalf("shared asset downloaded %d times", downloads)
	}
	saved, err := appstate.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(text), "private-fixture-value") {
		t.Fatal("credential value serialized")
	}
	info := saved.Binaries["tool"].Binaries["linux"]["amd64"]["glibc"]
	if info.Auth == nil || info.Auth.TokenEnv != "TEST_PRIVATE_FORGE_TOKEN" {
		t.Fatal("credential reference not preserved")
	}
	bm := binmanager.NewWithResolver(binmanager.MapOfApps{"tool": {Binary: &binmanager.AppConfigBinary{Binaries: saved.Binaries["tool"].Binaries}}}, nil, nil, target.NewResolver(target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcGlibc}))
	command, err := bm.GetCommandInfo(context.Background(), "tool")
	if err != nil || command == nil {
		t.Fatalf("install command=%v err=%v", command, err)
	}
	if downloads != 2 {
		t.Fatalf("installation did not use download auth: %d", downloads)
	}
}
