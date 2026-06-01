package remotecfg

import (
	"testing"
	"time"
)

// TestRemoteConfigHTTPClientTimeout pins the overall budget for the remote-config
// fetch client after it was consolidated onto httpx.NewHardenedClient. Configs are
// small, so this client keeps its tighter 30s overall budget.
func TestRemoteConfigHTTPClientTimeout(t *testing.T) {
	if httpClient.Timeout != 30*time.Second {
		t.Errorf("remotecfg client Timeout = %v, want 30s", httpClient.Timeout)
	}
}
