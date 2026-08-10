package llmsdocs

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
)

// Resolution failures. Callers distinguish them to pick an exit code and to
// decide whether suggesting alternatives makes sense.
var (
	// ErrUnknownPage means nothing in the snapshot matches the argument.
	ErrUnknownPage = errors.New("unknown page")
	// ErrAmbiguous means a bare basename matches more than one page, so the
	// caller must repeat the request with a full slug.
	ErrAmbiguous = errors.New("ambiguous page")
)

// maxSuggestions caps how many "did you mean" candidates are offered, keeping
// the error line readable and its golden stable.
const maxSuggestions = 3

// Resolve maps a user-supplied page argument onto a canonical slug.
//
// The pipeline is deliberately forgiving about the shapes a page identity turns
// up in — a bare slug, a slug with .md, a docs-relative path, or a full URL
// copied from the website — because an agent picking a page out of prose should
// not have to know which form it found. The first rule that matches wins:
//
//  1. normalize: lowercase, trim space and a trailing "/", unwrap a URL, drop a
//     leading "docs/" and a trailing ".md"
//  2. alias hit: "<dir>/index" addresses the collapsed "<dir>"
//  3. exact canonical slug
//  4. basename shorthand: a unique page whose last segment matches
//  5. otherwise ErrAmbiguous (several basename matches) or ErrUnknownPage
func (d *Docs) Resolve(arg string) (string, error) {
	s := normalizeArg(arg)
	if s == "" {
		return "", fmt.Errorf("%w: %q", ErrUnknownPage, arg)
	}

	if slug, ok := d.byAlias[s]; ok {
		return slug, nil
	}
	if _, ok := d.bySlug[s]; ok {
		return s, nil
	}

	if !strings.Contains(s, "/") {
		switch matches := d.byBase[s]; len(matches) {
		case 1:
			return matches[0], nil
		case 0:
			// fall through to the unknown-page path
		default:
			return "", fmt.Errorf("%w: %q matches %s", ErrAmbiguous, arg, strings.Join(matches, ", "))
		}
	}

	return "", fmt.Errorf("%w: %q", ErrUnknownPage, arg)
}

// Suggest returns the canonical slugs closest to a failed argument, nearest
// first and ties broken by slug so the rendered error is byte-stable. It
// compares against both full slugs and bare base names, so a typo in either form
// finds its page. An empty result means nothing was close enough to be worth
// printing.
func (d *Docs) Suggest(arg string) []string {
	s := normalizeArg(arg)
	if s == "" {
		return nil
	}

	// A short argument must match tightly; a long one can absorb more typos.
	limit := max(len(s)*2/5, 1)
	limit = min(limit, 3)

	type candidate struct {
		slug string
		dist int
	}
	best := make(map[string]int)
	for _, slug := range d.slugs {
		dist := editDistance(s, slug)
		if base := path.Base(slug); base != slug {
			dist = min(dist, editDistance(s, base))
		}
		if dist > limit {
			continue
		}
		if prev, seen := best[slug]; !seen || dist < prev {
			best[slug] = dist
		}
	}

	out := make([]candidate, 0, len(best))
	for slug, dist := range best {
		out = append(out, candidate{slug: slug, dist: dist})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].dist != out[j].dist {
			return out[i].dist < out[j].dist
		}
		return out[i].slug < out[j].slug
	})

	slugs := make([]string, 0, min(len(out), maxSuggestions))
	for i := 0; i < len(out) && i < maxSuggestions; i++ {
		slugs = append(slugs, out[i].slug)
	}
	return slugs
}

// normalizeArg reduces every accepted spelling of a page identity to the bare
// docs-relative slug used as the lookup key.
func normalizeArg(arg string) string {
	s := strings.ToLower(strings.TrimSpace(arg))
	if s == "" {
		return ""
	}

	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		if u, err := url.Parse(s); err == nil && u.Path != "" {
			s = u.Path
		}
	}

	// Keep the innermost /docs/ segment so a site served under a /docs/ base
	// path still reduces to the page slug.
	const marker = "/docs/"
	if i := strings.LastIndex(s, marker); i >= 0 {
		s = s[i+len(marker):]
	}
	s = strings.TrimPrefix(s, "docs/")
	s = strings.TrimSuffix(s, ".md")
	return strings.Trim(s, "/")
}

// editDistance returns the Damerau–Levenshtein distance (optimal string
// alignment variant) between a and b, counting a transposition of adjacent
// characters as one edit so common typos rank as near misses.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}

	prev2 := make([]int, len(br)+1)
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				curr[j] = min(curr[j], prev2[j-2]+1)
			}
		}
		prev2, prev, curr = prev, curr, prev2
	}
	return prev[len(br)]
}
