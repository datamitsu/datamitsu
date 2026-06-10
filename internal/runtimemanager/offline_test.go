package runtimemanager

import (
	"context"
	"strings"
	"testing"
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
	t.Setenv("DATAMITSU_OFFLINE", "1")
	rm := New(nil)
	err := rm.downloadPNPMFromRegistryURL(context.Background(),
		"https://registry.invalid", "11.0.0", t.TempDir(), strings.Repeat("ab", 32))
	if err == nil {
		t.Fatal("expected offline refusal, got nil")
	}
	if !strings.Contains(err.Error(), "DATAMITSU_OFFLINE") {
		t.Errorf("error %q should mention DATAMITSU_OFFLINE", err)
	}
}
