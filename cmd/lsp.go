package cmd

import (
	"fmt"
	"os"

	"github.com/datamitsu/datamitsu/internal/lsp"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"

	"github.com/spf13/cobra"
)

var lspCmd = &cobra.Command{
	Use:   "lsp",
	Short: "Run a formatting-only LSP server over stdio",
	Long: `Starts a Language Server Protocol server on stdin/stdout that formats
documents with the project's configured fix tools plus datamitsu's in-core line
diff. Both stdin->stdout formatters and in-place "--write" tools are supported:
the unsaved buffer is formatted via a private temp copy outside the repo, so the
real file is never written. It implements only textDocument/formatting — no
diagnostics, no parsers.

stdout carries ONLY LSP JSON-RPC; all status/progress (including tool downloads)
is emitted as JSON-L on stderr. (--verbose additionally writes plain-text debug
lines to stderr, so stderr is line-delimited JSON only when --verbose is off.)`,
	Args: cobra.NoArgs,
	// Force JSON-L quiet mode on stderr for the whole process. The server owns
	// stdout for framed JSON-RPC, so nothing human/log may reach it. This runs
	// after cobra.OnInitialize, so it unconditionally overrides --log-format.
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		ui.SetEventSink(uievent.NewJSONLSink(os.Stderr), true)
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

	// Best-effort crash recovery: sweep in-place-format scratch dirs left by a
	// prior session that died mid-format. Non-fatal — a failure just leaves stale
	// temp dirs for the next sweep.
	_ = tooling.CleanStaleFormatTempDirs()

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
