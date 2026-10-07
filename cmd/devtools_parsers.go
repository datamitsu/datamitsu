package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/parsermanager"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var parsersCmd = &cobra.Command{
	Use:   "parsers",
	Short: "Inspect WASM output-parser modules and the tools they parse",
	Long: `List and inspect the WASM output-parser modules declared in the ` + "`parsers`" + ` config.

Each module self-describes (via its ` + "`describe`" + ` export) which tools it can parse,
how to invoke each, and its build-injected version. Results are deduplicated across
every configured parser — a module declared by N tools is described once.

Use --json for machine-readable output (to drive configs or build pipelines),
--wasm <path> to describe a local .wasm file without any config or network access,
or --embedded for the fallback module built into this binary. sniff shows which
standard format that fallback reads in a captured output.`,
}

var parsersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every tool the configured parser modules can parse (deduplicated)",
	Args:  usageArgs(cobra.NoArgs),
	RunE:  runParsersList,
}

var parsersInspectCmd = &cobra.Command{
	Use:   "inspect <tool>",
	Short: "Show full capability detail for one parsed tool",
	Args:  usageArgs(cobra.ExactArgs(1)),
	RunE:  runParsersInspect,
}

var parsersRunCmd = &cobra.Command{
	Use:   "run <tool>",
	Short: "Run a parser on a tool's raw output (from stdin) and print the diagnostics",
	Long: `Pipe a tool's raw output into its parser and see the structured diagnostics it
produces — the quickest way to develop or debug a parser against real output.

Reads the tool's stdout from stdin; pass --stderr-file / --exit-code if the parser
uses them (e.g. cue_fmt reads stderr). Resolves the module from --wasm (a local
.wasm), or from the configured ` + "`parsers`" + ` entry named <tool>. Output is the
module's answer as JSON: whether it recognized the output, the format it read,
and the nullable diagnostics (the core fills defaults later).

  pnpm dm exec eslint -- --format json file.js | \
    datamitsu devtools parsers run eslint --wasm ./datamitsu_parsers.wasm`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runParsersRun,
}

var parsersSniffCmd = &cobra.Command{
	Use:   "sniff [<file>|-]",
	Short: "Show which standard format the built-in fallback reads in a captured tool output",
	Long: `Run the sniffer of the fallback parser module built into this binary over a
tool's captured output — a file, or stdin with - or no argument — and print the
standard format it recognized and the findings it read. The core runs the same
fallback on the output of a tool whose output no declared parser recognized.

It is how to check a tool's format flag before declaring the format key as its
outputParser.parser: pass --stderr-file and --exit-code as the tool left them.

  ruff check --output-format sarif src > out.sarif
  datamitsu devtools parsers sniff out.sarif`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: runParsersSniff,
}

var parsersPrefetchCmd = &cobra.Command{
	Use:   "prefetch [module...]",
	Short: "Download + verify parser modules into the store (for OCI-bundle builds / airgap)",
	Long: `Download and SHA-256 verify the configured WASM parser modules into the store.

With no arguments, every module declared in the ` + "`parsers`" + ` config is fetched; pass
one or more module names to fetch a subset. A module already on disk whose bytes
still match its declared hash is a no-op. This materializes each module at its
content-addressed store path so it can be COPYed into an OCI-bundle layer (the
generated Dockerfile's parser stages run this) and later found offline by the
runtime — no compilation, fetch only.`,
	Args: cobra.ArbitraryArgs,
	RunE: runParsersPrefetch,
}

// addParsersFlags gives each leaf the same --json / --wasm / --embedded flags
// (read per-RunE so there is no shared mutable flag state between the
// commands).
func addParsersFlags(c *cobra.Command) {
	c.Flags().Bool("json", false, "Emit machine-readable JSON instead of the rendered view")
	c.Flags().String("wasm", "", "Describe a local .wasm module file instead of the configured parsers")
	c.Flags().Bool("embedded", false, "Describe the fallback parser module built into this binary instead of the configured parsers")
	c.MarkFlagsMutuallyExclusive("wasm", "embedded")
}

func init() {
	addParsersFlags(parsersListCmd)
	addParsersFlags(parsersInspectCmd)
	parsersRunCmd.Flags().String("wasm", "", "Local .wasm module to run (instead of the configured parser)")
	parsersRunCmd.Flags().Bool("embedded", false, "Run the fallback parser module built into this binary (instead of the configured parser)")
	parsersRunCmd.MarkFlagsMutuallyExclusive("wasm", "embedded")
	parsersRunCmd.Flags().String("module", "", "Which parsers entry (module) to load; defaults to the parser name")
	parsersRunCmd.Flags().String("stderr-file", "", "File holding the tool's stderr (some parsers read it, e.g. cue_fmt)")
	parsersRunCmd.Flags().Int("exit-code", 0, "The tool's exit code")
	parsersSniffCmd.Flags().Bool("json", false, "Emit the fallback's whole answer as JSON")
	parsersSniffCmd.Flags().String("stderr-file", "", "File holding the tool's stderr")
	parsersSniffCmd.Flags().Int("exit-code", 0, "The tool's exit code")
	parsersCmd.AddCommand(parsersListCmd)
	parsersCmd.AddCommand(parsersInspectCmd)
	parsersCmd.AddCommand(parsersRunCmd)
	parsersCmd.AddCommand(parsersSniffCmd)
	parsersCmd.AddCommand(parsersPrefetchCmd)
	devtoolsCmd.AddCommand(parsersCmd)
}

func runParsersPrefetch(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	c, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if len(c.Parsers) == 0 {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "no parsers declared in config; nothing to prefetch"); err != nil {
			return fmt.Errorf("write notice: %w", err)
		}
		return nil
	}
	mgr := parsermanager.New(c.Parsers)
	defer func() { _ = mgr.Close(ctx) }()
	if err := mgr.Prefetch(ctx, args); err != nil {
		return err
	}
	fetched := args
	if len(fetched) == 0 {
		fetched = make([]string, 0, len(c.Parsers))
		for name := range c.Parsers {
			fetched = append(fetched, name)
		}
		sort.Strings(fetched)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Prefetched %d parser module(s): %s\n", len(fetched), strings.Join(fetched, ", ")); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

func runParsersRun(cmd *cobra.Command, args []string) error {
	tool := args[0]
	ctx := cmd.Context()

	stdout, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	var stderr []byte
	if p, _ := cmd.Flags().GetString("stderr-file"); p != "" {
		if stderr, err = os.ReadFile(p); err != nil {
			return fmt.Errorf("read stderr file: %w", err)
		}
	}
	exitCode, _ := cmd.Flags().GetInt("exit-code")
	//nolint:gosec // G115: a process exit code is small; the int32 cast is intentional.
	ec := int32(exitCode)

	var resp parsermanager.Response
	if embedded, _ := cmd.Flags().GetBool("embedded"); embedded {
		resp, err = parsermanager.ParseEmbedded(ctx, tool, stdout, stderr, ec)
	} else if wasmPath, _ := cmd.Flags().GetString("wasm"); wasmPath != "" {
		wasm, readErr := os.ReadFile(wasmPath)
		if readErr != nil {
			return fmt.Errorf("read wasm module: %w", readErr)
		}
		resp, err = parsermanager.ParseLocal(ctx, wasm, tool, stdout, stderr, ec)
	} else {
		c, _, _, loadErr := loadConfig()
		if loadErr != nil {
			return fmt.Errorf("loading config: %w (or pass --wasm <path>)", loadErr)
		}
		// The positional arg is the parser dispatch key; --module selects which
		// parsers entry to load (defaults to the same name, the shorthand case).
		module, _ := cmd.Flags().GetString("module")
		if module == "" {
			module = tool
		}
		mgr := parsermanager.New(c.Parsers)
		defer func() { _ = mgr.Close(ctx) }()
		resp, err = mgr.ParseOutput(ctx, module, tool, stdout, stderr, ec)
	}
	if err != nil {
		return err
	}
	return writeJSONIndent(cmd.OutOrStdout(), resp)
}

// runParsersSniff reads a captured output and prints what the embedded sniffer
// makes of it.
func runParsersSniff(cmd *cobra.Command, args []string) error {
	var stdout []byte
	var err error
	if len(args) == 0 || args[0] == "-" {
		stdout, err = io.ReadAll(cmd.InOrStdin())
	} else {
		stdout, err = os.ReadFile(args[0])
	}
	if err != nil {
		return fmt.Errorf("read the output: %w", err)
	}
	var stderr []byte
	if p, _ := cmd.Flags().GetString("stderr-file"); p != "" {
		if stderr, err = os.ReadFile(p); err != nil {
			return fmt.Errorf("read stderr file: %w", err)
		}
	}
	exitCode, _ := cmd.Flags().GetInt("exit-code")
	//nolint:gosec // G115: a process exit code is small; the int32 cast is intentional.
	resp, err := parsermanager.ParseEmbedded(cmd.Context(), parsermanager.FallbackParser, stdout, stderr, int32(exitCode))
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		return writeJSONIndent(out, resp)
	}
	if _, err := fmt.Fprint(out, renderSniff(resp)); err != nil {
		return fmt.Errorf("write sniff result: %w", err)
	}
	return nil
}

// renderSniff is the human view of the sniffer's answer: the format, then one
// line per finding.
func renderSniff(resp parsermanager.Response) string {
	if !resp.Recognized {
		return "no standard format recognized\n"
	}
	var b strings.Builder
	noun := "findings"
	if len(resp.Diagnostics) == 1 {
		noun = "finding"
	}
	fmt.Fprintf(&b, "format: %s (%d %s)\n", resp.Format, len(resp.Diagnostics), noun)
	for _, d := range resp.Diagnostics {
		fmt.Fprintf(&b, "  %s", sniffLocation(d))
		if d.Severity != nil {
			fmt.Fprintf(&b, " %s", diagnostic.Severity(*d.Severity))
		}
		if d.Code != nil {
			fmt.Fprintf(&b, " [%s]", *d.Code)
		}
		fmt.Fprintf(&b, " %s\n", strings.ReplaceAll(d.Message, "\n", " "))
	}
	return b.String()
}

func sniffLocation(d parsermanager.RawDiagnostic) string {
	loc := "-"
	if d.File != nil {
		loc = *d.File
	}
	if d.Row != nil {
		loc += fmt.Sprintf(":%d", *d.Row)
		if d.Col != nil {
			loc += fmt.Sprintf(":%d", *d.Col)
		}
	}
	return loc
}

// loadParserCatalog builds the catalog from a local --wasm file or the
// embedded fallback (both fully offline), or by describing every configured
// parser (deduplicated).
func loadParserCatalog(cmd *cobra.Command) (*parsermanager.ParserCatalog, error) {
	ctx := cmd.Context()
	if embedded, _ := cmd.Flags().GetBool("embedded"); embedded {
		caps, err := parsermanager.DescribeEmbedded(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe the embedded module: %w", err)
		}
		return parsermanager.CatalogFromCapabilities("(embedded)", caps), nil
	}
	if wasmPath, _ := cmd.Flags().GetString("wasm"); wasmPath != "" {
		data, err := os.ReadFile(wasmPath)
		if err != nil {
			return nil, fmt.Errorf("read wasm module: %w", err)
		}
		caps, err := parsermanager.DescribeLocal(ctx, data)
		if err != nil {
			return nil, fmt.Errorf("describe %s: %w", wasmPath, err)
		}
		return parsermanager.CatalogFromCapabilities("(local)", caps), nil
	}

	c, _, _, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	mgr := parsermanager.New(c.Parsers)
	defer func() { _ = mgr.Close(ctx) }()
	return mgr.ListCapabilities(ctx)
}

func runParsersList(cmd *cobra.Command, _ []string) error {
	cat, err := loadParserCatalog(cmd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		return writeJSONIndent(out, cat)
	}
	for _, t := range cat.Tools {
		if _, err := fmt.Fprint(out, renderToolLine(t)); err != nil {
			return fmt.Errorf("write parser list: %w", err)
		}
	}
	// Conflicts are an advisory smell, not a failure — surface on stderr so the
	// stdout listing stays clean for piping.
	for _, conflict := range cat.Conflicts {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), color.New(color.FgYellow).Sprint("conflict: ")+conflict); err != nil {
			return fmt.Errorf("write conflict: %w", err)
		}
	}
	return writeOutdatedNotes(cmd.ErrOrStderr(), cat.Modules)
}

// writeOutdatedNotes says, once per module, which described modules are older
// than the newest descriptor schema.
func writeOutdatedNotes(w io.Writer, modules []parsermanager.CatalogModule) error {
	for _, m := range modules {
		if !m.Outdated() {
			continue
		}
		if _, err := fmt.Fprintln(w, color.New(color.FgYellow).Sprint("note: ")+m.OutdatedNote()); err != nil {
			return fmt.Errorf("write note: %w", err)
		}
	}
	return nil
}

func runParsersInspect(cmd *cobra.Command, args []string) error {
	cat, err := loadParserCatalog(cmd)
	if err != nil {
		return err
	}
	name := args[0]
	for i := range cat.Tools {
		if cat.Tools[i].Name != name {
			continue
		}
		out := cmd.OutOrStdout()
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			return writeJSONIndent(out, cat.Tools[i])
		}
		if _, err := fmt.Fprint(out, renderToolDetail(cat.Tools[i])); err != nil {
			return fmt.Errorf("write parser detail: %w", err)
		}
		return nil
	}
	return fmt.Errorf("tool %q is not provided by any configured parser", name)
}

func writeJSONIndent(out io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	if _, err := fmt.Fprintln(out, string(data)); err != nil {
		return fmt.Errorf("write JSON: %w", err)
	}
	return nil
}

// renderToolLine is the compact `list` row. fatih/color auto-disables under
// NO_COLOR / non-TTY, so the rendered text stays byte-stable for the golden suite.
func renderToolLine(t parsermanager.CatalogTool) string {
	modes := strings.Join(t.Modes(), ",")
	if modes == "" {
		modes = "-"
	}
	var b strings.Builder
	name := color.New(color.Bold).Sprint(t.Name)
	ver := ""
	if t.Version != "" {
		ver = color.New(color.Faint).Sprintf(" (%s)", t.Version)
	}
	fmt.Fprintf(&b, "%s%s  [%s]\n", name, ver, modes)
	if t.Description != "" {
		fmt.Fprintf(&b, "  %s\n", t.Description)
	}
	if t.URL != "" {
		fmt.Fprintf(&b, "  %s\n", color.New(color.FgBlue).Sprint(t.URL))
	}
	var facts []string
	if t.Kind == "format" {
		facts = append(facts, "format")
	}
	if levels, ok := levelsText(t); ok {
		facts = append(facts, "levels: "+levels)
	}
	if t.ColumnUnit != "" {
		facts = append(facts, "columns: "+t.ColumnUnit)
	}
	if t.Category != "" {
		facts = append(facts, "category: "+t.Category)
	}
	if len(facts) > 0 {
		fmt.Fprintf(&b, "  %s\n", color.New(color.Faint).Sprint(strings.Join(facts, " · ")))
	}
	return b.String()
}

// levelsText renders a tool's level vocabulary; ok is false for a module that
// predates the field, which says nothing about levels.
func levelsText(t parsermanager.CatalogTool) (string, bool) {
	switch {
	case t.Severities == nil:
		return "", false
	case len(t.Severities) == 0:
		return "none", true
	default:
		return strings.Join(t.Severities, ", "), true
	}
}

// renderToolDetail is the verbose `inspect` view, including per-mode invocations.
func renderToolDetail(t parsermanager.CatalogTool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", color.New(color.Bold).Sprint(t.Name))
	fmt.Fprintf(&b, "  parser:   %s\n", t.Parser)
	fmt.Fprintf(&b, "  module:   %s\n", t.Module)
	fmt.Fprintf(&b, "  version:  %s\n", t.Version)
	if t.URL != "" {
		fmt.Fprintf(&b, "  url:      %s\n", t.URL)
	}
	if t.Description != "" {
		fmt.Fprintf(&b, "  desc:     %s\n", t.Description)
	}
	if levels, ok := levelsText(t); ok {
		fmt.Fprintf(&b, "  levels:   %s\n", levels)
	}
	if t.ColumnUnit != "" {
		fmt.Fprintf(&b, "  columns:  %s\n", t.ColumnUnit)
	}
	if t.Category != "" {
		fmt.Fprintf(&b, "  category: %s\n", t.Category)
	}
	if t.Kind != "" {
		fmt.Fprintf(&b, "  kind:     %s\n", t.Kind)
	}
	modes := t.Modes()
	if len(modes) == 0 {
		fmt.Fprintf(&b, "  modes:    (none)\n")
		return b.String()
	}
	fmt.Fprintf(&b, "  modes:\n")
	for _, mode := range modes {
		op := t.Operations[mode]
		stdin := ""
		if op.Stdin {
			stdin = " (stdin)"
		}
		fmt.Fprintf(&b, "    %s: %s%s\n", mode, strings.Join(op.Args, " "), stdin)
	}
	return b.String()
}
