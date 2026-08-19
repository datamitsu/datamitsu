// Package main is the datamitsu CLI entry point, delegating to the cmd package.
package main

import (
	"os"

	"github.com/datamitsu/datamitsu/cmd"
	"github.com/datamitsu/datamitsu/internal/shim"

	_ "embed"
)

func main() {
	// Source mode puts symlinks to this executable on PATH, so the same binary
	// is also every tool the project declares. Dispatch inspects argv[0] and,
	// when it names a farm entry, execs the real tool without ever building the
	// cobra tree or constructing the UI. It declines — returning handled=false —
	// for a normal datamitsu invocation and for a datamitsu binary that was
	// merely renamed.
	if code, handled := shim.Dispatch(); handled {
		os.Exit(code)
	}
	cmd.Execute()
}
