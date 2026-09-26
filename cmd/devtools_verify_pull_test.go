package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/appstate"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/registry"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

// A download that fails says nothing about the candidate, so the next-ranked
// asset must not be tried in its place: for age that would be the .proof file.
func TestPickBinaryForPlatform_DownloadFailureDoesNotFallBack(t *testing.T) {
	candidates := []github.Asset{
		pickAsset("age-v1.3.2-darwin-amd64.tar.gz"),
		pickAsset("age-v1.3.2-darwin-amd64.tar.gz.proof"),
	}
	var tried []string
	verify := func(_ context.Context, url, _ string, _ binmanager.BinContentType, _ *string) error {
		tried = append(tried, url)
		return &binmanager.DownloadError{Err: errors.New("giving up after 4 attempts: bad status: 502 Bad Gateway")}
	}

	pick, status, err := pickBinaryForPlatform(context.Background(), "age", candidates, glibcAmd64, nil, verify)
	if pick != nil || status != "verification_failed" {
		t.Fatalf("got (%v, %q), want (nil, verification_failed)", pick, status)
	}
	if len(tried) != 1 {
		t.Errorf("tried %v, want only the first candidate", tried)
	}
	var failure *candidateError
	if !errors.As(err, &failure) || failure.asset != "age-v1.3.2-darwin-amd64.tar.gz" || !binmanager.IsDownloadError(err) {
		t.Errorf("err = %v, want a candidateError naming the archive that could not be downloaded", err)
	}
}

// The error a platform reports belongs to the last candidate tried, not to
// the first one ranked.
func TestPickBinaryForPlatform_FailureNamesTheAssetTried(t *testing.T) {
	candidates := []github.Asset{
		pickAsset("yq_linux_amd64.tar.gz"),
		pickAsset("yq_linux_amd64"),
	}
	verify := func(_ context.Context, url, _ string, _ binmanager.BinContentType, _ *string) error {
		return errors.New("not an executable from " + url)
	}
	_, status, err := pickBinaryForPlatform(context.Background(), "yq", candidates, glibcAmd64, nil, verify)
	if status != "verification_failed" || err == nil || !strings.HasPrefix(err.Error(), "yq_linux_amd64: not an executable") {
		t.Fatalf("got (%q, %v), want the raw binary named", status, err)
	}
}

// withRetryNotices wires the retry notifiers the way a command does and
// restores whatever the process had afterwards.
func withRetryNotices(t *testing.T) {
	t.Helper()
	prevRegistry, prevVerify := registry.RetryNotifier, binmanager.VerifyRetryNotifier
	enableRetryNotices()
	t.Cleanup(func() { registry.RetryNotifier, binmanager.VerifyRetryNotifier = prevRegistry, prevVerify })
}

func TestEnableRetryNotices(t *testing.T) {
	withRetryNotices(t)
	if registry.RetryNotifier == nil || binmanager.VerifyRetryNotifier == nil {
		t.Error("enableRetryNotices() left a notifier nil")
	}
}

// An asset is downloaded once per run: the musl tuple reuses what the glibc
// tuple verified, so a network that fails after the first download cannot
// fail an app whose only asset already verified.
func TestRunPullGithub_VerifiesEachAssetOnce(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}
	archive := vcTarGz(t, map[string]string{"once": "#!/bin/sh\necho once\n"})
	downloads := 0
	assets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads++
		if downloads > 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write(archive)
	}))
	defer assets.Close()

	api := &fakeGitHub{attempts: map[string]int{}, handle: func(repo string, _ int, w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1", "published_at": "2020-01-01T00:00:00Z",
			"assets": []any{map[string]any{
				"name": repo + "-linux-amd64.tar.gz", "browser_download_url": assets.URL + "/" + repo + "-linux-amd64.tar.gz", "digest": "sha256:" + vcSHA256Hex(archive),
			}},
		})
	}}
	srv := httptest.NewServer(api)
	defer srv.Close()
	githubBaseURL = srv.URL
	defer func() { githubBaseURL = "" }()
	verifyExtractionFlag = true
	defer func() { verifyExtractionFlag = false }()
	withRetryNotices(t)

	path := filepath.Join(t.TempDir(), "githubApps.json")
	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{"once": {Owner: "o", Repo: "once", Tag: "v1"}},
		Binaries: map[string]*appstate.BinariesEntry{},
	}
	if err := appstate.Save(path, state); err != nil {
		t.Fatal(err)
	}

	var err error
	stderr := captureStderr(func() {
		_ = captureStdout(func() { err = runPullGithub(pullGithubCmd, []string{path}) })
	})
	if err != nil {
		t.Fatalf("runPullGithub() = %v\nstderr:\n%s", err, stderr)
	}
	if downloads != 1 {
		t.Errorf("asset downloaded %d times, want once", downloads)
	}
	saved, loadErr := appstate.Load(path)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if got := saved.Binaries["once"]; got == nil || got.Binaries["linux"]["amd64"]["glibc"].URL == "" {
		t.Errorf("once was not recorded: %+v", got)
	}
}

// Under --verify-extraction a platform whose asset cannot be downloaded fails
// the app: the previous entry stays, the report names the platform and the
// asset, and the run exits non-zero. A platform whose asset downloads and
// verifies is recorded for the app that has no such trouble.
func TestRunPullGithub_VerifyDownloadFailureFailsTheApp(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}
	fine := vcTarGz(t, map[string]string{"fine": "#!/bin/sh\necho fine\n"})
	assets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/fine/") {
			_, _ = w.Write(fine)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer assets.Close()

	api := &fakeGitHub{attempts: map[string]int{}, handle: func(repo string, _ int, w http.ResponseWriter) {
		digest := "sha256:" + vcSHA256Hex(fine)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1", "published_at": "2020-01-01T00:00:00Z",
			"assets": []any{
				map[string]any{"name": repo + "-linux-amd64.tar.gz", "browser_download_url": assets.URL + "/" + repo + "/" + repo + "-linux-amd64.tar.gz", "digest": digest},
				map[string]any{"name": repo + "-linux-amd64.tar.gz.proof", "browser_download_url": assets.URL + "/" + repo + "/" + repo + "-linux-amd64.tar.gz.proof", "digest": digest},
			},
		})
	}}
	srv := httptest.NewServer(api)
	defer srv.Close()
	githubBaseURL = srv.URL
	defer func() { githubBaseURL = "" }()
	verifyExtractionFlag = true
	defer func() { verifyExtractionFlag = false }()
	withRetryNotices(t)

	previous := &appstate.BinariesEntry{
		ConfigHash: "old",
		Binaries:   binmanager.MapOfBinaries{"darwin": {"amd64": {"unknown": binmanager.BinaryOsArchInfo{URL: "https://example.test/v0/flaky", Hash: strings.Repeat("cd", 32), ContentType: binmanager.BinContentTypeTarGz}}}},
	}
	path := filepath.Join(t.TempDir(), "githubApps.json")
	state := &appstate.State{
		Apps: map[string]*appstate.AppMetadata{
			"fine":  {Owner: "o", Repo: "fine", Tag: "v1"},
			"flaky": {Owner: "o", Repo: "flaky", Tag: "v1"},
		},
		Binaries: map[string]*appstate.BinariesEntry{"flaky": previous},
	}
	if err := appstate.Save(path, state); err != nil {
		t.Fatal(err)
	}

	var err error
	stderr := captureStderr(func() {
		_ = captureStdout(func() { err = runPullGithub(pullGithubCmd, []string{path}) })
	})
	if err == nil || err.Error() != "1 of 2 apps failed" {
		t.Fatalf("runPullGithub() = %v, want 1 of 2 apps failed\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"retry 2/4 for GET " + assets.URL + "/flaky/flaky-linux-amd64.tar.gz",
		"flaky (verify linux/amd64/glibc): flaky-linux-amd64.tar.gz: download failed: giving up after 4 attempts",
		"flaky (verify linux/amd64/musl): flaky-linux-amd64.tar.gz: download failed",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, ".proof") {
		t.Errorf("the attestation file was tried as a candidate:\n%s", stderr)
	}

	saved, loadErr := appstate.Load(path)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if got := saved.Binaries["flaky"]; got == nil || got.ConfigHash != "old" || got.Binaries["darwin"] == nil {
		t.Errorf("flaky's previous entry did not survive: %+v", got)
	}
	if got := saved.Binaries["fine"]; got == nil || got.Binaries["linux"] == nil {
		t.Errorf("fine was not recorded: %+v", got)
	}
}
