package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/datamitsu/datamitsu/internal/appstate"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

func captureStderr(fn func()) string {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	fn()

	_ = w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

// fakeGitHub serves the release and repository endpoints pull-github reads,
// answering each repository the way its scenario says.
type fakeGitHub struct {
	mu       sync.Mutex
	attempts map[string]int
	handle   func(repo string, attempt int, w http.ResponseWriter)
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	repo := parts[1]
	if len(parts) == 2 {
		// Repository metadata: the description is a warning at most.
		_ = json.NewEncoder(w).Encode(map[string]string{"full_name": "o/" + repo, "description": "desc " + repo})
		return
	}
	f.mu.Lock()
	f.attempts[repo]++
	attempt := f.attempts[repo]
	f.mu.Unlock()
	if f.isListing(r) {
		f.handle(repo, attempt, listingWriter{w})
		return
	}
	f.handle(repo, attempt, w)
}

// listingWriter turns a single release written as JSON into the one-element
// listing GetLatestReleaseWithMinAge reads.
type listingWriter struct{ http.ResponseWriter }

func (l listingWriter) Write(b []byte) (int, error) {
	if _, err := l.ResponseWriter.Write([]byte("[")); err != nil {
		return 0, err
	}
	n, err := l.ResponseWriter.Write(bytes.TrimSpace(b))
	if err != nil {
		return n, err
	}
	_, err = l.ResponseWriter.Write([]byte("]\n"))
	return len(b), err
}

func releaseObject(tag string, digest bool) map[string]any {
	asset := map[string]any{
		"name":                 "tool-linux-amd64.tar.gz",
		"browser_download_url": "https://example.test/" + tag + "/tool-linux-amd64.tar.gz",
	}
	if digest {
		asset["digest"] = "sha256:" + strings.Repeat("ab", 32)
	}
	return map[string]any{"tag_name": tag, "published_at": "2020-01-01T00:00:00Z", "assets": []any{asset}}
}

// releaseJSON answers a release lookup, and a release listing with that one
// release, so the same scenario serves runs with and without --update.
func releaseJSON(w http.ResponseWriter, tag string, digest bool) {
	_ = json.NewEncoder(w).Encode(releaseObject(tag, digest))
}

func (f *fakeGitHub) isListing(r *http.Request) bool {
	return strings.HasSuffix(r.URL.Path, "/releases")
}

// A run over several apps attempts every one, retries what is transient,
// reports each failure with its stage, keeps the failed apps' entries, and
// exits non-zero.
func TestRunPullGithub_ReportsFailuresAndExitsNonZero(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}
	api := &fakeGitHub{attempts: map[string]int{}, handle: func(repo string, attempt int, w http.ResponseWriter) {
		switch repo {
		case "flaky":
			if attempt == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			releaseJSON(w, "v1", true)
		case "gone":
			w.WriteHeader(http.StatusNotFound)
		case "limited":
			w.WriteHeader(http.StatusForbidden)
		case "undigested":
			releaseJSON(w, "v1", false)
		default:
			releaseJSON(w, "v1", true)
		}
	}}
	srv := httptest.NewServer(api)
	defer srv.Close()

	githubBaseURL = srv.URL
	defer func() { githubBaseURL = "" }()
	updateFlag = true
	defer func() { updateFlag = false }()
	*pullGithubMinAge = 0
	defer func() { *pullGithubMinAge = minAgeFlagDefault }()

	// "gone" was recorded by an earlier run; its entry must survive untouched.
	previous := &appstate.BinariesEntry{
		ConfigHash:  "old",
		Description: "old description",
		Binaries:    binmanager.MapOfBinaries{"linux": {"amd64": {"glibc": binmanager.BinaryOsArchInfo{URL: "https://example.test/v0/gone", Hash: strings.Repeat("cd", 32), ContentType: binmanager.BinContentTypeTarGz}}}},
	}
	path := filepath.Join(t.TempDir(), "githubApps.json")
	state := &appstate.State{Apps: map[string]*appstate.AppMetadata{}, Binaries: map[string]*appstate.BinariesEntry{"gone": previous}}
	for _, name := range []string{"fine", "flaky", "gone", "limited", "undigested"} {
		state.Apps[name] = &appstate.AppMetadata{Owner: "o", Repo: name, Tag: "v0"}
	}
	if err := appstate.Save(path, state); err != nil {
		t.Fatal(err)
	}

	var err error
	stderr := captureStderr(func() {
		_ = captureStdout(func() { err = runPullGithub(pullGithubCmd, []string{path}) })
	})

	if err == nil || err.Error() != "3 of 5 apps failed" {
		t.Fatalf("runPullGithub() = %v, want 3 of 5 apps failed\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"retry 2/4 for GET " + srv.URL + "/repos/o/flaky/releases?per_page=30",
		"3 of 5 apps failed and are left as they were in " + path,
		"gone (latest release): release not found",
		"limited (latest release): GitHub API rate limit exceeded",
		"undigested (binaries for v1): assets were detected for 2 platform(s) but none carries a SHA-256 digest",
		"Hint: set GITHUB_TOKEN",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if api.attempts["gone"] != 1 || api.attempts["limited"] != 1 {
		t.Errorf("permanent failures were retried: gone=%d limited=%d", api.attempts["gone"], api.attempts["limited"])
	}

	saved, loadErr := appstate.Load(path)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	for _, name := range []string{"fine", "flaky"} {
		entry := saved.Binaries[name]
		if entry == nil || entry.Binaries["linux"] == nil || saved.Apps[name].Tag != "v1" {
			t.Errorf("%s was not updated to v1: tag %q, entry %+v", name, saved.Apps[name].Tag, entry)
		}
	}
	for _, name := range []string{"limited", "undigested"} {
		if saved.Binaries[name] != nil || saved.Apps[name].Tag != "v0" {
			t.Errorf("%s changed although it failed: tag %q, entry %+v", name, saved.Apps[name].Tag, saved.Binaries[name])
		}
	}
	if got := saved.Binaries["gone"]; got == nil || got.ConfigHash != "old" || got.Description != "old description" || saved.Apps["gone"].Tag != "v0" {
		t.Errorf("gone's previous entry did not survive: tag %q, entry %+v", saved.Apps["gone"].Tag, got)
	}
}

func TestRunPullGithub_AllSucceedExitsZero(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}
	api := &fakeGitHub{attempts: map[string]int{}, handle: func(_ string, _ int, w http.ResponseWriter) {
		releaseJSON(w, "v1", true)
	}}
	srv := httptest.NewServer(api)
	defer srv.Close()
	githubBaseURL = srv.URL
	defer func() { githubBaseURL = "" }()

	path := filepath.Join(t.TempDir(), "githubApps.json")
	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{"fine": {Owner: "o", Repo: "fine", Tag: "v1"}},
		Binaries: map[string]*appstate.BinariesEntry{},
	}
	if err := appstate.Save(path, state); err != nil {
		t.Fatal(err)
	}

	var err error
	stdout := captureStdout(func() { err = runPullGithub(pullGithubCmd, []string{path}) })
	if err != nil {
		t.Fatalf("runPullGithub() = %v", err)
	}
	if !strings.Contains(stdout, "✓ Processed 1 apps") {
		t.Errorf("stdout lacks the success summary:\n%s", stdout)
	}
}

func TestIsRateLimited(t *testing.T) {
	if !isRateLimited(&github.RateLimitError{}) || isRateLimited(io.EOF) {
		t.Error("isRateLimited misclassifies")
	}
}

// pull-node lists every package it could not look up after the summary and
// exits non-zero; the package keeps its previous entry.
func TestRunPullNode_ReportsFailedPackages(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/broken") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/latest") {
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "demo", "version": "1.0.0", "description": "test package"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "demo", "description": "test package",
			"dist-tags": map[string]string{"latest": "1.0.0"},
			"versions":  map[string]any{"1.0.0": map[string]string{}},
			"time":      map[string]string{"1.0.0": "2020-01-01T00:00:00Z"},
		})
	}))
	t.Cleanup(srv.Close)

	path := writeNodeApps(t, t.TempDir(), nodeAppsJSON{
		"demo":   {PackageName: "demo", Version: "0.9.0"},
		"broken": {PackageName: "broken", Version: "0.1.0"},
	})
	*pullNodeMinAge = 0
	defer func() { *pullNodeMinAge = minAgeFlagDefault }()
	nodeUpdateFlag = true
	defer func() { nodeUpdateFlag = false }()

	var err error
	var stderr string
	withNPMRegistry(t, srv, func() {
		stderr = captureStderr(func() {
			_ = captureStdout(func() { err = runPullNode(pullNodeCmd, []string{path}) })
		})
	})
	if err == nil || err.Error() != "1 of 2 packages failed" {
		t.Fatalf("runPullNode() = %v, want 1 of 2 packages failed\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"retry 2/4 for GET", "1 of 2 packages failed and are left as they were in " + path, "broken (broken): "} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}

	apps, err := readNodeAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if apps["demo"].Version != "1.0.0" || apps["broken"].Version != "0.1.0" {
		t.Errorf("file holds demo=%s broken=%s, want the update written and the failed package as it was", apps["demo"].Version, apps["broken"].Version)
	}
}
