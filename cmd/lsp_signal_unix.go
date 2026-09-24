//go:build unix

package cmd

import (
	"os"
	"os/signal"
	"syscall"
)

// absorbBrokenPipe keeps a write to a closed stdout or stderr from killing the
// language server: without it Go exits on the first such write, wherever the
// server is — including between persisting a buffer and running the tools. The
// write fails with EPIPE instead.
//
// Notify rather than signal.Ignore: an ignored signal is inherited by every
// tool the server starts, and a tool that relies on SIGPIPE to stop writing
// into a closed pipe would print errors instead. A handled one resets to the
// default in each child.
func absorbBrokenPipe() {
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
}
