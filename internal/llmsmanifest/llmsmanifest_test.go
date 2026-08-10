package llmsmanifest

import (
	"strings"
	"testing"
)

func TestCanonicalSlug(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain page untouched", "about", "about"},
		{"nested page untouched", "contributing/brand-guidelines", "contributing/brand-guidelines"},
		{"directory index collapses", "contributing/index", "contributing"},
		{"deep directory index collapses", "getting-started/installation/index", "getting-started/installation"},
		{"root index has no parent to collapse into", "index", "index"},
		{"index in the middle is not a suffix", "index/overview", "index/overview"},
		{"page merely ending in index is untouched", "guides/reindex", "guides/reindex"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanonicalSlug(tc.raw); got != tc.want {
				t.Errorf("CanonicalSlug(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestComputePageSetHashIsOrderIndependent proves the hash identifies the page
// set itself, not the order it happens to be listed in, so reordering the
// manifest cannot masquerade as a content change.
func TestComputePageSetHashIsOrderIndependent(t *testing.T) {
	a := []Page{
		{Slug: "about", ContentHash: "aaaa"},
		{Slug: "intro", ContentHash: "bbbb"},
		{Slug: "guides/caching", ContentHash: "cccc"},
	}
	reversed := []Page{a[2], a[1], a[0]}

	if got, want := ComputePageSetHash(reversed), ComputePageSetHash(a); got != want {
		t.Errorf("hash changed with page order: %q vs %q", got, want)
	}

	changed := []Page{a[0], a[1], {Slug: "guides/caching", ContentHash: "dddd"}}
	if ComputePageSetHash(changed) == ComputePageSetHash(a) {
		t.Error("hash did not change when a page's content hash changed")
	}

	renamed := []Page{a[0], a[1], {Slug: "guides/cache", ContentHash: "cccc"}}
	if ComputePageSetHash(renamed) == ComputePageSetHash(a) {
		t.Error("hash did not change when a page was renamed")
	}
}

func TestFind(t *testing.T) {
	m := Manifest{Pages: []Page{{Slug: "about", Title: "About"}, {Slug: "intro", Title: "Intro"}}}

	if p, ok := m.Find("intro"); !ok || p.Title != "Intro" {
		t.Errorf("Find(\"intro\") = %+v, %v; want the Intro page", p, ok)
	}
	if _, ok := m.Find("missing"); ok {
		t.Error("Find(\"missing\") reported a hit")
	}
}

func TestValidate(t *testing.T) {
	// valid returns a well-formed manifest that each case then breaks in one way.
	valid := func() Manifest {
		pages := []Page{
			{Slug: "about", Aliases: []string{}, ContentHash: "aaaa"},
			{Slug: "contributing", Aliases: []string{"contributing/index"}, ContentHash: "bbbb"},
		}
		return Manifest{
			SchemaVersion: SchemaVersion,
			Generator:     Generator,
			License:       "MIT",
			PageCount:     len(pages),
			PageSetHash:   ComputePageSetHash(pages),
			Pages:         pages,
		}
	}

	baseline := valid()
	if err := baseline.Validate(); err != nil {
		t.Fatalf("baseline manifest is invalid: %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(*Manifest)
		wantErr string
	}{
		{
			name:    "unsupported schema version",
			mutate:  func(m *Manifest) { m.SchemaVersion = SchemaVersion + 1 },
			wantErr: "schemaVersion",
		},
		{
			name:    "no pages",
			mutate:  func(m *Manifest) { m.Pages = nil; m.PageCount = 0 },
			wantErr: "no pages",
		},
		{
			name:    "page count disagrees with page list",
			mutate:  func(m *Manifest) { m.PageCount = 99 },
			wantErr: "pageCount",
		},
		{
			name:    "duplicate slug",
			mutate:  func(m *Manifest) { m.Pages[1].Slug = "about" },
			wantErr: "duplicate slug",
		},
		{
			name:    "empty slug",
			mutate:  func(m *Manifest) { m.Pages[0].Slug = "" },
			wantErr: "empty slug",
		},
		{
			name:    "uppercase slug is unreachable",
			mutate:  func(m *Manifest) { m.Pages[0].Slug = "About" },
			wantErr: "not lowercase",
		},
		{
			name:    "slug not index-collapsed",
			mutate:  func(m *Manifest) { m.Pages[0].Slug = "guides/index" },
			wantErr: "not index-collapsed",
		},
		{
			name:    "slug with leading slash",
			mutate:  func(m *Manifest) { m.Pages[0].Slug = "/about" },
			wantErr: "must not start or end",
		},
		{
			name:    "alias duplicates its own slug",
			mutate:  func(m *Manifest) { m.Pages[1].Aliases = []string{"contributing"} },
			wantErr: "duplicates its own slug",
		},
		{
			name:    "uppercase alias",
			mutate:  func(m *Manifest) { m.Pages[1].Aliases = []string{"Contributing/Index"} },
			wantErr: "not lowercase",
		},
		{
			name:    "stale page set hash",
			mutate:  func(m *Manifest) { m.Pages[0].ContentHash = "zzzz" },
			wantErr: "pageSetHash mismatch",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := valid()
			tc.mutate(&m)

			err := m.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted a manifest broken by %q", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate() error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
