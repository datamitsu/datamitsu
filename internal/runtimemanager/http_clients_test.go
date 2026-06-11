package runtimemanager

import (
	"testing"
	"time"
)

// TestRuntimeHTTPClientTimeouts pins the payload download clients to NO
// end-to-end deadline: a flat budget encodes a hidden size/speed assumption
// that kills large-but-healthy downloads on slow links. Transfers are bounded
// by the httpx.StallGuard progress watchdog at the call sites instead.
func TestRuntimeHTTPClientTimeouts(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{"pnpm", pnpmHTTPClient.Timeout},
		{"jvm", jvmHTTPClient.Timeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.timeout != 0 {
				t.Errorf("%s client Timeout = %v, want 0 (progress-guarded, not deadline-bounded)", tt.name, tt.timeout)
			}
		})
	}
}
