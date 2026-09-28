package cmd

import (
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/exitcode"
)

// checkParseLimits refuses a parse cap variable that does not hold a positive
// integer. Its getter falls back to the default, and a cap that is silently not
// the one asked for is something a run cannot tell anyone.
func checkParseLimits() error {
	if err := env.CheckParseCaps(); err != nil {
		return exitcode.UsageError{Err: err}
	}
	return nil
}
