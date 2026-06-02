package cmd

import (
	"strings"
	"testing"
)

func TestPullGithubCmd_HasMinAgeFlag(t *testing.T) {
	if pullGithubMinAge == nil {
		t.Fatal("pullGithubMinAge pointer was not wired up in init()")
	}
	if *pullGithubMinAge != minAgeFlagDefault {
		t.Errorf("default --min-age = %d, want sentinel %d", *pullGithubMinAge, minAgeFlagDefault)
	}
	if pullGithubCmd.Flags().Lookup("min-age") == nil {
		t.Fatal("--min-age flag was not registered on pull-github")
	}
}

func TestMinAgeBanner(t *testing.T) {
	tests := []struct {
		name   string
		minAge int
		want   string
	}{
		{"zero is disabled", 0, "disabled"},
		{"negative is disabled", -1, "disabled"},
		{"positive shows minutes", 10080, "10080 minutes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := minAgeBanner(tt.minAge); got != tt.want {
				t.Errorf("minAgeBanner(%d) = %q, want %q", tt.minAge, got, tt.want)
			}
		})
	}
}

func TestNoReleaseOldEnoughErr(t *testing.T) {
	err := noReleaseOldEnoughErr("ripgrep", 10080)
	if err == nil {
		t.Fatal("expected a non-nil error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ripgrep") {
		t.Errorf("error should name the app, got: %q", msg)
	}
	if !strings.Contains(msg, "10080") {
		t.Errorf("error should mention the min age, got: %q", msg)
	}
	if !strings.Contains(msg, "--min-age 0") {
		t.Errorf("error should point at the --min-age 0 bypass, got: %q", msg)
	}
}
