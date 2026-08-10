package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/llmsmanifest"
)

func TestSlugFromURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{name: "root page", url: "https://datamitsu.com/docs/about.md", want: "about"},
		{name: "nested page", url: "https://datamitsu.com/docs/guides/architecture/caching.md", want: "guides/architecture/caching"},
		{name: "directory index", url: "https://datamitsu.com/docs/contributing/index.md", want: "contributing/index"},
		{name: "innermost docs segment wins", url: "https://example.com/docs/site/docs/about.md", want: "about"},
		{name: "no docs segment", url: "https://datamitsu.com/blog/hello.md", wantErr: true},
		{name: "nothing after the marker", url: "https://datamitsu.com/docs/", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := slugFromURL(tc.url)
			if tc.wantErr {
				if ok {
					t.Errorf("slugFromURL(%q) = %q, want failure", tc.url, got)
				}
				return
			}
			if !ok {
				t.Fatalf("slugFromURL(%q) failed", tc.url)
			}
			if got != tc.want {
				t.Errorf("slugFromURL(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

// TestNormalizeBytes pins the byte-level normalization the drift gate depends
// on: a CRLF checkout or a missing final newline must not change the snapshot.
func TestNormalizeBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "crlf becomes lf", in: "a\r\nb\r\n", want: "a\nb\n"},
		{name: "lone cr becomes lf", in: "a\rb", want: "a\nb\n"},
		{name: "missing trailing newline is added", in: "a", want: "a\n"},
		{name: "repeated trailing newlines collapse", in: "a\n\n\n", want: "a\n"},
		{name: "already normal is unchanged", in: "a\nb\n", want: "a\nb\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(normalizeBytes([]byte(tc.in)))
			if got != tc.want {
				t.Errorf("normalizeBytes(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := string(normalizeBytes([]byte(got))); again != got {
				t.Errorf("normalizeBytes is not idempotent: %q then %q", got, again)
			}
		})
	}
}

func TestSplitHeader(t *testing.T) {
	t.Run("title and description", func(t *testing.T) {
		in := "# About datamitsu\n\n> Why it exists\n\nBody text.\n"

		title, desc, body, err := splitHeader([]byte(in))
		if err != nil {
			t.Fatalf("splitHeader() error = %v", err)
		}
		if title != "About datamitsu" {
			t.Errorf("title = %q, want %q", title, "About datamitsu")
		}
		if desc != "Why it exists" {
			t.Errorf("description = %q, want %q", desc, "Why it exists")
		}
		if string(body) != in {
			t.Errorf("body was modified: %q", body)
		}
	})

	// The plugin re-emits the page's own "# Title" right below the preamble it
	// prepends, so the raw page opens with its title twice. The harvester drops
	// the duplicate.
	t.Run("duplicate title heading is dropped", func(t *testing.T) {
		in := "# About\n\n> About desc\n\n# About\n\n## Section\n\nText.\n"

		title, desc, body, err := splitHeader([]byte(in))
		if err != nil {
			t.Fatalf("splitHeader() error = %v", err)
		}
		if title != "About" || desc != "About desc" {
			t.Errorf("header = (%q, %q), want (About, About desc)", title, desc)
		}
		if want := "# About\n\n> About desc\n\n## Section\n\nText.\n"; string(body) != want {
			t.Errorf("body = %q, want %q", body, want)
		}
		if strings.Count(string(body), "# About\n") != 1 {
			t.Errorf("title still appears more than once:\n%s", body)
		}
	})

	// A page whose body does not repeat the title is left intact apart from the
	// preamble normalization.
	t.Run("body without a duplicate title is preserved", func(t *testing.T) {
		in := "# About datamitsu\n\n> Why it exists\n\n## Section\n\nBody text.\n"

		_, _, body, err := splitHeader([]byte(in))
		if err != nil {
			t.Fatalf("splitHeader() error = %v", err)
		}
		if string(body) != in {
			t.Errorf("body = %q, want it unchanged (%q)", body, in)
		}
	})

	// A page whose frontmatter has no description makes the plugin emit a bare
	// blockquote. Serving that to a model would open the page with a stray "> ",
	// so it is dropped and reported as an empty description rather than guessed.
	t.Run("empty blockquote is dropped", func(t *testing.T) {
		in := "# Untitled Page\n\n> \n\n# Untitled Page\n\nBody text.\n"

		title, desc, body, err := splitHeader([]byte(in))
		if err != nil {
			t.Fatalf("splitHeader() error = %v", err)
		}
		if title != "Untitled Page" {
			t.Errorf("title = %q, want %q", title, "Untitled Page")
		}
		if desc != "" {
			t.Errorf("description = %q, want empty", desc)
		}
		if want := "# Untitled Page\n\nBody text.\n"; string(body) != want {
			t.Errorf("body = %q, want %q", body, want)
		}
	})

	t.Run("rejects malformed pages", func(t *testing.T) {
		for name, in := range map[string]string{
			"no heading":     "Just text\n\n> desc\n",
			"empty heading":  "# \n\n> desc\n",
			"no description": "# Title\n\nBody with no blockquote near the top.\n",
		} {
			if _, _, _, err := splitHeader([]byte(in)); err == nil {
				t.Errorf("%s: splitHeader() accepted %q", name, in)
			}
		}
	})
}

func TestParseRootIndex(t *testing.T) {
	const index = `# Datamitsu

> Your toolchain deserves a home.

This file contains links to documentation sections following the llmstxt.org standard.

## Table of Contents

- [About datamitsu](https://datamitsu.com/docs/about.md): Learn why datamitsu exists
- [Contributing](https://datamitsu.com/docs/contributing/index.md): How to contribute
- [No Description](https://datamitsu.com/docs/bare.md)
`

	title, tagline, entries, err := parseRootIndex(index)
	if err != nil {
		t.Fatalf("parseRootIndex() error = %v", err)
	}
	if title != "Datamitsu" {
		t.Errorf("title = %q, want %q", title, "Datamitsu")
	}
	if tagline != "Your toolchain deserves a home." {
		t.Errorf("tagline = %q", tagline)
	}
	want := []string{"about", "contributing/index", "bare"}
	if len(entries) != len(want) {
		t.Fatalf("parsed %d entries, want %d", len(entries), len(want))
	}
	for i, w := range want {
		if entries[i].rawSlug != w {
			t.Errorf("entry %d slug = %q, want %q", i, entries[i].rawSlug, w)
		}
	}
}

func TestParseRootIndexRejectsIncomplete(t *testing.T) {
	cases := map[string]string{
		"no heading": "> tagline\n\n- [A](https://x/docs/a.md)\n",
		"no tagline": "# Title\n\n- [A](https://x/docs/a.md)\n",
		"no entries": "# Title\n\n> tagline\n",
		"bad url":    "# Title\n\n> tagline\n\n- [A](https://x/blog/a.md)\n",
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := parseRootIndex(in); err == nil {
				t.Errorf("parseRootIndex() accepted an index with %s", name)
			}
		})
	}
}

// site builds a synthetic Docusaurus build output: a llms.txt listing pages and
// the matching cleaned Markdown files.
func site(t *testing.T, pages map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	docsDir := filepath.Join(dir, "build", "docs")

	var toc strings.Builder
	toc.WriteString("# Datamitsu\n\n> Your toolchain deserves a home.\n\n## Table of Contents\n\n")

	slugs := make([]string, 0, len(pages))
	for slug := range pages {
		slugs = append(slugs, slug)
	}
	// Deterministic order so the generated snapshot is comparable run to run.
	for i := 1; i < len(slugs); i++ {
		for j := i; j > 0 && slugs[j] < slugs[j-1]; j-- {
			slugs[j], slugs[j-1] = slugs[j-1], slugs[j]
		}
	}

	for _, slug := range slugs {
		body := pages[slug]
		title, _ := strings.CutPrefix(strings.SplitN(body, "\n", 2)[0], "# ")
		toc.WriteString("- [" + title + "](https://datamitsu.com/docs/" + slug + ".md): desc for " + slug + "\n")

		dst := filepath.Join(docsDir, filepath.FromSlash(slug)+".md")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
			t.Fatalf("write page: %v", err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "build", "llms.txt"), []byte(toc.String()), 0o644); err != nil {
		t.Fatalf("write llms.txt: %v", err)
	}
	return dir
}

func readManifest(t *testing.T, outDir string) llmsmanifest.Manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m llmsmanifest.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

// TestRunHarvest exercises the whole pipeline on a synthetic site: index
// collapse, alias recording, file layout, and a valid deterministic manifest.
func TestRunHarvest(t *testing.T) {
	websiteDir := site(t, map[string]string{
		"about":              "# About\n\n> About desc\n\nAbout body.\n",
		"contributing/index": "# Contributing\n\n> Contributing desc\n\nContributing body.\n",
		"contributing/rules": "# Rules\n\n> Rules desc\n\nRules body.\n",
	})
	outDir := filepath.Join(t.TempDir(), "embed")

	if err := run(websiteDir, outDir, "MIT"); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	m := readManifest(t, outDir)
	if err := m.Validate(); err != nil {
		t.Fatalf("harvested manifest is invalid: %v", err)
	}
	if m.PageCount != 3 {
		t.Errorf("pageCount = %d, want 3", m.PageCount)
	}
	if m.License != "MIT" {
		t.Errorf("license = %q, want MIT", m.License)
	}

	// The directory index is addressed as its directory, and remembers the
	// spelling it collapsed from.
	page, ok := m.Find("contributing")
	if !ok {
		t.Fatal("index page was not collapsed to the slug \"contributing\"")
	}
	if len(page.Aliases) != 1 || page.Aliases[0] != "contributing/index" {
		t.Errorf("aliases = %v, want [contributing/index]", page.Aliases)
	}
	if _, collapsed := m.Find("contributing/index"); collapsed {
		t.Error("the pre-collapse slug \"contributing/index\" is still a canonical page")
	}

	// A collapsed index and its siblings coexist: pages/contributing.md next to
	// pages/contributing/rules.md.
	for _, rel := range []string{"pages/about.md", "pages/contributing.md", "pages/contributing/rules.md"} {
		if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s: %v", rel, err)
		}
	}

	index, err := os.ReadFile(filepath.Join(outDir, "index.txt"))
	if err != nil {
		t.Fatalf("read index.txt: %v", err)
	}
	if !strings.Contains(string(index), "- [Contributing](contributing):") {
		t.Errorf("index.txt does not link the collapsed slug:\n%s", index)
	}
}

// TestRunHarvestIsDeterministic is the property the CI drift gate rests on:
// harvesting the same input twice must produce byte-identical output, or
// `git diff --exit-code` would flap on unrelated commits.
func TestRunHarvestIsDeterministic(t *testing.T) {
	websiteDir := site(t, map[string]string{
		"about":              "# About\n\n> About desc\n\nAbout body.\n",
		"guides/caching":     "# Caching\n\n> Caching desc\n\nCaching body.\n",
		"contributing/index": "# Contributing\n\n> Contributing desc\n\nContributing body.\n",
	})
	base := t.TempDir()
	first, second := filepath.Join(base, "a"), filepath.Join(base, "b")

	if err := run(websiteDir, first, "MIT"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := run(websiteDir, second, "MIT"); err != nil {
		t.Fatalf("second run: %v", err)
	}

	for _, rel := range []string{"manifest.json", "index.txt", "pages/about.md", "pages/contributing.md"} {
		a, err := os.ReadFile(filepath.Join(first, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		b, err := os.ReadFile(filepath.Join(second, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(a) != string(b) {
			t.Errorf("%s differs between identical harvests", rel)
		}
	}
}

// TestRunHarvestRemovesDeletedPages proves a page deleted upstream cannot
// linger in the snapshot and keep being served after a re-harvest.
func TestRunHarvestRemovesDeletedPages(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "embed")

	full := site(t, map[string]string{
		"about":   "# About\n\n> About desc\n\nAbout body.\n",
		"retired": "# Retired\n\n> Retired desc\n\nRetired body.\n",
	})
	if err := run(full, outDir, "MIT"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "pages", "retired.md")); err != nil {
		t.Fatalf("setup: retired page was not written: %v", err)
	}

	reduced := site(t, map[string]string{"about": "# About\n\n> About desc\n\nAbout body.\n"})
	if err := run(reduced, outDir, "MIT"); err != nil {
		t.Fatalf("second run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "pages", "retired.md")); !os.IsNotExist(err) {
		t.Error("a page removed upstream is still present in the snapshot")
	}
	if m := readManifest(t, outDir); m.PageCount != 1 {
		t.Errorf("pageCount = %d after removing a page, want 1", m.PageCount)
	}
}

// TestRunHarvestRejectsSlugCollision guards the one case where index-collapse
// could shadow a real page: a "contributing.md" page alongside a
// "contributing/index.md" directory index would both want the same slug.
func TestRunHarvestRejectsSlugCollision(t *testing.T) {
	websiteDir := site(t, map[string]string{
		"contributing":       "# Contributing Page\n\n> desc\n\nBody.\n",
		"contributing/index": "# Contributing Index\n\n> desc\n\nBody.\n",
	})

	err := run(websiteDir, filepath.Join(t.TempDir(), "embed"), "MIT")
	if err == nil {
		t.Fatal("run() accepted two pages claiming the slug \"contributing\"")
	}
	if !strings.Contains(err.Error(), "claimed by both") {
		t.Errorf("error = %v, want a slug-collision message", err)
	}
}

// TestRunHarvestRejectsUppercaseSlug guards the resolution invariant: the
// command lowercases its argument, so a mixed-case page could never be fetched.
func TestRunHarvestRejectsUppercaseSlug(t *testing.T) {
	websiteDir := site(t, map[string]string{"FAQ": "# FAQ\n\n> desc\n\nBody.\n"})

	err := run(websiteDir, filepath.Join(t.TempDir(), "embed"), "MIT")
	if err == nil {
		t.Fatal("run() accepted a non-lowercase slug")
	}
	if !strings.Contains(err.Error(), "non-lowercase") {
		t.Errorf("error = %v, want a lowercase-slug message", err)
	}
}

// TestRunHarvestRejectsTOCMismatch proves the bijection check catches a page
// present on disk but missing from the index, and vice versa.
func TestRunHarvestRejectsTOCMismatch(t *testing.T) {
	websiteDir := site(t, map[string]string{"about": "# About\n\n> desc\n\nBody.\n"})
	orphan := filepath.Join(websiteDir, "build", "docs", "orphan.md")
	if err := os.WriteFile(orphan, []byte("# Orphan\n\n> desc\n\nBody.\n"), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}

	err := run(websiteDir, filepath.Join(t.TempDir(), "embed"), "MIT")
	if err == nil {
		t.Fatal("run() accepted a page that llms.txt does not list")
	}
	if !strings.Contains(err.Error(), "disagree") {
		t.Errorf("error = %v, want a TOC/disk mismatch message", err)
	}
}

// TestGuardSingleVersionDocs proves the harvester refuses to run once the site
// grows versioned docs or extra locales, whose per-version and per-locale
// subtrees would silently break the one-slug-per-page mapping.
func TestGuardSingleVersionDocs(t *testing.T) {
	t.Run("plain site passes", func(t *testing.T) {
		if err := guardSingleVersionDocs(t.TempDir()); err != nil {
			t.Errorf("guardSingleVersionDocs() error = %v", err)
		}
	})

	t.Run("default locale passes", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "i18n", "en"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := guardSingleVersionDocs(dir); err != nil {
			t.Errorf("guardSingleVersionDocs() error = %v", err)
		}
	})

	t.Run("versioned docs fail", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "versioned_docs"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := guardSingleVersionDocs(dir); err == nil {
			t.Error("guardSingleVersionDocs() accepted versioned docs")
		}
	})

	t.Run("extra locale fails", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "i18n", "ja"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := guardSingleVersionDocs(dir); err == nil {
			t.Error("guardSingleVersionDocs() accepted an extra locale")
		}
	})
}

// TestRunReportsMissingBuild gives a maintainer who forgot the site build an
// actionable message instead of a bare file-not-found.
func TestRunReportsMissingBuild(t *testing.T) {
	err := run(t.TempDir(), filepath.Join(t.TempDir(), "embed"), "MIT")
	if err == nil {
		t.Fatal("run() succeeded without a website build")
	}
	if !strings.Contains(err.Error(), "pnpm --filter website build") {
		t.Errorf("error = %v, want it to name the missing build step", err)
	}
}
