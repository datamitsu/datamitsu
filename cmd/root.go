package cmd

import (
	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/sponsor"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	// BinaryCommandOverride allows overriding the binary command used in facts
	BinaryCommandOverride string
	// BeforeConfigPaths allows specifying config files to load before auto-discovery (for wrappers/libraries)
	BeforeConfigPaths []string
	// NoAutoConfig disables auto-discovery of datamitsu.config.{js,mjs,ts} at git root
	NoAutoConfig bool
	// ConfigPaths allows specifying multiple configuration files to be merged
	ConfigPaths []string
)

var rootCmd = &cobra.Command{
	Use:           ldflags.PackageName,
	Short:         fmt.Sprintf("%s - configuration management tool", ldflags.PackageName),
	Long:          "A tool for managing configuration and binaries\n\n" + sponsor.StaticLine(),
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&BinaryCommandOverride, "binary-command", "",
		"Override the binary command (for npm package wrappers, etc). Can also be set via DATAMITSU_BINARY_COMMAND env var")
	rootCmd.PersistentFlags().StringSliceVar(&BeforeConfigPaths, "before-config", []string{},
		"Configuration file(s) to load before auto-discovery (for wrappers/libraries)")
	rootCmd.PersistentFlags().BoolVar(&NoAutoConfig, "no-auto-config", false,
		"Disable auto-discovery of datamitsu.config.{js,mjs,ts} at git root")
	rootCmd.PersistentFlags().StringSliceVar(&ConfigPaths, "config", []string{},
		"Additional configuration file(s) to load and merge (can be specified multiple times)")
}

func Execute() {
	clr.Init()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", clr.Red("error:"), err)
		os.Exit(1)
	}
}
