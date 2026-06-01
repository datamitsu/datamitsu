package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const goDevReleasesURL = "https://go.dev/dl/?mode=json&include=all"

// GoRelease is a resolved Go release: the stable version with the "go" prefix
// stripped (e.g. "1.26.3") and a filename→SHA-256 map for every published file.
type GoRelease struct {
	Version string
	Files   map[string]string
}

type goDevFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Kind     string `json:"kind"`
}

type goDevRelease struct {
	Version string      `json:"version"`
	Stable  bool        `json:"stable"`
	Files   []goDevFile `json:"files"`
}

var goDevHTTPClient = &http.Client{Timeout: 15 * time.Second}

// GetLatestGoRelease fetches the latest stable Go release (version + per-file
// SHA-256 hashes) from go.dev over HTTPS. Unlike the Node dist manifest there is
// no GPG signature here; the published SHA-256, pinned in git, is the integrity
// anchor — the same trust model as the musl Node path (see
// cmd/devtools_pull_runtimes.go).
func GetLatestGoRelease() (*GoRelease, error) {
	return getLatestGoReleaseFromURL(goDevReleasesURL)
}

func getLatestGoReleaseFromURL(url string) (*GoRelease, error) {
	resp, err := goDevHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Go releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("go.dev returned status %d: %s", resp.StatusCode, string(body))
	}

	var releases []goDevRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&releases); err != nil {
		return nil, fmt.Errorf("failed to decode Go releases: %w", err)
	}

	rel := highestStableGoRelease(releases)
	if rel == nil {
		return nil, fmt.Errorf("no stable version found in Go releases")
	}

	version := strings.TrimPrefix(rel.Version, "go")
	if version == "" {
		return nil, fmt.Errorf("go.dev release has empty version")
	}

	files := make(map[string]string, len(rel.Files))
	for _, f := range rel.Files {
		if f.Filename == "" || f.SHA256 == "" {
			continue
		}
		files[f.Filename] = f.SHA256
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("go.dev release %s has no files with published hashes", rel.Version)
	}

	return &GoRelease{Version: version, Files: files}, nil
}

// highestStableGoRelease returns the stable release with the highest semantic
// version, independent of the order go.dev returns them in.
func highestStableGoRelease(releases []goDevRelease) *goDevRelease {
	var best *goDevRelease
	for i := range releases {
		r := &releases[i]
		if !r.Stable {
			continue
		}
		if best == nil || goVersionLess(best.Version, r.Version) {
			best = r
		}
	}
	return best
}

// goVersionLess reports whether Go version a sorts below b (e.g. "go1.26.2" <
// "go1.26.3"). Falls back to string comparison if either is not valid semver.
func goVersionLess(a, b string) bool {
	av := "v" + strings.TrimPrefix(a, "go")
	bv := "v" + strings.TrimPrefix(b, "go")
	if semver.IsValid(av) && semver.IsValid(bv) {
		return semver.Compare(av, bv) < 0
	}
	return a < b
}
