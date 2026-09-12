package runtimemanager

import (
	"context"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func TestJARDownloadRefusesOffline(t *testing.T) {
	t.Setenv("DATAMITSU_OFFLINE", "1")
	err := downloadAndVerifyJAR(context.Background(), "checkstyle",
		"https://example.invalid/checkstyle.jar", strings.Repeat("ab", 32),
		t.TempDir()+"/checkstyle.jar")
	if err == nil {
		t.Fatal("expected offline refusal, got nil")
	}
	if !strings.Contains(err.Error(), "DATAMITSU_OFFLINE") {
		t.Errorf("error %q should mention DATAMITSU_OFFLINE", err)
	}
}

func TestPNPMDownloadRefusesOffline(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")
	rm := New(config.MapOfRuntimes{
		testPNPMRuntimeName: hostPNPMRuntime(t, "https://example.invalid/pnpm.tar.gz", strings.Repeat("ab", 32), testLibc),
	})
	_, err := rm.getRuntimePath(context.Background(), testPNPMRuntimeName)
	if err == nil {
		t.Fatal("expected offline refusal, got nil")
	}
	if !strings.Contains(err.Error(), "DATAMITSU_OFFLINE") {
		t.Errorf("error %q should mention DATAMITSU_OFFLINE", err)
	}
}
