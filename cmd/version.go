package cmd

import (
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number",
	Long:  `Print the version number of datamitsu`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("%s version %s\n", ldflags.PackageName, ldflags.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
