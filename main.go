// Package main is the datamitsu CLI entry point, delegating to the cmd package.
package main

import (
	"github.com/datamitsu/datamitsu/cmd"

	_ "embed"
)

func main() {
	cmd.Execute()
}
