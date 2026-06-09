package httpx

import (
	"errors"
	"fmt"
	"strings"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ldflags"
)

// ErrOffline reports a network operation refused because offline mode is on.
// Match with errors.Is.
var ErrOffline = errors.New("offline mode: network access is disabled")

// GuardOffline returns an ErrOffline-wrapping error naming the blocked
// operation when offline mode is enabled, nil otherwise. Every network entry
// point calls it BEFORE dialing (or spawning a child process that would dial),
// so offline failures are uniform and actionable instead of surfacing as
// timeouts deep inside a download.
func GuardOffline(operation string) error {
	if !env.Offline() {
		return nil
	}
	envName := strings.ToUpper(ldflags.PackageName) + "_OFFLINE"
	return fmt.Errorf("%w by %s (blocked: %s); unset %s or pre-seed the store while online (%s store seed)",
		ErrOffline, envName, operation, envName, ldflags.PackageName)
}
