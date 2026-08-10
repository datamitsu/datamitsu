// Package llmsdocs serves the documentation snapshot compiled into the binary.
//
// The snapshot under embed/ is produced by packaging/llms-harvest from the
// Docusaurus llms output and committed to git, so a plain `go build` needs no
// website toolchain and the docs a binary serves always match the source tree it
// was built from. Serving is pure data access: no network, no cache, no git.
package llmsdocs

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/datamitsu/datamitsu/internal/llmsmanifest"
)

// snapshot holds the harvested docs: pages/<slug>.md, the rewritten root index
// and the provenance manifest.
//
//go:embed embed
var snapshot embed.FS

// Snapshot paths inside the embedded filesystem.
const (
	rootDir      = "embed"
	pagesDir     = rootDir + "/pages"
	indexPath    = rootDir + "/index.txt"
	manifestPath = rootDir + "/manifest.json"
)

// Page is a documentation page together with its rendered body.
type Page struct {
	Slug        string   `json:"slug"`
	Aliases     []string `json:"aliases"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Bytes       int      `json:"bytes"`
	ContentHash string   `json:"contentHash"`
	Body        string   `json:"body,omitempty"`
}

// Docs is the loaded snapshot: the manifest plus the lookup tables Resolve
// needs. Construct it with Load.
type Docs struct {
	manifest llmsmanifest.Manifest
	index    string
	bySlug   map[string]llmsmanifest.Page
	byAlias  map[string]string
	byBase   map[string][]string
	slugs    []string
}

// load parses and validates the embedded snapshot exactly once per process. The
// snapshot is compiled in and covered by TestManifestMatchesEmbed, so a failure
// here means a corrupt build rather than a runtime condition.
var load = sync.OnceValues(func() (*Docs, error) {
	raw, err := snapshot.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", manifestPath, err)
	}

	var m llmsmanifest.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse embedded %s: %w", manifestPath, err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("embedded %s is invalid: %w", manifestPath, err)
	}

	index, err := snapshot.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", indexPath, err)
	}

	d := &Docs{
		manifest: m,
		index:    string(index),
		bySlug:   make(map[string]llmsmanifest.Page, len(m.Pages)),
		byAlias:  make(map[string]string),
		byBase:   make(map[string][]string),
		slugs:    make([]string, 0, len(m.Pages)),
	}
	for _, p := range m.Pages {
		d.bySlug[p.Slug] = p
		d.slugs = append(d.slugs, p.Slug)
		for _, alias := range p.Aliases {
			d.byAlias[alias] = p.Slug
		}
		base := path.Base(p.Slug)
		d.byBase[base] = append(d.byBase[base], p.Slug)
	}
	sort.Strings(d.slugs)
	for base := range d.byBase {
		sort.Strings(d.byBase[base])
	}
	return d, nil
})

// Load returns the parsed snapshot, reusing the result across calls.
func Load() (*Docs, error) {
	return load()
}

// Index returns the agent-facing root index: the llmstxt.org shape with every
// link target rewritten to the `datamitsu llms <slug>` argument that fetches it.
func (d *Docs) Index() string {
	return d.index
}

// List returns every canonical slug, sorted.
func (d *Docs) List() []string {
	out := make([]string, len(d.slugs))
	copy(out, d.slugs)
	return out
}

// Manifest returns the provenance manifest of the embedded snapshot.
func (d *Docs) Manifest() llmsmanifest.Manifest {
	return d.manifest
}

// Page returns the page for a canonical slug, with its body read from the
// embedded filesystem. Callers pass a slug from Resolve, not raw user input.
func (d *Docs) Page(slug string) (Page, error) {
	meta, ok := d.bySlug[slug]
	if !ok {
		return Page{}, fmt.Errorf("%w: %s", ErrUnknownPage, slug)
	}

	body, err := snapshot.ReadFile(pagesDir + "/" + slug + ".md")
	if err != nil {
		return Page{}, fmt.Errorf("read embedded page %q: %w", slug, err)
	}

	return Page{
		Slug:        meta.Slug,
		Aliases:     meta.Aliases,
		URL:         meta.URL,
		Title:       meta.Title,
		Description: meta.Description,
		Bytes:       meta.Bytes,
		ContentHash: meta.ContentHash,
		Body:        string(body),
	}, nil
}

// Pages returns every page's metadata in manifest order, without bodies.
func (d *Docs) Pages() []Page {
	out := make([]Page, 0, len(d.manifest.Pages))
	for _, p := range d.manifest.Pages {
		out = append(out, Page{
			Slug:        p.Slug,
			Aliases:     p.Aliases,
			URL:         p.URL,
			Title:       p.Title,
			Description: p.Description,
			Bytes:       p.Bytes,
			ContentHash: p.ContentHash,
		})
	}
	return out
}

// WebIndex renders the standard llmstxt.org index, with the real fetchable page
// URLs instead of CLI slugs. Index() is deliberately non-standard so an agent
// can act on it directly; this is the interoperable form for consumers that
// expect resolvable links.
func (d *Docs) WebIndex() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n> %s\n\n", d.manifest.SiteTitle, d.manifest.SiteTagline)
	b.WriteString("This file contains links to documentation sections following the llmstxt.org standard.\n\n")
	b.WriteString("## Table of Contents\n\n")
	for _, p := range d.manifest.Pages {
		if p.Description == "" {
			fmt.Fprintf(&b, "- [%s](%s)\n", p.Title, p.URL)
			continue
		}
		fmt.Fprintf(&b, "- [%s](%s): %s\n", p.Title, p.URL, p.Description)
	}
	return b.String()
}

// PageFiles returns the snapshot-relative paths of every embedded page file,
// sorted. Tests use it to assert a bijection between files and manifest entries.
func PageFiles() ([]string, error) {
	var out []string
	err := fs.WalkDir(snapshot, pagesDir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(p, pagesDir+"/"), ".md"))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk embedded pages: %w", err)
	}
	sort.Strings(out)
	return out, nil
}
