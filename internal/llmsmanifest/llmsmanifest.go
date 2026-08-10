// Package llmsmanifest defines the on-disk schema of the embedded documentation
// snapshot (manifest.json).
//
// It is deliberately a standalone package with no embedded assets of its own:
// both the maintainer-only harvester that writes the snapshot
// (packaging/llms-harvest) and the llmsdocs package that embeds and serves it
// depend on this one schema. A shared type keeps writer and reader from drifting
// without introducing a build cycle — llmsdocs cannot be imported by the
// harvester, because its //go:embed directive requires the very snapshot the
// harvester has not produced yet.
package llmsmanifest

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/hashutil"
)

// SchemaVersion is the current manifest format version. Bump it on any
// breaking change to the Manifest/Page field set.
const SchemaVersion = 1

// Generator identifies the tool and format revision that produced a snapshot.
const Generator = "llms-harvest/1"

// IndexLeaf is the Docusaurus filename stem that addresses a directory's own
// page. Such a page is exposed under its directory's slug (index-collapse), so
// no canonical slug ever ends in "/index".
const IndexLeaf = "index"

// Manifest is the deterministic, content-derived description of the embedded
// documentation snapshot. Every field is derived from the harvested content:
// there is no timestamp and no git SHA, so regenerating an unchanged docs tree
// reproduces the file byte-for-byte and the CI drift gate cannot flap.
type Manifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	Generator     string `json:"generator"`
	License       string `json:"license"`
	SiteTitle     string `json:"siteTitle"`
	SiteTagline   string `json:"siteTagline"`
	PageCount     int    `json:"pageCount"`
	PageSetHash   string `json:"pageSetHash"`
	Pages         []Page `json:"pages"`
}

// Page is one documentation page in the snapshot. Slug is the canonical CLI
// argument (`datamitsu llms <slug>`) and also the snapshot-relative file stem,
// so the body always lives at "pages/<Slug>.md".
type Page struct {
	Slug        string   `json:"slug"`
	Aliases     []string `json:"aliases"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Bytes       int      `json:"bytes"`
	ContentHash string   `json:"contentHash"`
}

// CanonicalSlug applies index-collapse: a "<dir>/index" page is addressed as
// "<dir>". The bare root "index" has no parent directory to collapse into and
// is returned unchanged. After this transform no canonical slug ends in
// "/index", which removes the only basename collision in the docs tree.
func CanonicalSlug(raw string) string {
	const suffix = "/" + IndexLeaf
	if dir, ok := strings.CutSuffix(raw, suffix); ok && dir != "" {
		return dir
	}
	return raw
}

// ComputePageSetHash returns the order-independent XXH3-128 identity of a page
// set: the hash of each page's slug and content hash, sorted by slug. Callers
// compare it against Manifest.PageSetHash to detect a hand-edited manifest or a
// partial harvest.
//
// XXH3 (not SHA-256) is mandated here by the repo hashing policy: this is an
// internal fingerprint that is never compared against content from the network.
func ComputePageSetHash(pages []Page) string {
	sorted := make([]Page, len(pages))
	copy(sorted, pages)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })

	parts := make([][]byte, 0, len(sorted)*2)
	for _, p := range sorted {
		parts = append(parts, []byte(p.Slug), []byte(p.ContentHash))
	}
	return hashutil.XXH3Multi(parts...)
}

// Find returns the page with the given canonical slug.
func (m *Manifest) Find(slug string) (Page, bool) {
	for _, p := range m.Pages {
		if p.Slug == slug {
			return p, true
		}
	}
	return Page{}, false
}

// Validate checks the structural invariants every snapshot must satisfy,
// independent of the files on disk: a supported schema version, a page count
// that matches the page list, unique lowercase slugs that never end in
// "/index", and a page-set hash that matches the listed pages.
func (m *Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("manifest schemaVersion = %d, want %d", m.SchemaVersion, SchemaVersion)
	}
	if len(m.Pages) == 0 {
		return errors.New("manifest has no pages")
	}
	if m.PageCount != len(m.Pages) {
		return fmt.Errorf("manifest pageCount = %d, but pages list has %d entries", m.PageCount, len(m.Pages))
	}

	seen := make(map[string]string, len(m.Pages))
	for _, p := range m.Pages {
		if err := validateSlug(p.Slug); err != nil {
			return err
		}
		if prev, dup := seen[p.Slug]; dup {
			return fmt.Errorf("duplicate slug %q (already used by %q)", p.Slug, prev)
		}
		seen[p.Slug] = p.Title

		for _, alias := range p.Aliases {
			if err := validateAlias(alias, p.Slug); err != nil {
				return err
			}
		}
	}

	if got := ComputePageSetHash(m.Pages); got != m.PageSetHash {
		return fmt.Errorf("pageSetHash mismatch: manifest has %q, pages compute to %q", m.PageSetHash, got)
	}
	return nil
}

// validateSlug enforces the canonical-slug rules that Resolve depends on:
// non-empty, lowercase (Resolve lowercases its argument before matching, so a
// mixed-case slug could never be reached), and index-collapsed.
func validateSlug(slug string) error {
	switch {
	case slug == "":
		return errors.New("empty slug")
	case slug != strings.ToLower(slug):
		return fmt.Errorf("slug %q is not lowercase", slug)
	case strings.HasSuffix(slug, "/"+IndexLeaf):
		return fmt.Errorf("slug %q is not index-collapsed", slug)
	case strings.HasPrefix(slug, "/"), strings.HasSuffix(slug, "/"):
		return fmt.Errorf("slug %q must not start or end with %q", slug, "/")
	}
	return nil
}

// validateAlias enforces that an alias is lowercase and distinct from the
// canonical slug it points at.
func validateAlias(alias, slug string) error {
	switch {
	case alias == "":
		return fmt.Errorf("empty alias for slug %q", slug)
	case alias != strings.ToLower(alias):
		return fmt.Errorf("alias %q of slug %q is not lowercase", alias, slug)
	case alias == slug:
		return fmt.Errorf("alias %q duplicates its own slug", alias)
	}
	return nil
}
