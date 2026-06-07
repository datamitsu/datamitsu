// Package cmd implements datamitsu's Cobra command tree and CLI entrypoint.
package cmd

import (
	"context"
	"fmt"
	"os"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/sponsor"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/ui"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

// commandContext returns the command's context, falling back to a background
// context when none is set. cobra's Command.Context returns nil until Execute
// installs one, so RunE handlers invoked directly (e.g. in tests) would
// otherwise propagate a nil context. Execute always sets a real context in
// production, so this only guards the direct-call path.
func commandContext(cmd *cobra.Command) context.Context {
	if cmd != nil {
		if ctx := cmd.Context(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

var (
	// BinaryCommandOverride allows overriding the binary command used in facts
	BinaryCommandOverride string
	// BeforeConfigPaths allows specifying config files to load before auto-discovery (for wrappers/libraries)
	BeforeConfigPaths []string
	// NoAutoConfig disables auto-discovery of datamitsu.config.{js,mjs,ts} at git root
	NoAutoConfig bool
	// ConfigPaths allows specifying multiple configuration files to be merged
	ConfigPaths []string
	// verbose raises the log level to debug for the whole run
	verbose bool
)

var rootCmd = &cobra.Command{
	Use:           ldflags.PackageName,
	Short:         ldflags.PackageName + " - configuration management tool",
	Long:          "A tool for managing configuration and binaries\n\n" + sponsor.StaticLine(),
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	cobra.OnInitialize(func() {
		// Applied after flag parsing so --verbose overrides the env/default level.
		if verbose {
			logger.SetLevel(zapcore.DebugLevel)
		}
		if err := runtimeconfig.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "%s %s\n", clr.Red("error:"), err)
			os.Exit(1)
		}
	})

	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false,
		"Enable debug-level logging (default level is warn)")
	rootCmd.PersistentFlags().StringVar(&BinaryCommandOverride, "binary-command", "",
		"Override the binary command (for npm package wrappers, etc). Can also be set via DATAMITSU_BINARY_COMMAND env var")
	rootCmd.PersistentFlags().StringSliceVar(&BeforeConfigPaths, "before-config", []string{},
		"Configuration file(s) to load before auto-discovery (for wrappers/libraries)")
	rootCmd.PersistentFlags().BoolVar(&NoAutoConfig, "no-auto-config", false,
		"Disable auto-discovery of datamitsu.config.{js,mjs,ts} at git root")
	rootCmd.PersistentFlags().StringSliceVar(&ConfigPaths, "config", []string{},
		"Additional configuration file(s) to load and merge (can be specified multiple times)")
}

// Execute runs the root command and exits the process on error.
func Execute() {
	clr.Init()

	// Activate the shared display for the whole process so every command —
	// including `exec`, which does not own a render scope of its own — renders
	// downloads/installs through one container (animated in a terminal, throttled
	// lines under CI). Commands that own a scope (runner, init) nest their own
	// Display via Activate and restore to this one. Closed before any error is
	// printed (and before os.Exit) so progress output is flushed first.
	disp := ui.New(term.DetectMode())
	restore := ui.Activate(disp)

	err := rootCmd.Execute()

	disp.Close()
	restore()

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", clr.Red("error:"), err)
		os.Exit(1)
	}
}
