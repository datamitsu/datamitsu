package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/llmsdocs"
	"github.com/datamitsu/datamitsu/internal/version"

	"github.com/spf13/cobra"
)

// Exit codes specific to `llms`. The rest of the CLI only ever exits 0 or 1;
// this command separates its two failure modes because its consumer is a
// program, which must be able to tell "I asked for a page that does not exist"
// (recoverable: list the pages and retry) from "I called this wrong"
// (a bug in the caller) without parsing English.
const (
	exitLlmsUsage       = 2
	exitLlmsUnknownPage = 3
)

var (
	llmsList bool
	llmsJSON bool
	llmsWeb  bool
)

var llmsCmd = &cobra.Command{
	Use:   "llms [page]",
	Short: "Print project documentation as LLM-friendly markdown",
	Long: `Print the project documentation embedded in this binary, as plain markdown.

With no arguments it prints an index of every available page, where each entry is
the exact argument that fetches it, so an agent can choose its next call:

  datamitsu llms                      # index of all pages
  datamitsu llms about                # one page
  datamitsu llms contributing/brand-guidelines
  datamitsu llms brand-guidelines     # unique page names may be abbreviated
  datamitsu llms --list --json        # machine-readable page list
  datamitsu llms --web                # standard llms.txt, with website URLs

The documentation is compiled into the binary, so it always matches this exact
version and is served without network access. Content goes to stdout and
diagnostics to stderr, so the output can be piped straight into a model's
context. Exit codes: 2 for a usage error, 3 for an unknown or ambiguous page.`,
	RunE: runLlms,
}

func init() {
	llmsCmd.Flags().BoolVar(&llmsList, "list", false, "List every page slug, one per line")
	llmsCmd.Flags().BoolVar(&llmsJSON, "json", false, "Emit JSON instead of markdown")
	llmsCmd.Flags().BoolVar(&llmsWeb, "web", false, "Print the standard llms.txt index with website URLs")
	rootCmd.AddCommand(llmsCmd)
}

func runLlms(_ *cobra.Command, args []string) error {
	docs, err := llmsdocs.Load()
	if err != nil {
		return err
	}

	if len(args) > 1 {
		return llmsUsageError("accepts at most one page, got %d: %s", len(args), strings.Join(args, " "))
	}
	if llmsWeb && len(args) == 1 {
		return llmsUsageError("--web prints the page index and cannot be combined with a page argument")
	}

	switch {
	case llmsList:
		return printLlmsList(docs)
	case llmsWeb:
		fmt.Print(docs.WebIndex())
		return nil
	case len(args) == 0:
		return printLlmsRoot(docs)
	default:
		return printLlmsPage(docs, args[0])
	}
}

// printLlmsRoot prints the page index, or the provenance record under --json.
func printLlmsRoot(docs *llmsdocs.Docs) error {
	if !llmsJSON {
		fmt.Print(docs.Index())
		return nil
	}

	m := docs.Manifest()
	prov := struct {
		BinaryVersion string `json:"binaryVersion"`
		ImageTag      string `json:"imageTag,omitempty"`
		SchemaVersion int    `json:"schemaVersion"`
		Generator     string `json:"generator"`
		License       string `json:"license"`
		PageCount     int    `json:"pageCount"`
		PageSetHash   string `json:"pageSetHash"`
		Note          string `json:"note,omitempty"`
	}{
		BinaryVersion: ldflags.Version,
		ImageTag:      ldflags.ImageTag,
		SchemaVersion: m.SchemaVersion,
		Generator:     m.Generator,
		License:       m.License,
		PageCount:     m.PageCount,
		PageSetHash:   m.PageSetHash,
	}

	// Released builds are tagged, and CI refuses to tag a commit whose snapshot
	// is stale, so their docs provably match the source tree. A local or
	// unstable build carries no such guarantee, and says so rather than
	// implying one.
	if ldflags.Version == "dev" || version.IsUnstable(ldflags.Version) {
		prov.Note = "unversioned local or unstable build: embedded docs are not guaranteed to match a release"
	}

	return printLlmsJSON(prov)
}

// printLlmsList prints every canonical slug, or the full page metadata under
// --json so a caller can pick a page by title or description.
func printLlmsList(docs *llmsdocs.Docs) error {
	if llmsJSON {
		return printLlmsJSON(docs.Pages())
	}
	for _, slug := range docs.List() {
		fmt.Println(slug)
	}
	return nil
}

// printLlmsPage resolves a page argument and prints the page.
func printLlmsPage(docs *llmsdocs.Docs, arg string) error {
	slug, err := docs.Resolve(arg)
	if err != nil {
		return llmsPageError(docs, arg, err)
	}

	page, err := docs.Page(slug)
	if err != nil {
		return err
	}

	if llmsJSON {
		return printLlmsJSON(page)
	}
	fmt.Print(page.Body)
	return nil
}

func printLlmsJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

// llmsUsageError reports a caller mistake on stderr and exits 2.
//
// The exit happens here rather than by returning an error because Execute maps
// every returned error to exit 1, which would collapse this command's three
// outcomes into one. Returning nil afterwards keeps the signature honest for
// the compiler; the process is already gone.
func llmsUsageError(format string, a ...any) error {
	fmt.Fprintf(os.Stderr, "llms: %s\n", fmt.Sprintf(format, a...))
	os.Exit(exitLlmsUsage)
	return nil
}

// llmsPageError reports an unresolvable page on stderr and exits 3, offering the
// nearest page names when the argument looks like a typo.
func llmsPageError(docs *llmsdocs.Docs, arg string, err error) error {
	switch {
	case errors.Is(err, llmsdocs.ErrAmbiguous):
		fmt.Fprintf(os.Stderr, "llms: %v. Use the full page name.\n", err)
	default:
		msg := fmt.Sprintf("llms: unknown page %q.", arg)
		if suggestions := docs.Suggest(arg); len(suggestions) > 0 {
			msg += fmt.Sprintf(" Did you mean: %s?", strings.Join(suggestions, ", "))
		}
		fmt.Fprintln(os.Stderr, msg)
	}
	fmt.Fprintf(os.Stderr, "Run \"%s llms --list\" to see all %d pages.\n",
		ldflags.PackageName, docs.Manifest().PageCount)
	os.Exit(exitLlmsUnknownPage)
	return nil
}
