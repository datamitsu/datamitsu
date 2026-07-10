package github

import (
	"testing"
	"time"
)

func TestNormalizeSemver(t *testing.T) {
	tests := []struct {
		tag    string
		want   string
		wantOK bool
	}{
		{"v1.12.3", "v1.12.3", true},
		{"v1.11.11", "v1.11.11", true},
		{"1.55.1", "v1.55.1", true},
		{"0.44.0", "v0.44.0", true},
		{"jq-1.8.2", "v1.8.2", true},
		{"terragrunt-v0.55.0", "v0.55.0", true},
		{"v35.1", "v35.1", true}, // major.minor only, still valid
		{"v1.12.0-rc1", "v1.12.0-rc1", true},
		{"jdk-21.0.8+9", "v21.0.8", true}, // build metadata dropped
		{"nightly", "", false},
		{"latest", "", false},
		{"v2", "", false}, // no minor component
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			got, ok := normalizeSemver(tt.tag)
			if ok != tt.wantOK {
				t.Fatalf("normalizeSemver(%q) ok = %v, want %v", tt.tag, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("normalizeSemver(%q) = %q, want %q", tt.tag, got, tt.want)
			}
		})
	}
}

func TestSelectLatestStableRelease(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)   // comfortably past any cutoff used here
	older := now.Add(-72 * time.Hour) // even older
	fresh := now.Add(-1 * time.Minute)

	pre := func(r *Release) { r.Prerelease = true }
	draft := func(r *Release) { r.Draft = true }
	rel := func(tag string, published time.Time, opts ...func(*Release)) Release {
		r := Release{TagName: tag, PublishedAt: published}
		for _, o := range opts {
			o(&r)
		}
		return r
	}

	tests := []struct {
		name     string
		releases []Release
		minAge   int
		want     string // "" means expect nil
	}{
		{
			name: "highest semver wins over newest-by-date backport",
			// v1.11.11 published most recently but is a lower semver — the
			// OpenTofu regression. Slice is publish-date descending.
			releases: []Release{
				rel("v1.11.11", now.Add(-1*time.Hour)),
				rel("v1.12.3", now.Add(-6*time.Hour)),
				rel("v1.11.10", now.Add(-6*time.Hour)),
				rel("v1.12.2", now.Add(-24*time.Hour)),
				rel("v1.12.1", now.Add(-48*time.Hour)),
			},
			minAge: 0,
			want:   "v1.12.3",
		},
		{
			name: "min-age excludes fresher top semver, picks highest old-enough",
			releases: []Release{
				rel("v1.12.3", fresh), // too new under the 60m cutoff
				rel("v1.11.11", old),
				rel("v1.12.2", old),
			},
			minAge: 60,
			want:   "v1.12.2",
		},
		{
			name: "skips GitHub-flagged prerelease",
			releases: []Release{
				rel("v2.0.0", old, pre),
				rel("v1.0.0", old),
			},
			minAge: 0,
			want:   "v1.0.0",
		},
		{
			name: "skips draft",
			releases: []Release{
				rel("v2.0.0", old, draft),
				rel("v1.0.0", old),
			},
			minAge: 0,
			want:   "v1.0.0",
		},
		{
			name: "skips zero PublishedAt",
			releases: []Release{
				rel("v2.0.0", time.Time{}),
				rel("v1.0.0", old),
			},
			minAge: 0,
			want:   "v1.0.0",
		},
		{
			name: "unflagged semver prerelease does not mask newest stable",
			releases: []Release{
				rel("v1.13.0-rc1", old), // higher core, but a prerelease tag
				rel("v1.12.0", old),
			},
			minAge: 0,
			want:   "v1.12.0",
		},
		{
			name: "only prereleases parse: highest prerelease is chosen",
			releases: []Release{
				rel("v1.0.0-rc2", old),
				rel("v1.0.0-rc1", older),
			},
			minAge: 0,
			want:   "v1.0.0-rc2",
		},
		{
			name: "no parseable version falls back to newest by date",
			releases: []Release{
				rel("nightly", old),
				rel("latest", older),
			},
			minAge: 0,
			want:   "nightly",
		},
		{
			name: "nothing old enough returns nil",
			releases: []Release{
				rel("v2.0.0", fresh),
			},
			minAge: 60,
			want:   "",
		},
		{
			name:     "empty list returns nil",
			releases: nil,
			minAge:   0,
			want:     "",
		},
		{
			name: "all drafts/prereleases returns nil",
			releases: []Release{
				rel("v2.0.0", old, pre),
				rel("v1.0.0", old, draft),
			},
			minAge: 0,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectLatestStableRelease(tt.releases, tt.minAge, now)
			switch {
			case tt.want == "" && got != nil:
				t.Fatalf("expected nil, got %q", got.TagName)
			case tt.want == "" && got == nil:
				return
			case got == nil:
				t.Fatalf("expected %q, got nil", tt.want)
			case got.TagName != tt.want:
				t.Errorf("selectLatestStableRelease = %q, want %q", got.TagName, tt.want)
			}
		})
	}
}
