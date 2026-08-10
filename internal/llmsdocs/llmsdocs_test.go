package llmsdocs

import (
	"regexp"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/llmsmanifest"
)

// maxPageBytes bounds a single embedded page. The snapshot must only ever
// contain cleaned prose, so a page an order of magnitude past the largest real
// one signals that an asset (an inlined image, a cast recording) leaked into the
// harvest.
const maxPageBytes = 256 * 1024

// indexEntryRE matches a rewritten root-index entry: "- [Title](slug): desc",
// where the link target is the CLI argument rather than a URL.
var indexEntryRE = regexp.MustCompile(`^- \[([^\]]+)\]\(([^)]+)\)(?::\s(.*))?$`)

func loadDocs(t *testing.T) *Docs {
	t.Helper()
	docs, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return docs
}

// TestManifestMatchesEmbed is the provenance guard that runs without any
// website toolchain: it proves the committed manifest still describes the
// committed page files. A hand-edited manifest, a partial harvest, or a page
// added or deleted without re-running the harvester all fail here rather than
// silently shipping a snapshot that lies about itself.
func TestManifestMatchesEmbed(t *testing.T) {
	docs := loadDocs(t)
	m := docs.Manifest()

	if m.PageCount != len(m.Pages) {
		t.Errorf("pageCount = %d, but manifest lists %d pages", m.PageCount, len(m.Pages))
	}
	if got := llmsmanifest.ComputePageSetHash(m.Pages); got != m.PageSetHash {
		t.Errorf("pageSetHash = %q, recomputed %q", m.PageSetHash, got)
	}

	// Every manifest page has a file, and every file has a manifest entry.
	files, err := PageFiles()
	if err != nil {
		t.Fatalf("PageFiles() error = %v", err)
	}
	onDisk := make(map[string]bool, len(files))
	for _, f := range files {
		onDisk[f] = true
	}
	for _, p := range m.Pages {
		if !onDisk[p.Slug] {
			t.Errorf("manifest page %q has no file at pages/%s.md", p.Slug, p.Slug)
		}
		delete(onDisk, p.Slug)
	}
	for f := range onDisk {
		t.Errorf("orphan page file pages/%s.md is absent from the manifest", f)
	}
}

// TestManifestMatchesPageBodies pins the title and description in three places
// at once — the manifest, the page body's own heading and blockquote, and the
// rendered index entry. Each is written independently by the harvester, so
// without this they could drift apart undetected and an agent would see one title
// in the index and another in the page.
func TestManifestMatchesPageBodies(t *testing.T) {
	docs := loadDocs(t)

	indexTitles, indexDescriptions := parseIndexEntries(t, docs.Index())

	for _, meta := range docs.Manifest().Pages {
		page, err := docs.Page(meta.Slug)
		if err != nil {
			t.Errorf("Page(%q) error = %v", meta.Slug, err)
			continue
		}

		bodyTitle, bodyDesc := headerOf(page.Body)
		if bodyTitle != meta.Title {
			t.Errorf("page %q: body heading = %q, manifest title = %q", meta.Slug, bodyTitle, meta.Title)
		}
		if bodyDesc != meta.Description {
			t.Errorf("page %q: body description = %q, manifest description = %q", meta.Slug, bodyDesc, meta.Description)
		}
		if got, ok := indexTitles[meta.Slug]; !ok {
			t.Errorf("page %q is missing from the root index", meta.Slug)
		} else if got != meta.Title {
			t.Errorf("page %q: index title = %q, manifest title = %q", meta.Slug, got, meta.Title)
		}
		if got := indexDescriptions[meta.Slug]; got != meta.Description {
			t.Errorf("page %q: index description = %q, manifest description = %q", meta.Slug, got, meta.Description)
		}
		if page.Bytes != len(page.Body) {
			t.Errorf("page %q: manifest bytes = %d, body is %d bytes", meta.Slug, page.Bytes, len(page.Body))
		}
		if page.Bytes > maxPageBytes {
			t.Errorf("page %q is %d bytes, over the %d ceiling: did an asset leak into the harvest?",
				meta.Slug, page.Bytes, maxPageBytes)
		}
	}

	if len(indexTitles) != docs.Manifest().PageCount {
		t.Errorf("root index lists %d pages, manifest has %d", len(indexTitles), docs.Manifest().PageCount)
	}
}

// TestSlugsAreAddressable asserts the invariants Resolve relies on: slugs are
// lowercase (Resolve lowercases its input, so a mixed-case slug would be
// unreachable) and index-collapsed (so no canonical slug ends in "/index").
func TestSlugsAreAddressable(t *testing.T) {
	docs := loadDocs(t)

	for _, slug := range docs.List() {
		if slug != strings.ToLower(slug) {
			t.Errorf("slug %q is not lowercase and could never be resolved", slug)
		}
		if strings.HasSuffix(slug, "/"+llmsmanifest.IndexLeaf) {
			t.Errorf("slug %q is not index-collapsed", slug)
		}
		if _, err := docs.Resolve(slug); err != nil {
			t.Errorf("Resolve(%q) on its own canonical slug: %v", slug, err)
		}
	}
}

// TestListSorted keeps --list output stable so its golden cannot flap.
func TestListSorted(t *testing.T) {
	list := loadDocs(t).List()
	if len(list) == 0 {
		t.Fatal("List() is empty")
	}
	for i := 1; i < len(list); i++ {
		if list[i-1] >= list[i] {
			t.Errorf("List() is not sorted: %q before %q", list[i-1], list[i])
		}
	}
}

// TestResolve covers every spelling of a page identity the command accepts.
func TestResolve(t *testing.T) {
	docs := loadDocs(t)

	cases := []struct {
		name string
		arg  string
		want string
	}{
		{"exact root page", "about", "about"},
		{"exact nested page", "contributing/brand-guidelines", "contributing/brand-guidelines"},
		{"basename shorthand", "brand-guidelines", "contributing/brand-guidelines"},
		{"trailing .md", "about.md", "about"},
		{"nested with .md", "contributing/brand-guidelines.md", "contributing/brand-guidelines"},
		{"leading docs/", "docs/about", "about"},
		{"trailing slash", "about/", "about"},
		{"uppercase", "About", "about"},
		{"surrounding space", "  about  ", "about"},
		{"full website URL", "https://datamitsu.com/docs/guides/architecture/caching.md", "guides/architecture/caching"},
		{"URL without .md", "https://datamitsu.com/docs/about", "about"},
		{"index alias to canonical", "contributing/index", "contributing"},
		{"collapsed index slug", "contributing", "contributing"},
		{"deep index alias", "getting-started/installation/index", "getting-started/installation"},
		{"deep collapsed slug", "getting-started/installation", "getting-started/installation"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := docs.Resolve(tc.arg)
			if err != nil {
				t.Fatalf("Resolve(%q) error = %v", tc.arg, err)
			}
			if got != tc.want {
				t.Errorf("Resolve(%q) = %q, want %q", tc.arg, got, tc.want)
			}
		})
	}
}

// TestResolveUnknown checks that unresolvable arguments fail as unknown rather
// than resolving to something arbitrary.
func TestResolveUnknown(t *testing.T) {
	docs := loadDocs(t)

	for _, arg := range []string{"", "   ", "no-such-page", "guides/no-such-page", "/", "https://example.com/"} {
		if slug, err := docs.Resolve(arg); err == nil {
			t.Errorf("Resolve(%q) = %q, want an error", arg, slug)
		} else if !strings.Contains(err.Error(), "unknown page") {
			t.Errorf("Resolve(%q) error = %v, want an unknown-page error", arg, err)
		}
	}
}

// TestSuggest proves a typo finds its page, that suggestions are deterministic
// (the stderr golden depends on it), and that nonsense suggests nothing.
func TestSuggest(t *testing.T) {
	docs := loadDocs(t)

	cases := []struct {
		name string
		arg  string
		want string // expected first suggestion; "" means expect none
	}{
		{"typo in basename", "instalation", "getting-started/installation"}, //nolint:misspell // the misspelling is the input under test
		{"missing letter", "abut", "about"},
		{"transposition", "aboutt", "about"},
		{"typo in nested slug", "guides/architecture/cachng", "guides/architecture/caching"},
		{"nothing close", "zzzzzzzzzzzzzzzz", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := docs.Suggest(tc.arg)
			if tc.want == "" {
				if len(got) != 0 {
					t.Errorf("Suggest(%q) = %v, want none", tc.arg, got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("Suggest(%q) returned nothing, want %q first", tc.arg, tc.want)
			}
			if got[0] != tc.want {
				t.Errorf("Suggest(%q)[0] = %q, want %q (all: %v)", tc.arg, got[0], tc.want, got)
			}
			if len(got) > maxSuggestions {
				t.Errorf("Suggest(%q) returned %d candidates, cap is %d", tc.arg, len(got), maxSuggestions)
			}
			// Determinism: the same input must produce the same order.
			if second := docs.Suggest(tc.arg); strings.Join(second, ",") != strings.Join(got, ",") {
				t.Errorf("Suggest(%q) is not deterministic: %v then %v", tc.arg, got, second)
			}
		})
	}
}

// TestWebIndexIsStandard checks that --web emits resolvable website URLs, in
// contrast to the default index which emits CLI slugs.
func TestWebIndexIsStandard(t *testing.T) {
	docs := loadDocs(t)
	web := docs.WebIndex()

	for _, p := range docs.Manifest().Pages {
		if !strings.Contains(web, "]("+p.URL+")") {
			t.Errorf("web index is missing the URL for %q (%s)", p.Slug, p.URL)
		}
	}
	if !strings.Contains(web, "## Table of Contents") {
		t.Error("web index has no table of contents heading")
	}
	if strings.Contains(web, "datamitsu llms <slug>") {
		t.Error("web index leaked the CLI-facing header; it must be the standard llms.txt form")
	}
}

// TestPageBodiesAreServed checks every page actually reads back from the
// embedded filesystem, so no slug in the manifest is a dead link.
func TestPageBodiesAreServed(t *testing.T) {
	docs := loadDocs(t)

	for _, slug := range docs.List() {
		page, err := docs.Page(slug)
		if err != nil {
			t.Errorf("Page(%q) error = %v", slug, err)
			continue
		}
		if !strings.HasPrefix(page.Body, "# ") {
			t.Errorf("page %q does not start with a markdown heading", slug)
		}
		if !strings.HasSuffix(page.Body, "\n") {
			t.Errorf("page %q does not end with a newline", slug)
		}
		if strings.Contains(page.Body, "\r") {
			t.Errorf("page %q contains a carriage return; harvest normalization should have removed it", slug)
		}
	}
}

// TestPageUnknownSlug covers the direct-lookup path for a slug that is not in
// the snapshot (Resolve would normally have rejected it first).
func TestPageUnknownSlug(t *testing.T) {
	if _, err := loadDocs(t).Page("definitely/not/a/page"); err == nil {
		t.Error("Page() on an unknown slug returned no error")
	}
}

// headerOf extracts the "# Title" heading and "> Description" blockquote a
// harvested page opens with. A page whose description was empty has had the
// blockquote stripped, so an absent quote means an empty description.
func headerOf(body string) (title, desc string) {
	lines := strings.Split(body, "\n")
	if len(lines) > 0 {
		title = strings.TrimSpace(strings.TrimPrefix(lines[0], "# "))
	}
	for i := 1; i < len(lines) && i < 4; i++ {
		if quoted, ok := strings.CutPrefix(lines[i], ">"); ok {
			return title, strings.TrimSpace(quoted)
		}
	}
	return title, ""
}

// parseIndexEntries reads the rendered root index back into slug-keyed maps so
// it can be compared against the manifest.
func parseIndexEntries(t *testing.T, index string) (titles, descriptions map[string]string) {
	t.Helper()
	titles = make(map[string]string)
	descriptions = make(map[string]string)

	for line := range strings.SplitSeq(index, "\n") {
		if !strings.HasPrefix(line, "- [") {
			continue
		}
		m := indexEntryRE.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("malformed index entry: %q", line)
			continue
		}
		titles[m[2]] = m[1]
		descriptions[m[2]] = m[3]
	}
	return titles, descriptions
}
