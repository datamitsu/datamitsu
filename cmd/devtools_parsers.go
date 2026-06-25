package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

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

Use --json for machine-readable output (to drive configs or build pipelines), or
--wasm <path> to describe a local .wasm file without any config or network access.`,
}

var parsersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every tool the configured parser modules can parse (deduplicated)",
	Args:  cobra.NoArgs,
	RunE:  runParsersList,
}

var parsersInspectCmd = &cobra.Command{
	Use:   "inspect <tool>",
	Short: "Show full capability detail for one parsed tool",
	Args:  cobra.ExactArgs(1),
	RunE:  runParsersInspect,
}

// addParsersFlags gives each leaf the same --json / --wasm pair (read per-RunE so
// there is no shared mutable flag state between the two commands).
func addParsersFlags(c *cobra.Command) {
	c.Flags().Bool("json", false, "Emit machine-readable JSON instead of the rendered view")
	c.Flags().String("wasm", "", "Describe a local .wasm module file instead of the configured parsers")
}

func init() {
	addParsersFlags(parsersListCmd)
	addParsersFlags(parsersInspectCmd)
	parsersCmd.AddCommand(parsersListCmd)
	parsersCmd.AddCommand(parsersInspectCmd)
	devtoolsCmd.AddCommand(parsersCmd)
}

// loadParserCatalog builds the catalog either from a local --wasm file (fully
// offline) or by describing every configured parser (deduplicated).
func loadParserCatalog(cmd *cobra.Command) (*parsermanager.ParserCatalog, error) {
	ctx := cmd.Context()
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
	return parsermanager.New(c.Parsers).ListCapabilities(ctx)
}

func runParsersList(cmd *cobra.Command, _ []string) error {
	cat, err := loadParserCatalog(cmd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		return writeParsersJSON(out, cat)
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
			return writeParsersJSON(out, cat.Tools[i])
		}
		if _, err := fmt.Fprint(out, renderToolDetail(cat.Tools[i])); err != nil {
			return fmt.Errorf("write parser detail: %w", err)
		}
		return nil
	}
	return fmt.Errorf("tool %q is not provided by any configured parser", name)
}

func writeParsersJSON(out io.Writer, v any) error {
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
	return b.String()
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
