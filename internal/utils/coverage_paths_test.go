package utils

import (
	"runtime"
	"testing"
)

func TestHomeDir_Error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME-based fallback does not apply on Windows")
	}
	// With HOME unset/empty, os.UserHomeDir returns an error on unix.
	t.Setenv("HOME", "")
	if _, err := HomeDir(); err == nil {
		t.Error("HomeDir() expected error with empty HOME, got nil")
	}
}

func TestExpandHome_EmptyHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME-based fallback does not apply on Windows")
	}
	t.Setenv("HOME", "")
	// ExpandHome swallows the HomeDir error and returns an empty prefix; the
	// important property is that it does not panic on the error path.
	if got := ExpandHome("~"); got != "" {
		t.Errorf("ExpandHome(~) with empty HOME = %q, want empty", got)
	}
	if got := ExpandHome("~/sub"); got != "sub" && got != "/sub" {
		t.Errorf("ExpandHome(~/sub) with empty HOME = %q", got)
	}
}
