// Package main implements toolenv-doc, the maintainer-only generator of the
// tool-environment reference page. It prints the page internal/toolenv
// renders; task gen:toolenv-doc writes it to toolenv.DocPath and formats it.
// It is a standalone main, not a datamitsu subcommand, so the shipped binary
// carries no documentation generator.
package main

import (
	"fmt"
	"os"

	"github.com/datamitsu/datamitsu/internal/toolenv"
)

func main() {
	if _, err := fmt.Fprint(os.Stdout, toolenv.Markdown()); err != nil {
		fmt.Fprintln(os.Stderr, "toolenv-doc:", err)
		os.Exit(1)
	}
}
