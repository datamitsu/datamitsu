package cmd

import (
	"github.com/datamitsu/datamitsu/internal/env"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var storeCmd = &cobra.Command{
	Use:   "store",
	Short: "Manage the global binary and runtime store",
	Long:  `Manage the global binary and runtime store (binaries, runtimes, apps, remote configs).`,
}

var storePathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print store directory path",
	Long:  `Print the absolute path to the global store directory.`,
	RunE:  runStorePath,
}

var storeClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear the entire store",
	Long:  `Remove the entire global store directory including all binaries, runtimes, apps, and remote configs.`,
	RunE:  runStoreClear,
}

func init() {
	storeCmd.AddCommand(storePathCmd)
	storeCmd.AddCommand(storeClearCmd)
	rootCmd.AddCommand(storeCmd)
}

func runStorePath(cmd *cobra.Command, args []string) error {
	fmt.Println(env.GetStorePath())
	return nil
}

func runStoreClear(cmd *cobra.Command, args []string) error {
	storePath := filepath.Clean(env.GetStorePath())
	home, _ := os.UserHomeDir()
	if home != "" {
		home = filepath.Clean(home)
	}
	volume := filepath.VolumeName(storePath)
	sep := string(filepath.Separator)
	if storePath == "" || storePath == "." || storePath == "/" ||
		strings.EqualFold(storePath, home) ||
		!filepath.IsAbs(storePath) ||
		strings.EqualFold(storePath, volume+sep) ||
		(volume != "" && strings.EqualFold(storePath, volume)) ||
		(home != "" && strings.HasPrefix(strings.ToLower(home), strings.ToLower(storePath+sep))) {
		return fmt.Errorf("refusing to clear dangerous path: %s", storePath)
	}
	if err := os.RemoveAll(storePath); err != nil {
		return fmt.Errorf("failed to clear store: %w", err)
	}
	fmt.Printf("Cleared store: %s\n", storePath)
	return nil
}
