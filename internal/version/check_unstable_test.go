package version

import (
	"strings"
	"testing"
)

func TestIsUnstable(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{"release pipeline format", "0.0.0-unstable.20260523.abc1234", true},
		{"release pipeline with v prefix", "v0.0.0-unstable.20260523.abc1234", true},
		{"unstable without trailing dot is not pipeline format", "0.0.0-unstable", false},
		{"unstable mid-string is not pipeline format", "1.2.3-unstable.foo", false},
		{"unstable inside identifier is not pipeline format", "1.2.3-my-unstable-build", false},
		{"build metadata containing unstable", "1.2.3+meta.unstable", false},
		{"stable release", "1.2.3", false},
		{"stable with v prefix", "v1.2.3", false},
		{"dev sentinel", "dev", false},
		{"empty string", "", false},
		{"rc prerelease", "1.2.3-rc.1", false},
		{"beta prerelease", "1.2.3-beta.1", false},
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
		{"v-prefixed unstable", "v0.0.0-unstable.20260523.abc1234", "0.1.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skipped, err := CompareVersions(tt.current, tt.required)
			if err != nil {
				t.Errorf("CompareVersions(%q, %q) returned unexpected error: %v (unstable current should bypass)", tt.current, tt.required, err)
			}
			if !skipped {
				t.Errorf("CompareVersions(%q, %q) skipped=false; expected true so caller can warn", tt.current, tt.required)
			}
		})
	}
}

// Non-pipeline prerelease strings that contain "unstable" must NOT bypass —
// only the documented release-pipeline format is treated as unstable.
func TestCompareVersions_NonPipelineUnstableLikeStrings_StillEnforced(t *testing.T) {
	tests := []struct {
		name    string
		current string
	}{
		{"unstable mid-string", "1.2.3-unstable.foo"},
		{"unstable inside identifier", "1.2.3-my-unstable-build"},
		{"unstable in build metadata", "1.2.3+meta.unstable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skipped, _ := CompareVersions(tt.current, "1.0.0")
			if skipped {
				t.Errorf("CompareVersions(%q, %q) bypassed; expected enforcement for non-pipeline format", tt.current, "1.0.0")
			}
		})
	}
}

// Unstable bypass must still validate required so config authors see typos
// in getMinVersion() regardless of which build their users are running.
func TestCompareVersions_UnstableCurrent_InvalidRequired_StillErrors(t *testing.T) {
	tests := []struct {
		name     string
		required string
	}{
		{"alpha required", "abc"},
		{"wildcard required", "1.x.3"},
		{"empty required", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skipped, err := CompareVersions("0.0.0-unstable.20260523.abc1234", tt.required)
			if err == nil {
				t.Errorf("CompareVersions(unstable, %q) expected error for invalid required, got nil", tt.required)
			}
			if skipped {
				t.Errorf("CompareVersions(unstable, %q) reported skipped=true alongside an error", tt.required)
			}
			if err != nil && !strings.Contains(err.Error(), "invalid required version format") {
				t.Errorf("error should report invalid required, got: %v", err)
			}
		})
	}
}

func TestCompareVersions_StableCurrent_StillEnforced(t *testing.T) {
	skipped, err := CompareVersions("1.0.0", "2.0.0")
	if err == nil {
		t.Error("CompareVersions(1.0.0, 2.0.0) expected error for older stable current, got nil")
	}
	if skipped {
		t.Error("CompareVersions(1.0.0, 2.0.0) reported skipped=true for stable current")
	}

	skipped, err = CompareVersions("2.0.0", "1.0.0")
	if err != nil {
		t.Errorf("CompareVersions(2.0.0, 1.0.0) returned unexpected error: %v", err)
	}
	if skipped {
		t.Error("CompareVersions(2.0.0, 1.0.0) reported skipped=true for stable current")
	}
}
