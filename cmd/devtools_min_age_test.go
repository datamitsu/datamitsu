package cmd

import (
	"strconv"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/runtimeconfig"

	"github.com/spf13/cobra"
)

func TestResolveMinAge(t *testing.T) {
	eff := runtimeconfig.Effective{MinimumReleaseAgeMinutes: 10080}

	tests := []struct {
		name      string
		flagValue int
		want      int
	}{
		{"default sentinel uses effective", -1, 10080},
		{"zero disables filtering", 0, 0},
		{"positive custom value is used as-is", 1440, 1440},
		{"other negative falls back to effective", -5, 10080},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveMinAge(tt.flagValue, eff); got != tt.want {
				t.Errorf("resolveMinAge(%d, eff) = %d, want %d", tt.flagValue, got, tt.want)
			}
		})
	}
}

func TestAddMinAgeFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	p := addMinAgeFlag(cmd)
	if p == nil {
		t.Fatal("addMinAgeFlag returned nil pointer")
	}
	if *p != minAgeFlagDefault {
		t.Errorf("default min-age = %d, want %d", *p, minAgeFlagDefault)
	}
	if cmd.Flags().Lookup("min-age") == nil {
		t.Fatal("min-age flag was not registered on the command")
	}
}

func TestMinAgeDescriptionMentionsGlobalDefault(t *testing.T) {
	desc := minAgeDescription()
	if desc == "" {
		t.Fatal("minAgeDescription returned empty string")
	}
	// The help text must surface the actual global default and the sentinel
	// semantics it documents, not just be non-empty.
	wantDefault := strconv.Itoa(runtimeconfig.MinimumReleaseAgeMinutes)
	if !strings.Contains(desc, wantDefault) {
		t.Errorf("description should mention the global default %s, got %q", wantDefault, desc)
	}
	if !strings.Contains(desc, "disable") {
		t.Errorf("description should document the 0 = disable behavior, got %q", desc)
	}
}
