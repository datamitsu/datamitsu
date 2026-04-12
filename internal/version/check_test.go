package version

import (
	"strings"
	"testing"
)

func TestCompareVersions_ValidSemver(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		required string
	}{
		{"bare semver", "1.2.3", "1.0.0"},
		{"v-prefixed semver", "v1.2.3", "v1.0.0"},
		{"zero version", "0.1.0", "0.0.1"},
		{"mixed prefix current bare required v", "1.2.3", "v1.0.0"},
		{"mixed prefix current v required bare", "v1.2.3", "1.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CompareVersions(tt.current, tt.required)
			if err != nil {
				t.Errorf("CompareVersions(%q, %q) returned unexpected error: %v", tt.current, tt.required, err)
			}
		})
	}
}

func TestCompareVersions_InvalidSemver(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		required string
		wantMsg  string
	}{
		{"alpha string required", "1.0.0", "abc", "invalid required version format"},
		{"wildcard required", "1.0.0", "1.x.3", "invalid required version format"},
		{"alpha string current", "abc", "1.0.0", "invalid current version format"},
		{"empty required", "1.0.0", "", "invalid required version format"},
		{"empty current", "", "1.0.0", "invalid current version format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CompareVersions(tt.current, tt.required)
			if err == nil {
				t.Errorf("CompareVersions(%q, %q) expected error, got nil", tt.current, tt.required)
				return
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("CompareVersions(%q, %q) error = %q, want containing %q", tt.current, tt.required, err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestCompareVersions_CurrentGreaterThanRequired(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		required string
	}{
		{"major greater", "2.0.0", "1.0.0"},
		{"minor greater", "1.3.0", "1.2.0"},
		{"patch greater", "1.2.4", "1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CompareVersions(tt.current, tt.required)
			if err != nil {
				t.Errorf("CompareVersions(%q, %q) returned unexpected error: %v", tt.current, tt.required, err)
			}
		})
	}
}

func TestCompareVersions_CurrentEqualsRequired(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		required string
	}{
		{"exact match", "1.2.3", "1.2.3"},
		{"exact match with v prefix", "v1.2.3", "v1.2.3"},
		{"mixed prefix match", "v1.2.3", "1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CompareVersions(tt.current, tt.required)
			if err != nil {
				t.Errorf("CompareVersions(%q, %q) returned unexpected error: %v", tt.current, tt.required, err)
			}
		})
	}
}

func TestCompareVersions_CurrentLessThanRequired(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		required string
	}{
		{"major less", "1.0.0", "2.0.0"},
		{"minor less", "1.2.0", "1.3.0"},
		{"patch less", "1.2.3", "1.2.4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CompareVersions(tt.current, tt.required)
			if err == nil {
				t.Errorf("CompareVersions(%q, %q) expected error for older version, got nil", tt.current, tt.required)
			}
		})
	}
}

func TestCompareVersions_ErrorMessageFormat(t *testing.T) {
	err := CompareVersions("1.0.0", "2.0.0")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	msg := err.Error()

	checks := []struct {
		label string
		want  string
	}{
		{"required version", "v2.0.0"},
		{"current version", "v1.0.0"},
		{"upgrade instructions", "upgrade"},
	}

	for _, c := range checks {
		if !strings.Contains(msg, c.want) {
			t.Errorf("error message missing %s: got %q, want containing %q", c.label, msg, c.want)
		}
	}
}

func TestCompareVersions_DevVersion(t *testing.T) {
	err := CompareVersions("dev", "99.99.99")
	if err == nil {
		t.Error("CompareVersions(dev, 99.99.99) should fail since dev normalizes to v0.0.0")
	}

	err = CompareVersions("dev", "0.0.0")
	if err != nil {
		t.Errorf("CompareVersions(dev, 0.0.0) returned unexpected error: %v", err)
	}
}
