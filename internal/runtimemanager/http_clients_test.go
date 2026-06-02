package runtimemanager

import (
	"testing"
	"time"
)

// TestRuntimeHTTPClientTimeouts pins the overall per-request budget for each
// runtime download client after they were consolidated onto httpx.NewHardenedClient.
// The shared helper standardizes the transport sub-timeouts, but each call site
// MUST keep its original overall Timeout.
func TestRuntimeHTTPClientTimeouts(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{"pnpm", pnpmHTTPClient.Timeout, 5 * time.Minute},
		{"jvm", jvmHTTPClient.Timeout, 5 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.timeout != tt.want {
				t.Errorf("%s client Timeout = %v, want %v", tt.name, tt.timeout, tt.want)
			}
		})
	}
}
