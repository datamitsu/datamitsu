package remotecfg

import (
	"context"
	"strings"
	"testing"
)

func TestFetchRemoteConfigRefusesOffline(t *testing.T) {
	t.Setenv("DATAMITSU_OFFLINE", "1")
	_, err := FetchRemoteConfig(context.Background(), "https://example.invalid/cfg.ts", strings.Repeat("ab", 32))
	if err == nil {
		t.Fatal("expected offline refusal, got nil")
	}
	if !strings.Contains(err.Error(), "DATAMITSU_OFFLINE") {
		t.Errorf("error %q should mention DATAMITSU_OFFLINE", err)
	}
}
