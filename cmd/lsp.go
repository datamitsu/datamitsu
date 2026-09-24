package cmd

import (
	"fmt"
	"os"

	"github.com/datamitsu/datamitsu/internal/lsp"
	"github.com/datamitsu/datamitsu/internal/traverser"

	"github.com/spf13/cobra"
)

var lspCmd = &cobra.Command{
	Use:   "lsp",
	Short: "Run a formatting-only LSP server over stdio",
	Long: `Starts a Language Server Protocol server on stdin/stdout that formats
documents by running the project's configured fix on the real file (so every
tool's own project/config detection matches "datamitsu fix"), then returning the
diff as edits. A format persists the buffer to disk first, so it also saves the
file. It implements only textDocument/formatting — no diagnostics, no parsers.

What runs on save is the session's format policy, read once at initialize from
initializationOptions.format {widenTo: "target"|"unit", timeoutMs, tools:
{<tool>: bool}}, falling back per key to DATAMITSU_LSP_FORMAT_WIDEN_TO and
DATAMITSU_LSP_FORMAT_TIMEOUT_MS, then to unit and 15000. The effective policy is
echoed in the initialize result under capabilities.experimental.datamitsu.
A repository-wide fix never runs on save, nor does an operation marked
lsp: false; a project-wide fix that declares no globs runs only when
format.tools opts it in.

stdout carries ONLY LSP JSON-RPC. stderr is line-delimited JSON: status and
progress (including tool downloads), the server's notices and every log line,
as log events with a level of debug, info, warn or error. --verbose lowers the
log level to debug, so info and debug log events appear as well.`,
	Args: cobra.NoArgs,
	// Force JSON-L quiet mode on stderr for the whole process. The server owns
	// stdout for framed JSON-RPC, so nothing human/log may reach it. This runs
	// after cobra.OnInitialize, so it unconditionally overrides --log-format.
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		setJSONLStderr(true)
	},
	RunE: runLsp,
}

func init() {
	rootCmd.AddCommand(lspCmd)
}

func runLsp(cmd *cobra.Command, _ []string) error {
	ctx := commandContext(cmd)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	root, err := traverser.GetGitRoot(ctx, cwd)
	if err != nil {
		return fmt.Errorf("determine git root: %w", err)
	}

	// Config is loaded once and reused for the whole session (no mid-session
	// reload in this phase). Quiet mode is already active, so a config console.log
	// can't pollute stdout.
	cfg, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	srv := lsp.NewServer(os.Stdin, os.Stdout, cfg, root)
	if err := srv.Run(ctx); err != nil {
		return err
	}
	// Honor the LSP-mandated conditional exit code (1 when `exit` arrived before
	// `shutdown`). The normal path returns nil so cobra exits 0.
	if code := srv.ExitCode(); code != 0 {
		os.Exit(code)
	}
	return nil
}
