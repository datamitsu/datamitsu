package gittest

import (
	"os"
	"testing"
)

func TestIsCommandScopeKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"GIT_CONFIG_PARAMETERS", true},
		{"GIT_CONFIG_COUNT", true},
		{"GIT_CONFIG_KEY_0", true},
		{"GIT_CONFIG_VALUE_12", true},
		{"GIT_CONFIG_GLOBAL", false},
		{"GIT_CONFIG_SYSTEM", false},
		{"GIT_DIR", false},
	}
	for _, tt := range tests {
		if got := IsCommandScopeKey(tt.key); got != tt.want {
			t.Errorf("IsCommandScopeKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestIsolateReplacesInheritedCommandScope(t *testing.T) {
	t.Setenv("GIT_CONFIG_PARAMETERS", "'user.name'='x'")
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_1", "core.excludesFile")
	t.Setenv("GIT_CONFIG_VALUE_1", "/x")

	Isolate()

	for key, want := range map[string]string{
		"GIT_CONFIG_PARAMETERS": "",
		"GIT_CONFIG_KEY_1":      "",
		"GIT_CONFIG_VALUE_1":    "",
		"GIT_CONFIG_COUNT":      "1",
		"GIT_CONFIG_KEY_0":      "core.excludesFile",
	} {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}
