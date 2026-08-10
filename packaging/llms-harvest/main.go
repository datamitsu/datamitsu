// Package main implements llms-harvest, the maintainer-only tool that turns the
// Docusaurus llms output into the documentation snapshot embedded in the
// datamitsu binary.
//
// It reads what docusaurus-plugin-llms already produced under website/build
// (llms.txt plus one cleaned Markdown file per page) and writes a deterministic
// snapshot into internal/llmsdocs/embed: the page bodies verbatim, a rewritten
// agent-facing root index, and a content-derived manifest.json.
//
// The tool is a standalone main, not a datamitsu subcommand: it is only ever run
// by maintainers via `task gen:llms-docs`, so keeping it out of the shipped
// binary avoids both the extra surface and a blackbox-test contract entry.
//
// Output is deterministic by construction — LF line endings, exactly one
// trailing newline, page order taken from the llms.txt table of contents, and a
// manifest with no timestamp and no git SHA. Re-running it on an unchanged docs
// tree reproduces the snapshot byte-for-byte, which is what lets CI enforce
// freshness with `git diff --exit-code`.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/llmsmanifest"
)

// tocEntryRE matches one llms.txt table-of-contents line, e.g.
// "- [About datamitsu](https://datamitsu.com/docs/about.md): Learn why ...".
// The description group is optional: a page without a frontmatter description
// yields a bare "- [Title](url)" line.
var tocEntryRE = regexp.MustCompile(`^- \[([^\]]+)\]\(([^)]+)\)(?::\s?(.*))?$`)

// docsURLMarker separates the site prefix from the docs-relative slug inside a
// page URL. The last occurrence wins so a site ever served under a /docs/ base
// path still resolves to the innermost slug.
const docsURLMarker = "/docs/"

// harvestedPage is one page after reading and normalization, before it is
// written to the snapshot.
type harvestedPage struct {
	slug     string // canonical, index-collapsed
	rawSlug  string // docs-relative path as emitted by the plugin
	url      string
	title    string
	desc     string
	body     []byte
	bodyHash string
}

func main() {
	website := flag.String("website", "website", "path to the Docusaurus site directory")
	out := flag.String("out", filepath.Join("internal", "llmsdocs", "embed"), "path to the embedded snapshot directory to (re)write")
	license := flag.String("license", "MIT", "SPDX license identifier recorded in the manifest")
	flag.Parse()

	if err := run(*website, *out, *license); err != nil {
		fmt.Fprintf(os.Stderr, "llms-harvest: %v\n", err)
		os.Exit(1)
	}
}

func run(websiteDir, outDir, license string) error {
	if err := guardSingleVersionDocs(websiteDir); err != nil {
		return err
	}

	buildDir := filepath.Join(websiteDir, "build")
	indexPath := filepath.Join(buildDir, "llms.txt")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("read %s (run `pnpm --filter website build` first): %w", indexPath, err)
	}

	siteTitle, siteTagline, entries, err := parseRootIndex(string(raw))
	if err != nil {
		return fmt.Errorf("parse %s: %w", indexPath, err)
	}

	docsDir := filepath.Join(buildDir, "docs")
	if err := assertTOCCoversDocs(docsDir, entries); err != nil {
		return err
	}

	pages, err := harvestPages(docsDir, entries)
	if err != nil {
		return err
	}

	return writeSnapshot(outDir, license, siteTitle, siteTagline, pages)
}

// guardSingleVersionDocs fails when the site has grown versioned docs or extra
// i18n locales. Both would multiply website/build/docs into per-version or
// per-locale subtrees, silently breaking the one-slug-per-page mapping this
// snapshot and its CLI argument space assume.
func guardSingleVersionDocs(websiteDir string) error {
	versioned := filepath.Join(websiteDir, "versioned_docs")
	if _, err := os.Stat(versioned); err == nil {
		return fmt.Errorf("%s exists: versioned docs are not supported by the llms snapshot "+
			"(see docs/plans/2026-07-23-datamitsu-llms-command.md)", versioned)
	}

	i18nDir := filepath.Join(websiteDir, "i18n")
	locales, err := os.ReadDir(i18nDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // no i18n directory at all: the expected single-locale case
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", i18nDir, err)
	}
	for _, l := range locales {
		if l.IsDir() && l.Name() != "en" {
			return fmt.Errorf("%s exists: extra i18n locales are not supported by the llms snapshot "+
				"(see docs/plans/2026-07-23-datamitsu-llms-command.md)", filepath.Join(i18nDir, l.Name()))
		}
	}
	return nil
}

// tocEntry is one parsed llms.txt link: the page URL and the docs-relative slug
// derived from it. Entry order is the snapshot's page order.
type tocEntry struct {
	url     string
	rawSlug string
}

// parseRootIndex extracts the site title, the tagline and the ordered table of
// contents from the plugin's llms.txt. Titles and descriptions are deliberately
// NOT taken from here: the plugin truncates long descriptions in the index, so
// the page files themselves are the authoritative source.
func parseRootIndex(content string) (title, tagline string, entries []tocEntry, err error) {
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case title == "" && strings.HasPrefix(line, "# "):
			title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		case tagline == "" && strings.HasPrefix(line, "> "):
			tagline = strings.TrimSpace(strings.TrimPrefix(line, "> "))
		case strings.HasPrefix(line, "- ["):
			m := tocEntryRE.FindStringSubmatch(line)
			if m == nil {
				return "", "", nil, fmt.Errorf("malformed table-of-contents line: %q", line)
			}
			url := m[2]
			slug, ok := slugFromURL(url)
			if !ok {
				return "", "", nil, fmt.Errorf("page URL %q does not contain %q", url, docsURLMarker)
			}
			entries = append(entries, tocEntry{url: url, rawSlug: slug})
		}
	}

	switch {
	case title == "":
		return "", "", nil, fmt.Errorf("no %q heading found", "# ")
	case tagline == "":
		return "", "", nil, fmt.Errorf("no %q tagline found", "> ")
	case len(entries) == 0:
		return "", "", nil, errors.New("no table-of-contents entries found")
	}
	return title, tagline, entries, nil
}

// slugFromURL turns a published page URL into its docs-relative slug, e.g.
// "https://datamitsu.com/docs/guides/architecture/caching.md" ->
// "guides/architecture/caching".
func slugFromURL(url string) (string, bool) {
	i := strings.LastIndex(url, docsURLMarker)
	if i < 0 {
		return "", false
	}
	slug := url[i+len(docsURLMarker):]
	slug = strings.TrimSuffix(slug, ".md")
	if slug == "" {
		return "", false
	}
	return slug, true
}

// assertTOCCoversDocs enforces a bijection between the table of contents and the
// Markdown files on disk. A page present in only one of the two would otherwise
// be silently dropped from (or dangle in) the snapshot.
func assertTOCCoversDocs(docsDir string, entries []tocEntry) error {
	onDisk := make(map[string]bool)
	err := filepath.WalkDir(docsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(p) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(docsDir, p)
		if err != nil {
			return fmt.Errorf("relativize %s: %w", p, err)
		}
		onDisk[strings.TrimSuffix(filepath.ToSlash(rel), ".md")] = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", docsDir, err)
	}

	inTOC := make(map[string]bool, len(entries))
	for _, e := range entries {
		inTOC[e.rawSlug] = true
	}

	var missingFile, missingTOC []string
	for slug := range inTOC {
		if !onDisk[slug] {
			missingFile = append(missingFile, slug)
		}
	}
	for slug := range onDisk {
		if !inTOC[slug] {
			missingTOC = append(missingTOC, slug)
		}
	}
	sort.Strings(missingFile)
	sort.Strings(missingTOC)

	if len(missingFile) > 0 || len(missingTOC) > 0 {
		return fmt.Errorf("llms.txt and %s disagree: listed but no file %v; file but not listed %v",
			docsDir, missingFile, missingTOC)
	}
	return nil
}

// harvestPages reads, normalizes and describes every page in table-of-contents
// order, failing on any slug that would break the CLI argument space.
func harvestPages(docsDir string, entries []tocEntry) ([]harvestedPage, error) {
	pages := make([]harvestedPage, 0, len(entries))
	claimed := make(map[string]string, len(entries))

	for _, e := range entries {
		src := filepath.Join(docsDir, filepath.FromSlash(e.rawSlug)+".md")
		raw, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("read page %s: %w", src, err)
		}

		body := normalizeBytes(raw)
		title, desc, body, err := splitHeader(body)
		if err != nil {
			return nil, fmt.Errorf("page %s: %w", src, err)
		}

		slug := llmsmanifest.CanonicalSlug(e.rawSlug)
		if slug != strings.ToLower(slug) {
			return nil, fmt.Errorf("page %s has non-lowercase slug %q: `datamitsu llms` lowercases its "+
				"argument, so this page could never be addressed", src, slug)
		}
		if prev, dup := claimed[slug]; dup {
			return nil, fmt.Errorf("canonical slug %q claimed by both %q and %q "+
				"(a page and a directory index cannot share a slug)", slug, prev, e.rawSlug)
		}
		claimed[slug] = e.rawSlug

		pages = append(pages, harvestedPage{
			slug:     slug,
			rawSlug:  e.rawSlug,
			url:      e.url,
			title:    title,
			desc:     desc,
			body:     body,
			bodyHash: hashutil.XXH3Hex(body),
		})
	}
	return pages, nil
}

// normalizeBytes pins the byte-level form of harvested content so the committed
// snapshot cannot differ between maintainer machines: LF line endings and
// exactly one trailing newline. Without this, a CRLF checkout or an editor's
// final-newline setting would flap the CI drift diff.
func normalizeBytes(b []byte) []byte {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimRight(s, "\n") + "\n"
	return []byte(s)
}

// splitHeader reads the "# Title" / "> Description" preamble the plugin puts at
// the top of every page, returns the parsed title and description, and rebuilds
// a clean body.
//
// Two artifacts of the plugin's output are removed:
//   - An empty "> " blockquote (a page whose frontmatter has no description). It
//     is dropped and reported as an empty description rather than guessed at from
//     the first paragraph, which would make the manifest non-deterministic and
//     let it drift from the body.
//   - The page's own "# Title" heading, which the plugin re-emits immediately
//     below the preamble it just added. Left in, every page would open with its
//     title twice — noise in the text an agent reads.
func splitHeader(body []byte) (title, desc string, out []byte, err error) {
	lines := strings.Split(string(body), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "# ") {
		return "", "", nil, errors.New(`expected a "# Title" first line`)
	}
	title = strings.TrimSpace(strings.TrimPrefix(lines[0], "# "))
	if title == "" {
		return "", "", nil, errors.New(`empty "# Title" heading`)
	}

	quote := -1
	for i := 1; i < len(lines) && i < 4; i++ {
		if strings.HasPrefix(lines[i], ">") {
			quote = i
			break
		}
	}
	if quote < 0 {
		return "", "", nil, errors.New(`expected a "> Description" line near the top`)
	}
	desc = strings.TrimSpace(strings.TrimPrefix(lines[quote], ">"))

	// Everything after the plugin's blockquote is the page body. Skip the blank
	// line(s) that follow, then drop a leading "# Title" that duplicates the
	// heading we keep above.
	rest := lines[quote+1:]
	rest = dropLeadingBlanks(rest)
	if len(rest) > 0 && rest[0] == "# "+title {
		rest = dropLeadingBlanks(rest[1:])
	}

	// Rebuild: the title, the description blockquote (only when non-empty), a
	// blank separator, then the de-duplicated body.
	rebuilt := []string{"# " + title}
	if desc != "" {
		rebuilt = append(rebuilt, "", "> "+desc)
	}
	rebuilt = append(rebuilt, "")
	rebuilt = append(rebuilt, rest...)
	return title, desc, normalizeBytes([]byte(strings.Join(rebuilt, "\n"))), nil
}

// dropLeadingBlanks returns lines with any leading whitespace-only entries
// removed.
func dropLeadingBlanks(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	return lines
}

// writeSnapshot rewrites the whole embed directory: page bodies, the rewritten
// root index and the manifest. The pages tree is removed first so a page deleted
// upstream cannot linger in the snapshot and keep being served.
func writeSnapshot(outDir, license, siteTitle, siteTagline string, pages []harvestedPage) error {
	pagesDir := filepath.Join(outDir, "pages")
	if err := os.RemoveAll(pagesDir); err != nil {
		return fmt.Errorf("clear %s: %w", pagesDir, err)
	}
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", pagesDir, err)
	}

	manifest := llmsmanifest.Manifest{
		SchemaVersion: llmsmanifest.SchemaVersion,
		Generator:     llmsmanifest.Generator,
		License:       license,
		SiteTitle:     siteTitle,
		SiteTagline:   siteTagline,
		PageCount:     len(pages),
		Pages:         make([]llmsmanifest.Page, 0, len(pages)),
	}

	for _, p := range pages {
		// The canonical slug is also the file stem, so a page is always at
		// pages/<slug>.md and needs no path field in the manifest.
		dst := filepath.Join(pagesDir, filepath.FromSlash(p.slug)+".md")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
		}
		if err := os.WriteFile(dst, p.body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}

		aliases := []string{}
		if p.slug != p.rawSlug {
			aliases = append(aliases, p.rawSlug)
		}
		manifest.Pages = append(manifest.Pages, llmsmanifest.Page{
			Slug:        p.slug,
			Aliases:     aliases,
			URL:         p.url,
			Title:       p.title,
			Description: p.desc,
			Bytes:       len(p.body),
			ContentHash: p.bodyHash,
		})
	}

	manifest.PageSetHash = llmsmanifest.ComputePageSetHash(manifest.Pages)
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("harvested snapshot is invalid: %w", err)
	}

	indexPath := filepath.Join(outDir, "index.txt")
	if err := os.WriteFile(indexPath, renderIndex(manifest), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", indexPath, err)
	}

	encoded, err := encodeManifest(manifest)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(outDir, "manifest.json")
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}

	fmt.Printf("llms-harvest: wrote %d pages to %s (pageSetHash %s)\n",
		manifest.PageCount, outDir, manifest.PageSetHash)
	return nil
}

// renderIndex writes the agent-facing root index. It keeps the llmstxt.org
// shape (H1, tagline blockquote, "## Table of Contents") but rewrites every
// link target from a fetchable URL to the exact `datamitsu llms <slug>`
// argument, and prepends a short header telling an agent how to make the next
// call. That rewrite is the whole point of the command: the agent picks its
// next page without reverse-engineering a URL.
func renderIndex(m llmsmanifest.Manifest) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n> %s\n\n", m.SiteTitle, m.SiteTagline)
	b.WriteString("Documentation embedded in this binary, matching its exact version. No network access.\n\n")
	b.WriteString("Fetch any page with: `datamitsu llms <slug>`.\n")
	b.WriteString("Machine-readable list:  `datamitsu llms --list --json`.\n")
	b.WriteString("Standard llms.txt form: `datamitsu llms --web`.\n\n")
	b.WriteString("## Table of Contents\n\n")

	for _, p := range m.Pages {
		if p.Description == "" {
			fmt.Fprintf(&b, "- [%s](%s)\n", p.Title, p.Slug)
			continue
		}
		fmt.Fprintf(&b, "- [%s](%s): %s\n", p.Title, p.Slug, p.Description)
	}
	return normalizeBytes([]byte(b.String()))
}

// encodeManifest serializes the manifest deterministically: struct field order
// (not map iteration), a fixed indent, HTML escaping off so descriptions read
// literally, and a single trailing newline.
func encodeManifest(m llmsmanifest.Manifest) ([]byte, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	return normalizeBytes([]byte(buf.String())), nil
}
