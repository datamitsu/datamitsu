// Command sourcehash writes the fingerprint of the embedded fallback parser
// module's sources beside the module (`task build:parsers:embedded` runs it
// right after the module is copied there).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/parsermanager/embedded/sourcehash"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sourcehash:", err)
		os.Exit(1)
	}
}

func run() error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("working directory: %w", err)
	}
	root, err := sourcehash.Root(wd)
	if err != nil {
		return err
	}
	hash, err := sourcehash.Current(root)
	if err != nil {
		return err
	}
	out := filepath.Join(root, filepath.FromSlash(sourcehash.HashFile))
	if err := os.WriteFile(out, []byte(hash+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", sourcehash.HashFile, err)
	}
	fmt.Printf("%s %s\n", hash, sourcehash.HashFile)
	return nil
}
