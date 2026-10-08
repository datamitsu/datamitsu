package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/appstate"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

func TestSelectedPlatformBinaries(t *testing.T) {
	old := verifyExtractionFlag
	verifyExtractionFlag = false
	t.Cleanup(func() { verifyExtractionFlag = old })
	release := &github.Release{TagName: "v1", Assets: []github.Asset{
		pickAsset("tool-linux-amd64"), pickAsset("tool-darwin-arm64"),
		{Name: "tool-windows-arm64.exe", BrowserDownloadURL: "https://example.test/no-digest"},
	}}
	for _, selection := range [][]string{
		{"darwin/arm64"},
		{"linux/amd64/musl"},
		{"linux/amd64/glibc", "linux/amd64/musl", "darwin/arm64", "darwin/arm64"},
	} {
		state := &appstate.State{Platforms: selection}
		entry, err := buildBinariesForApp(context.Background(), "tool", release, "hash", state)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, arches := range entry.Binaries {
			for _, libcs := range arches {
				count += len(libcs)
			}
		}
		if count != len(selectedPlatformTuples(selection)) {
			t.Fatalf("selection %v generated %d leaves", selection, count)
		}
	}
	for _, selection := range [][]string{{"windows/arm64"}, {"freebsd/arm64"}, {"darwin/arm64", "freebsd/arm64"}} {
		entry, err := buildBinariesForApp(context.Background(), "tool", release, "hash", &appstate.State{Platforms: selection})
		if err == nil || entry != nil {
			t.Fatalf("selection %v must fail without partial entry", selection)
		}
	}
}

func TestPullGithubPlatformTransitions(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatal(err)
	}
	oldUpdate, oldVerify, oldURL := updateFlag, verifyExtractionFlag, githubBaseURL
	updateFlag, verifyExtractionFlag = false, false
	t.Cleanup(func() { updateFlag, verifyExtractionFlag, githubBaseURL = oldUpdate, oldVerify, oldURL })
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.URL.Path, "/releases/") {
			_ = json.NewEncoder(w).Encode(&github.Release{TagName: "v1", Assets: []github.Asset{pickAsset("tool-linux-amd64"), pickAsset("tool-darwin-arm64")}})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]string{"description": "tool"})
		}
	}))
	defer srv.Close()
	githubBaseURL = srv.URL
	path := filepath.Join(t.TempDir(), "apps.json")
	state := &appstate.State{Apps: map[string]*appstate.AppMetadata{"tool": {Owner: "o", Repo: "r", Tag: "v1"}}, Binaries: map[string]*appstate.BinariesEntry{}}
	run := func() {
		t.Helper()
		if err := appstate.Save(path, state); err != nil {
			t.Fatal(err)
		}
		if err := runPullGithub(pullGithubCmd, []string{path}); err != nil {
			t.Fatal(err)
		}
		var err error
		state, err = appstate.Load(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	run()
	state.Platforms = []string{"darwin/arm64"}
	run()
	if len(state.Binaries["tool"].Binaries) != 1 {
		t.Fatal("narrowing did not prune")
	}
	prior := calls
	run()
	if calls != prior {
		t.Fatal("unchanged selector did not skip discovery")
	}
	state.Platforms = []string{"linux/amd64/musl", "darwin/arm64"}
	run()
	if _, ok := state.Binaries["tool"].Binaries["linux"]["amd64"]["musl"]; !ok {
		t.Fatal("expansion did not discover musl")
	}
	state.Platforms = nil
	run()
	if _, ok := state.Binaries["tool"].Binaries["linux"]["amd64"]["glibc"]; !ok {
		t.Fatal("removing selector did not restore default-all")
	}
}

func TestPlatformPruningSurvivesPullFailureAndSkip(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatal(err)
	}
	oldUpdate, oldURL := updateFlag, githubBaseURL
	updateFlag = false
	t.Cleanup(func() { updateFlag, githubBaseURL = oldUpdate, oldURL })
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	githubBaseURL = srv.URL
	for _, scenario := range []string{"failure", "skip", "no-apps"} {
		t.Run(scenario, func(t *testing.T) {
			metadata := &appstate.AppMetadata{Owner: "o", Repo: "r", Tag: "v1"}
			state := &appstate.State{Platforms: []string{"darwin/arm64"}, Apps: map[string]*appstate.AppMetadata{}, Binaries: map[string]*appstate.BinariesEntry{
				"tool": {ConfigHash: "old", Binaries: binmanager.MapOfBinaries{"linux": {"amd64": {"glibc": {URL: "old"}}}, "darwin": {"arm64": {"unknown": {URL: "keep"}}}}},
			}}
			if scenario != "no-apps" {
				state.Apps["tool"] = metadata
			}
			if scenario == "skip" {
				state.Binaries["tool"].ConfigHash = appstate.ComputeConfigHash(metadata, state.Platforms)
			}
			path := filepath.Join(t.TempDir(), "apps.json")
			if err := appstate.Save(path, state); err != nil {
				t.Fatal(err)
			}
			err := runPullGithub(pullGithubCmd, []string{path})
			if (err != nil) != (scenario == "failure") {
				t.Fatalf("unexpected run error: %v", err)
			}
			saved, err := appstate.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(saved.Binaries["tool"].Binaries) != 1 {
				t.Fatal("pruning was not saved")
			}
			expectedHash := ""
			if scenario == "skip" {
				expectedHash = state.Binaries["tool"].ConfigHash
			}
			if saved.Binaries["tool"].ConfigHash != expectedHash {
				t.Fatal("hash changed on failure or skip")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "invalid.json")
	invalid := []byte(`{"platforms":["Darwin/arm64"],"apps":{},"binaries":{}}`)
	if err := os.WriteFile(path, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runPullGithub(pullGithubCmd, []string{path}); err == nil {
		t.Fatal("invalid selector accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(invalid) {
		t.Fatal("invalid manifest was changed")
	}
}

func TestSelectedPlatformsOnlyDownloadChosenAssets(t *testing.T) {
	old := verifyExtractionFlag
	verifyExtractionFlag = true
	t.Cleanup(func() { verifyExtractionFlag = old })
	archive := vcTarGz(t, map[string]string{"tool": "#!/bin/sh\necho tool\n"})
	downloads := 0
	skipped := 0
	assets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "darwin") {
			skipped++
			http.Error(w, "unselected", http.StatusInternalServerError)
			return
		}
		downloads++
		_, _ = w.Write(archive)
	}))
	defer assets.Close()
	release := &github.Release{TagName: "v1", Assets: []github.Asset{
		{Name: "tool-linux-amd64.tar.gz", BrowserDownloadURL: assets.URL + "/tool-linux-amd64.tar.gz", Digest: "sha256:" + vcSHA256Hex(archive)},
		{Name: "tool-darwin-arm64.tar.gz", BrowserDownloadURL: assets.URL + "/tool-darwin-arm64.tar.gz", Digest: "sha256:" + vcSHA256Hex(archive)},
	}}
	entry, err := buildBinariesForApp(context.Background(), "tool", release, "hash", &appstate.State{Platforms: []string{"linux/amd64/glibc", "linux/amd64/musl"}})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 || downloads != 1 {
		t.Fatalf("downloads=%d, unselected=%d", downloads, skipped)
	}
	if len(entry.Binaries["linux"]["amd64"]) != 2 {
		t.Fatal("selected libc keys must both be retained")
	}
}

func TestRemovingPlatformsAfterFailedPullRestoresDefault(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatal(err)
	}
	oldUpdate, oldURL := updateFlag, githubBaseURL
	updateFlag = false
	t.Cleanup(func() { updateFlag, githubBaseURL = oldUpdate, oldURL })
	api := &fakeGitHub{attempts: map[string]int{}, handle: func(_ string, _ int, w http.ResponseWriter) { releaseJSON(w, "v1", true) }}
	srv := httptest.NewServer(api)
	defer srv.Close()
	githubBaseURL = srv.URL
	metadata := &appstate.AppMetadata{Owner: "o", Repo: "tool", Tag: "v1"}
	state := &appstate.State{Platforms: []string{"darwin/arm64"}, Apps: map[string]*appstate.AppMetadata{"tool": metadata}, Binaries: map[string]*appstate.BinariesEntry{
		"tool": {ConfigHash: appstate.ComputeConfigHash(metadata), Binaries: binmanager.MapOfBinaries{
			"linux": {"amd64": {"glibc": {URL: "old"}}}, "darwin": {"arm64": {"unknown": {URL: "keep"}}},
		}},
	}}
	path := filepath.Join(t.TempDir(), "apps.json")
	if err := appstate.Save(path, state); err != nil {
		t.Fatal(err)
	}
	if err := runPullGithub(pullGithubCmd, []string{path}); err == nil {
		t.Fatal("missing darwin should fail")
	}
	state, err := appstate.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Binaries["tool"].ConfigHash != "" {
		t.Fatal("failed pruning retained default-all hash")
	}
	state.Platforms = nil
	if err := appstate.Save(path, state); err != nil {
		t.Fatal(err)
	}
	if err := runPullGithub(pullGithubCmd, []string{path}); err != nil {
		t.Fatal(err)
	}
	state, err = appstate.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Binaries["tool"].Binaries["linux"]["amd64"]["glibc"].URL == "" {
		t.Fatal("default-all was not restored")
	}
}

func TestPlatformPruningPreservesBinaryPathHistory(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatal(err)
	}
	oldUpdate, oldVerify, oldURL := updateFlag, verifyExtractionFlag, githubBaseURL
	updateFlag, verifyExtractionFlag = false, false
	t.Cleanup(func() { updateFlag, verifyExtractionFlag, githubBaseURL = oldUpdate, oldVerify, oldURL })
	api := &fakeGitHub{attempts: map[string]int{}, handle: func(_ string, _ int, w http.ResponseWriter) { releaseJSON(w, "v1", true) }}
	srv := httptest.NewServer(api)
	defer srv.Close()
	githubBaseURL = srv.URL
	binaryPath := "custom/bin/tool"
	state := &appstate.State{Platforms: []string{"linux/amd64/musl"}, Apps: map[string]*appstate.AppMetadata{"tool": {Owner: "o", Repo: "tool", Tag: "v1"}}, Binaries: map[string]*appstate.BinariesEntry{
		"tool": {Binaries: binmanager.MapOfBinaries{"linux": {"amd64": {"glibc": {
			URL: "https://example.test/v1/tool-linux-amd64.tar.gz", Hash: testHash1, ContentType: binmanager.BinContentTypeTarGz, BinaryPath: &binaryPath,
		}}}}},
	}}
	path := filepath.Join(t.TempDir(), "apps.json")
	if err := appstate.Save(path, state); err != nil {
		t.Fatal(err)
	}
	if err := runPullGithub(pullGithubCmd, []string{path}); err != nil {
		t.Fatal(err)
	}
	state, err := appstate.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := state.Binaries["tool"].Binaries["linux"]["amd64"]["musl"].BinaryPath
	if got == nil || *got != binaryPath {
		t.Fatalf("history lost: %v", got)
	}
}
