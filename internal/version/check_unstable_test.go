package version

import "testing"

func TestIsUnstable(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{"unstable build", "0.0.0-unstable.20260523.abc1234", true},
		{"unstable with v prefix", "v0.0.0-unstable.20260523.abc1234", true},
		{"unstable mid-string", "1.2.3-unstable.foo", true},
		{"stable release", "1.2.3", false},
		{"stable with v prefix", "v1.2.3", false},
		{"dev sentinel", "dev", false},
		{"empty string", "", false},
		{"different prerelease", "1.2.3-rc.1", false},
		{"different prerelease beta", "1.2.3-beta.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsUnstable(tt.version)
			if got != tt.want {
				t.Errorf("IsUnstable(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestCompareVersions_UnstableCurrent_BypassesCheck(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		required string
	}{
		{"unstable vs stable equal base", "0.0.0-unstable.20260523.abc1234", "0.0.0"},
		{"unstable vs higher required", "0.0.0-unstable.20260523.abc1234", "0.1.0"},
		{"unstable vs much higher required", "0.0.0-unstable.20260523.abc1234", "99.99.99"},
		{"unstable vs equal required", "1.2.3-unstable.foo", "1.2.3"},
		{"v-prefixed unstable", "v0.0.0-unstable.20260523.abc1234", "0.1.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CompareVersions(tt.current, tt.required)
			if err != nil {
				t.Errorf("CompareVersions(%q, %q) returned unexpected error: %v (unstable current should bypass)", tt.current, tt.required, err)
			}
		})
	}
}

func TestCompareVersions_StableCurrent_StillEnforced(t *testing.T) {
	// Regression guard: stable versions still get the normal comparison.
	err := CompareVersions("1.0.0", "2.0.0")
	if err == nil {
		t.Error("CompareVersions(1.0.0, 2.0.0) expected error for older stable current, got nil")
	}

	err = CompareVersions("2.0.0", "1.0.0")
	if err != nil {
		t.Errorf("CompareVersions(2.0.0, 1.0.0) returned unexpected error: %v", err)
	}
}
