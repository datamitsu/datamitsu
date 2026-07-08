package github

import (
	"regexp"
	"time"

	"golang.org/x/mod/semver"
)

// semverTagRe extracts the first version-looking token — major.minor with an
// optional patch and prerelease suffix — from an arbitrary release tag such as
// "v1.12.3", "jq-1.8.2", "terragrunt-v0.55.0", or "35.1". Build metadata (the
// "+9" in "jdk-21.0.8+9") is intentionally excluded: golang.org/x/mod/semver
// ignores it during comparison, and dropping it keeps the token canonical.
var semverTagRe = regexp.MustCompile(`(?i)v?(\d+\.\d+(?:\.\d+)?(?:-[0-9a-z][0-9a-z.-]*)?)`)

// normalizeSemver extracts a canonical, comparable semantic version from an
// arbitrary release tag. It returns the "vX.Y.Z[-pre]" form understood by
// golang.org/x/mod/semver and true, or ("", false) when the tag carries no
// recognisable version (e.g. "nightly", "latest", a bare "v2").
func normalizeSemver(tag string) (string, bool) {
	m := semverTagRe.FindStringSubmatch(tag)
	if m == nil {
		return "", false
	}
	v := "v" + m[1]
	if !semver.IsValid(v) {
		return "", false
	}
	return v, true
}

// selectLatestStableRelease chooses the release with the highest semantic
// version among the non-draft, non-prerelease, published releases that satisfy
// the minimum-age cutoff.
//
// GitHub returns releases newest-first by publish date, but projects that
// backport fixes to older branches publish a lower semver more recently — e.g.
// OpenTofu shipped v1.11.11 after v1.12.3. Selecting by publish date therefore
// silently downgrades such tools; selection must be by semantic version.
//
// minAgeMinutes <= 0 disables the age cutoff (selection stays semver-based).
// now is injected so callers/tests control the cutoff clock. Returns nil when
// no release qualifies (empty list, all drafts/prereleases, or — under an
// active cutoff — nothing old enough).
func selectLatestStableRelease(releases []Release, minAgeMinutes int, now time.Time) *Release {
	applyCutoff := minAgeMinutes > 0
	cutoff := now.Add(-time.Duration(minAgeMinutes) * time.Minute)

	type candidate struct {
		rel    *Release
		ver    string
		parsed bool
	}

	var qualifying []candidate
	for i := range releases {
		r := &releases[i]
		if r.Prerelease || r.Draft || r.PublishedAt.IsZero() {
			continue
		}
		if applyCutoff && r.PublishedAt.After(cutoff) {
			continue
		}
		ver, ok := normalizeSemver(r.TagName)
		qualifying = append(qualifying, candidate{rel: r, ver: ver, parsed: ok})
	}
	if len(qualifying) == 0 {
		return nil
	}

	// qualifying preserves GitHub's publish-date-descending order. A strict ">"
	// therefore keeps the most recently published release when two tags carry
	// the same semantic version (e.g. differing only in build metadata).
	best := -1
	pick := func(accept func(candidate) bool) {
		for i, c := range qualifying {
			if !accept(c) {
				continue
			}
			if best == -1 || semver.Compare(c.ver, qualifying[best].ver) > 0 {
				best = i
			}
		}
	}

	// Prefer a parseable, stable version. Some projects forget to flag a tagged
	// prerelease (e.g. "v1.13.0-rc1") as a GitHub prerelease; excluding semver
	// prereleases here keeps such a tag from masking the newest stable release.
	pick(func(c candidate) bool { return c.parsed && semver.Prerelease(c.ver) == "" })
	if best == -1 {
		pick(func(c candidate) bool { return c.parsed }) // only prereleases parsed
	}
	if best == -1 {
		best = 0 // nothing parseable; fall back to newest by publish date
	}

	chosen := *qualifying[best].rel
	return &chosen
}
