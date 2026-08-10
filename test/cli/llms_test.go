package cli_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// pageCountRE masks the number of embedded pages in diagnostics. The count is a
// property of the documentation, not of the CLI contract, so leaving it in a
// golden would make every added docs page fail an unrelated CLI test.
var pageCountRE = regexp.MustCompile(`all \d+ pages`)

// Goldens in this file deliberately cover only the parts of `llms` that are
// contract rather than content: the help text and the diagnostics. The page
// bodies, the index and the JSON payloads are derived from website/docs, so
// freezing their bytes here would mean every documentation edit breaks the CLI
// suite. Those surfaces are asserted structurally instead, and their byte-level
// integrity is covered by internal/llmsdocs (manifest ↔ embed) and by the CI
// drift gate (embed ↔ website/docs).

func TestLlmsHelpGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{}, "llms", "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`llms --help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	clitest.AssertGolden(t, "llms_help", norm.Apply(res.Stdout))
}

// TestLlmsRootIndex proves the no-argument output is a usable menu: it names
// every page under a link target that is the literal argument fetching it, so an
// agent can pick its next call straight out of this text.
func TestLlmsRootIndex(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "llms")
	if res.ExitCode != 0 {
		t.Fatalf("`llms` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`llms` wrote to stderr:\n%s", res.Stderr)
	}

	if !strings.HasPrefix(res.Stdout, "# ") {
		t.Error("index does not start with a markdown heading")
	}
	for _, want := range []string{"## Table of Contents", "datamitsu llms <slug>"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("index does not contain %q", want)
		}
	}

	// Every listed slug must be exactly what `llms <slug>` accepts.
	slugs := indexSlugs(res.Stdout)
	if len(slugs) == 0 {
		t.Fatal("index lists no pages")
	}
	listed := lines(t, clitest.Run(t, clitest.RunOptions{}, "llms", "--list").Stdout)
	if len(slugs) != len(listed) {
		t.Errorf("index lists %d pages, --list reports %d", len(slugs), len(listed))
	}
	for _, slug := range listed {
		if !strings.Contains(res.Stdout, "]("+slug+")") {
			t.Errorf("page %q is missing from the index", slug)
		}
	}
}

// TestLlmsPage covers the core contract: a page goes to stdout, verbatim
// markdown, with nothing on stderr, so the output can be piped into a model.
func TestLlmsPage(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "llms", "about")
	if res.ExitCode != 0 {
		t.Fatalf("`llms about` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`llms about` wrote to stderr:\n%s", res.Stderr)
	}
	if !strings.HasPrefix(res.Stdout, "# ") {
		t.Errorf("page does not start with a markdown heading:\n%.80s", res.Stdout)
	}
	if len(res.Stdout) < 100 {
		t.Errorf("page is suspiciously short (%d bytes)", len(res.Stdout))
	}
}

// TestLlmsShorthandAndAlias proves the two conveniences that make the command
// forgiving about how an agent found a page name: a unique page may be named by
// its last segment, and a section's own page answers to the section.
func TestLlmsShorthandAndAlias(t *testing.T) {
	full := clitest.Run(t, clitest.RunOptions{}, "llms", "contributing/brand-guidelines")
	if full.ExitCode != 0 {
		t.Fatalf("`llms contributing/brand-guidelines` exit = %d\nstderr:\n%s", full.ExitCode, full.Stderr)
	}

	cases := map[string][]string{
		"basename shorthand": {"llms", "brand-guidelines"},
		"trailing .md":       {"llms", "contributing/brand-guidelines.md"},
		"website URL":        {"llms", "https://datamitsu.com/docs/contributing/brand-guidelines.md"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{}, args...)
			if res.ExitCode != 0 {
				t.Fatalf("exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
			}
			if res.Stdout != full.Stdout {
				t.Errorf("%v resolved to a different page than the full slug", args)
			}
		})
	}

	// A directory index is addressed by its directory, and by its pre-collapse
	// spelling, and both return the same page.
	canonical := clitest.Run(t, clitest.RunOptions{}, "llms", "contributing")
	alias := clitest.Run(t, clitest.RunOptions{}, "llms", "contributing/index")
	if canonical.ExitCode != 0 || alias.ExitCode != 0 {
		t.Fatalf("index page exits: canonical=%d alias=%d", canonical.ExitCode, alias.ExitCode)
	}
	if canonical.Stdout != alias.Stdout {
		t.Error("`llms contributing` and `llms contributing/index` returned different pages")
	}
	if canonical.Stdout == full.Stdout {
		t.Error("`llms contributing` returned the brand-guidelines page")
	}
}

// TestLlmsUnknownPageGolden freezes the failure an agent is most likely to hit.
// Exit 3 distinguishes "that page does not exist" from a usage error, stdout
// stays empty so a failed fetch cannot pollute a pipe, and the message points at
// the nearest match plus the way to list everything.
func TestLlmsUnknownPageGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	//nolint:misspell // the misspelling is the input under test
	const typo = "instalation"

	res := clitest.Run(t, clitest.RunOptions{}, "llms", typo)
	if res.ExitCode != 3 {
		t.Errorf("`llms %s` exit = %d, want 3", typo, res.ExitCode)
	}
	if res.Stdout != "" {
		t.Errorf("unknown page wrote to stdout:\n%s", res.Stdout)
	}
	clitest.AssertGolden(t, "llms_unknown_page", pageCountRE.ReplaceAllString(norm.Apply(res.Stderr), "all <N> pages"))
}

// TestLlmsUsageErrorsGolden freezes exit 2, the "you called this wrong" signal,
// for each way a caller can misuse the flags.
func TestLlmsUsageErrorsGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	cases := []struct {
		name string
		args []string
	}{
		{"too many args", []string{"llms", "about", "intro"}},
		{"web with page", []string{"llms", "--web", "about"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{}, tc.args...)
			if res.ExitCode != 2 {
				t.Errorf("`%s` exit = %d, want 2", strings.Join(tc.args, " "), res.ExitCode)
			}
			if res.Stdout != "" {
				t.Errorf("usage error wrote to stdout:\n%s", res.Stdout)
			}
			clitest.AssertGolden(t, "llms_usage_"+strings.ReplaceAll(tc.name, " ", "_"), norm.Apply(res.Stderr))
		})
	}
}

// TestLlmsListJSON checks the machine-readable page list an agent would use to
// choose a page by title or description rather than by guessing a slug.
func TestLlmsListJSON(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "llms", "--list", "--json")
	if res.ExitCode != 0 {
		t.Fatalf("`llms --list --json` exit = %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	var pages []struct {
		Slug        string `json:"slug"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		ContentHash string `json:"contentHash"`
		Body        string `json:"body"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &pages); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("no pages in JSON output")
	}

	plain := lines(t, clitest.Run(t, clitest.RunOptions{}, "llms", "--list").Stdout)
	if len(pages) != len(plain) {
		t.Errorf("--list --json has %d pages, --list has %d", len(pages), len(plain))
	}
	for _, p := range pages {
		if p.Slug == "" || p.Title == "" || p.ContentHash == "" {
			t.Errorf("incomplete page entry: %+v", p)
		}
		if p.Body != "" {
			t.Errorf("page %q carries a body in the list; bodies belong to single-page output", p.Slug)
		}
	}
}

// TestLlmsPageJSON checks the single-page JSON form, which unlike the list does
// carry the body.
func TestLlmsPageJSON(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "llms", "about", "--json")
	if res.ExitCode != 0 {
		t.Fatalf("`llms about --json` exit = %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	var page struct {
		Slug string `json:"slug"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &page); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if page.Slug != "about" {
		t.Errorf("slug = %q, want %q", page.Slug, "about")
	}
	if !strings.HasPrefix(page.Body, "# ") {
		t.Error("JSON body is not the markdown page")
	}
}

// TestLlmsProvenanceJSON checks the introspection surface that lets a caller
// verify which documentation snapshot a binary carries.
func TestLlmsProvenanceJSON(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "llms", "--json")
	if res.ExitCode != 0 {
		t.Fatalf("`llms --json` exit = %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	var prov struct {
		BinaryVersion string `json:"binaryVersion"`
		SchemaVersion int    `json:"schemaVersion"`
		License       string `json:"license"`
		PageCount     int    `json:"pageCount"`
		PageSetHash   string `json:"pageSetHash"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &prov); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if prov.BinaryVersion == "" {
		t.Error("binaryVersion is empty")
	}
	if prov.SchemaVersion != 1 {
		t.Errorf("schemaVersion = %d, want 1", prov.SchemaVersion)
	}
	if prov.License != "MIT" {
		t.Errorf("license = %q, want MIT", prov.License)
	}
	if len(prov.PageSetHash) != 32 {
		t.Errorf("pageSetHash = %q, want a 32-char XXH3-128 hex digest", prov.PageSetHash)
	}
	if want := len(lines(t, clitest.Run(t, clitest.RunOptions{}, "llms", "--list").Stdout)); prov.PageCount != want {
		t.Errorf("pageCount = %d, but --list printed %d slugs", prov.PageCount, want)
	}
}

// TestLlmsWeb checks the interoperable index: the same pages, but under their
// real website URLs, for consumers that expect a standard llms.txt.
func TestLlmsWeb(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "llms", "--web")
	if res.ExitCode != 0 {
		t.Fatalf("`llms --web` exit = %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "## Table of Contents") {
		t.Error("web index has no table of contents")
	}
	if !strings.Contains(res.Stdout, "](https://") {
		t.Error("web index has no absolute URLs")
	}
	if strings.Contains(res.Stdout, "datamitsu llms <slug>") {
		t.Error("web index leaked the CLI-facing header")
	}
}

// TestLlmsIsOffline is the standing promise of the command: it answers with no
// network, no store and no project, which is what makes it usable as a local
// documentation source for an agent.
func TestLlmsIsOffline(t *testing.T) {
	p := clitest.NewProject(t)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "llms", "about")
	if res.ExitCode != 0 {
		t.Fatalf("`llms about` in an empty project exit = %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.HasPrefix(res.Stdout, "# ") {
		t.Error("page was not served from an empty project directory")
	}
}

// lines splits command output into non-empty trimmed lines.
func lines(t *testing.T, s string) []string {
	t.Helper()
	var out []string
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// indexSlugs extracts the link targets from the rendered root index.
func indexSlugs(index string) []string {
	re := regexp.MustCompile(`^- \[[^\]]+\]\(([^)]+)\)`)
	var out []string
	for line := range strings.SplitSeq(index, "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}
