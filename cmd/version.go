package cmd

import (
	"fmt"

	"github.com/datamitsu/datamitsu/internal/ldflags"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Aliases:                    nil,
	SuggestFor:                 nil,
	GroupID:                    "",
	Example:                    "",
	ValidArgs:                  nil,
	ValidArgsFunction:          nil,
	Args:                       nil,
	ArgAliases:                 nil,
	BashCompletionFunction:     "",
	Deprecated:                 "",
	Annotations:                nil,
	Version:                    "",
	PersistentPreRun:           nil,
	PersistentPreRunE:          nil,
	PreRun:                     nil,
	PreRunE:                    nil,
	RunE:                       nil,
	PostRun:                    nil,
	PostRunE:                   nil,
	PersistentPostRun:          nil,
	PersistentPostRunE:         nil,
	FParseErrWhitelist:         cobra.FParseErrWhitelist{UnknownFlags: false, UnknownFlagsHandling: 0},
	CompletionOptions:          cobra.CompletionOptions{DisableDefaultCmd: false, DisableNoDescFlag: false, DisableDescriptions: false, HiddenDefaultCmd: false, DefaultShellCompDirective: nil},
	TraverseChildren:           false,
	Hidden:                     false,
	SilenceErrors:              false,
	SilenceUsage:               false,
	DisableFlagParsing:         false,
	DisableAutoGenTag:          false,
	DisableFlagsInUseLine:      false,
	DisableSuggestions:         false,
	SuggestionsMinimumDistance: 0,
	Use:                        "version",
	Short:                      "Print the version number",
	Long:                       `Print the version number of datamitsu`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("%s version %s\n", ldflags.PackageName, ldflags.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
