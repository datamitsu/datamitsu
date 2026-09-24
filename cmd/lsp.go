package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/facts"
	"github.com/datamitsu/datamitsu/internal/lsp"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"

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

The server formats one repository: the datamitsu root (topmost git root) of the
first workspace folder initialize names, else its rootUri, else its rootPath,
else the directory the server was started in. The root is echoed in the
initialize result under capabilities.experimental.datamitsu.root. The
configuration is loaded after initialize and loaded again, before a format,
whenever one of its files has changed; a configuration that fails to load never
stops the server.

What runs on save is the session's format policy, read at initialize from
initializationOptions.format {widenTo: "target"|"unit", timeoutMs, tools:
{<tool>: bool}}, falling back per key to DATAMITSU_LSP_FORMAT_WIDEN_TO and
DATAMITSU_LSP_FORMAT_TIMEOUT_MS, then to unit and 15000. The effective policy is
echoed in the initialize result under capabilities.experimental.datamitsu.
A repository-wide fix never runs on save, nor does an operation marked
lsp: false; a project-wide fix that declares no globs runs only when
format.tools opts it in.

A cancelled format ($/cancelRequest) stops before its next group of tools and
is answered RequestCancelled; a tool that is already running finishes.

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

// lspForcedExitGrace is how long a second termination signal waits for the
// cancelled tools to exit before the process exits regardless.
const lspForcedExitGrace = time.Second

func init() {
	rootCmd.AddCommand(lspCmd)
}

func runLsp(cmd *cobra.Command, _ []string) error {
	code, err := serveLsp(commandContext(cmd))
	if err != nil {
		return err
	}
	// Honor the LSP-mandated conditional exit code (1 when `exit` arrived before
	// `shutdown`). The normal path returns nil so cobra exits 0.
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func serveLsp(parent context.Context) (int, error) {
	absorbBrokenPipe()

	// The root is not resolved here: initialize names the workspace, and a
	// failure to serve it must not kill the process before the client hears why.
	launchDir, err := os.Getwd()
	if err != nil {
		launchDir = "" // initialize can still name the workspace
	}

	// Never cancelled by a request: killing an in-place formatter can truncate
	// the user's file. Only a second termination signal cancels it.
	ctx, cancelTools := context.WithCancel(parent)
	defer cancelTools()

	srv := lsp.New(os.Stdin, os.Stdout, newLspLoader(), launchDir)
	stopSignals := watchLspSignals(srv, cancelTools)
	defer stopSignals()

	if err := srv.Run(ctx); err != nil {
		return 0, err
	}
	return srv.ExitCode(), nil
}

// watchLspSignals turns SIGTERM and interrupt into a graceful stop: the first
// behaves like the client closing stdin — the running format stops at its next
// checkpoint and the cache is flushed. A second cancels the tools' context and
// exits.
func watchLspSignals(srv *lsp.Server, cancelTools context.CancelFunc) (stop func()) {
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigs:
		case <-done:
			return
		}
		srv.Stop()
		select {
		case <-sigs:
		case <-done:
			return
		}
		// exec delivers SIGTERM to each tool's process group from its own
		// goroutine, so the process waits a moment instead of exiting under it.
		cancelTools()
		select {
		case <-done:
		case <-time.After(lspForcedExitGrace):
			os.Exit(1)
		}
	}()
	return func() {
		signal.Stop(sigs)
		close(done)
	}
}

// lspLoader loads the configuration from the root the language server serves,
// the way every command does. The flag paths are made absolute once, against
// the launch directory: a load changes into the root, and a relative --config
// must keep naming the file it named at startup.
//
// Changing the process directory is safe because the server serves one root
// and loads only on its single worker.
type lspLoader struct {
	before  []string
	configs []string
	noAuto  bool
}

func newLspLoader() lspLoader {
	return lspLoader{before: absPaths(BeforeConfigPaths), configs: absPaths(ConfigPaths), noAuto: NoAutoConfig}
}

// Root resolves dir's root with the resolver the loader itself uses, so the two
// never disagree about which repository is served.
func (l lspLoader) Root(ctx context.Context, dir string) (string, error) {
	if err := os.Chdir(dir); err != nil {
		return "", fmt.Errorf("enter %s: %w", dir, err)
	}
	root, err := facts.GetGitRoot(ctx)
	if err != nil {
		return "", fmt.Errorf("determine git root: %w", err)
	}
	if root == "" {
		return "", errors.New("determine git root: no git repository contains it")
	}
	return root, nil
}

func (l lspLoader) Load(ctx context.Context, root string) (*config.Config, []string, error) {
	if err := os.Chdir(root); err != nil {
		return nil, l.Watch(root), fmt.Errorf("enter %s: %w", root, err)
	}
	cfg, _, _, err := loadConfigWithPaths(ctx, l.before, l.noAuto, l.configs)
	if err != nil {
		return nil, l.Watch(root), fmt.Errorf("load config: %w", err)
	}
	return cfg, l.Watch(root), nil
}

// Watch is what a change to the configuration can show up in: the chain the
// last load read or tried to — a declared before-config that is missing
// included — plus the explicit flag paths, so a load that failed before
// resolving its chain still notices a fix, and, unless auto-discovery is off,
// the repository tripwires source mode watches (.git/HEAD, pnpm-lock.yaml, every
// auto-config candidate).
func (l lspLoader) Watch(root string) []string {
	explicit := append(append([]string(nil), l.before...), l.configs...)
	if l.noAuto {
		return sourcefarm.ConfigWatchPaths(explicit)
	}
	return sourcefarm.WatchPaths(root, append(ConfigChainFiles(), explicit...))
}
