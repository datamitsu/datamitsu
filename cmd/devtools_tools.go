package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "Inspect the configured tools (the fix/lint units)",
	Long: `List and inspect the tools declared in the config — the fix/lint units datamitsu
plans and runs. Shows each tool's operations, project types, the app it runs, and
its output parser. Use --json for machine-readable output.`,
}

var toolsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured tools with their operations and project types",
	Args:  cobra.NoArgs,
	RunE:  runToolsList,
}

var toolsInspectCmd = &cobra.Command{
	Use:   "inspect <name>",
	Short: "Show full configuration detail for one tool",
	Args:  cobra.ExactArgs(1),
	RunE:  runToolsInspect,
}

func init() {
	toolsListCmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of the rendered view")
	toolsInspectCmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of the rendered view")
	toolsCmd.AddCommand(toolsListCmd)
	toolsCmd.AddCommand(toolsInspectCmd)
	devtoolsCmd.AddCommand(toolsCmd)
}

func runToolsList(cmd *cobra.Command, _ []string) error {
	c, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	out := cmd.OutOrStdout()
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		// MarshalIndent sorts map keys, so the JSON is deterministic.
		return writeJSONIndent(out, c.Tools)
	}
	for _, name := range sortedToolNames(c.Tools) {
		if _, err := fmt.Fprint(out, renderToolConfigSummary(name, c.Tools[name])); err != nil {
			return fmt.Errorf("write tools list: %w", err)
		}
	}
	return nil
}

func runToolsInspect(cmd *cobra.Command, args []string) error {
	c, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	name := args[0]
	tool, ok := c.Tools[name]
	if !ok {
		return fmt.Errorf("tool %q is not declared in config", name)
	}
	out := cmd.OutOrStdout()
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		return writeJSONIndent(out, tool)
	}
	if _, err := fmt.Fprint(out, renderToolConfigDetail(name, tool)); err != nil {
		return fmt.Errorf("write tool detail: %w", err)
	}
	return nil
}

func sortedToolNames(tools config.MapOfTools) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedOps(ops map[config.OperationType]config.ToolOperation) []string {
	out := make([]string, 0, len(ops))
	for k := range ops {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

func skipReason(t config.Tool) string {
	if t.SkipReason != "" {
		return t.SkipReason
	}
	return "disabled"
}

// renderToolConfigSummary is the compact `list` row. fatih/color auto-disables
// under NO_COLOR / non-TTY, so the text stays byte-stable for the golden suite.
func renderToolConfigSummary(name string, t config.Tool) string {
	var b strings.Builder
	fmt.Fprint(&b, color.New(color.Bold).Sprint(name))
	fmt.Fprintf(&b, "  [%s]", strings.Join(sortedOps(t.Operations), ","))
	if len(t.ProjectTypes) > 0 {
		fmt.Fprintf(&b, "  %s", color.New(color.Faint).Sprintf("(%s)", strings.Join(t.ProjectTypes, ",")))
	}
	if t.OutputParser != "" {
		fmt.Fprintf(&b, "  %s", color.New(color.Faint).Sprintf("→ parser:%s", t.OutputParser))
	}
	if t.Skip {
		fmt.Fprintf(&b, "  %s", color.New(color.Faint).Sprintf("(skipped: %s)", skipReason(t)))
	}
	b.WriteByte('\n')
	return b.String()
}

// renderToolConfigDetail is the verbose `inspect` view with per-operation detail.
func renderToolConfigDetail(name string, t config.Tool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", color.New(color.Bold).Sprint(name))
	if t.Name != "" && t.Name != name {
		fmt.Fprintf(&b, "  name:         %s\n", t.Name)
	}
	if len(t.ProjectTypes) > 0 {
		fmt.Fprintf(&b, "  projectTypes: %s\n", strings.Join(t.ProjectTypes, ", "))
	}
	if t.OutputParser != "" {
		fmt.Fprintf(&b, "  outputParser: %s\n", t.OutputParser)
	}
	if t.Skip {
		fmt.Fprintf(&b, "  skipped:      %s\n", skipReason(t))
	}
	fmt.Fprintf(&b, "  operations:\n")
	for _, opName := range sortedOps(t.Operations) {
		op := t.Operations[config.OperationType(opName)]
		fmt.Fprintf(&b, "    %s: app=%s scope=%s", opName, op.App, op.Scope)
		if op.Priority != 0 {
			fmt.Fprintf(&b, " priority=%d", op.Priority)
		}
		if len(op.Globs) > 0 {
			fmt.Fprintf(&b, " globs=%s", strings.Join(op.Globs, ","))
		}
		b.WriteByte('\n')
		if len(op.Args) > 0 {
			fmt.Fprintf(&b, "      args: %s\n", strings.Join(op.Args, " "))
		}
	}
	return b.String()
}
