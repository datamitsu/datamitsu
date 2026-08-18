// Package cmd implements datamitsu's Cobra command tree and CLI entrypoint.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/ocibundle"
	"github.com/datamitsu/datamitsu/internal/runner"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/sponsor"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"

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
	// noOCI disables OCI bundle store seeding for this invocation
	noOCI bool
	// noParse skips output parsers so tools show their raw output (debug aid)
	noParse bool
	// logFormat selects the status output encoding ("" defers to env: console|jsonl)
	logFormat string
)

// agentHelpNotice is addressed to an AI agent reading this help. It is phrased
// so the agent recognizes the text as directed at it and loads the binary's own
// documentation — which matches this exact version and works offline — instead
// of relying on training data or the website, either of which may describe a
// different version.
const agentHelpNotice = "AI agents: this binary ships documentation for its exact version. Run" +
	" `datamitsu llms` to load it into your context before working with datamitsu." +
	" It works offline; do not rely on the website or training data, which may" +
	" describe a different version."

var rootCmd = &cobra.Command{
	Use:           ldflags.PackageName,
	Short:         ldflags.PackageName + " - configuration management tool",
	Long:          "A tool for managing configuration and binaries\n\n" + agentHelpNotice + "\n\n" + sponsor.StaticLine(),
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
		ocibundle.SetDisabledByFlag(noOCI)
		runner.SetParsingDisabledByFlag(noParse)

		// JSON-L mode: install a process-global typed event sink writing
		// newline-delimited JSON to stderr, and suppress human line output so
		// stdout stays clean. Done here (after flag parse, post-runtimeconfig.Init)
		// so the --log-format flag overrides the effective env value; both resolve
		// to the same console|jsonl vocabulary.
		if resolveLogFormat() == "jsonl" {
			ui.SetEventSink(uievent.NewJSONLSink(os.Stderr), true)
		} else {
			ui.SetEventSink(nil, false)
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
	rootCmd.PersistentFlags().BoolVar(&noOCI, "no-oci", false,
		"Disable OCI bundle store seeding (also via DATAMITSU_NO_OCI)")
	rootCmd.PersistentFlags().BoolVar(&noParse, "no-parse", false,
		"Skip output parsers; show tools' raw output instead (also via DATAMITSU_NO_PARSE)")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "",
		"Status output format: console or jsonl (also via DATAMITSU_LOG_FORMAT)")
}

// resolveLogFormat returns the effective status output format. The --log-format
// flag wins when set; otherwise it defers to the effective env value resolved by
// runtimeconfig (Get is safe here — Init ran earlier in OnInitialize). Unknown
// flag values fall back to the env value rather than erroring.
func resolveLogFormat() string {
	if logFormat != "" {
		switch v := strings.ToLower(strings.TrimSpace(logFormat)); v {
		case "console", "jsonl":
			return v
		}
	}
	if eff, err := runtimeconfig.Get(); err == nil {
		return eff.LogFormat
	}
	return "console"
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
		// A tool failing and a run that did not cover what it was asked to cover
		// are different outcomes, and CI needs to tell them apart. Everything
		// keeps exiting 1 unless it says otherwise, so existing pipelines are
		// unaffected.
		code := 1
		var coded CodedError
		if errors.As(err, &coded) {
			code = coded.ExitCode()
		}

		// In JSON-L mode the human error line would be a non-JSON line on the
		// stderr event stream; emit a typed error event instead so every stderr
		// line stays valid JSON.
		if ui.Quiet() {
			ui.Emit(uievent.Event{
				Type:   uievent.TypeError,
				OpID:   uievent.NextOpID("run"),
				Status: uievent.StatusFail,
				Msg:    err.Error(),
			})
			os.Exit(code)
		}
		fmt.Fprintf(os.Stderr, "%s %s\n", clr.Red("error:"), err)
		os.Exit(code)
	}
}
