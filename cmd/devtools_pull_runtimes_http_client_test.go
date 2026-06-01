package cmd

import (
	"testing"
	"time"
)

// TestPullRuntimesPNPMClientTimeout pins the overall budget for the pull-runtimes
// pnpm metadata/tarball client after it was consolidated onto
// httpx.NewHardenedClient. This path only hashes the tarball, so it keeps its
// shorter 2-minute overall budget (and now also gains the hardened transport it
// previously lacked).
func TestPullRuntimesPNPMClientTimeout(t *testing.T) {
	if pnpmHTTPClient.Timeout != 2*time.Minute {
		t.Errorf("pull-runtimes pnpm client Timeout = %v, want 2m", pnpmHTTPClient.Timeout)
	}
}
