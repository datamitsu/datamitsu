package cmd

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/runner"

	"github.com/spf13/cobra"
)

// The flag is passed on only when it was given, so the runner can tell an
// explicit value from the default; without it DATAMITSU_FAIL_FAST decides, and
// a value the variable does not accept is refused here.
func TestFailFastOption(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     string
		want    *bool
		wantErr string
	}{
		{name: "default", want: nil},
		{name: "bare flag", args: []string{"--fail-fast"}, want: new(true)},
		{name: "flag false", args: []string{"--fail-fast=false"}, want: new(false)},
		{name: "env defers to the runner", env: "false", want: nil},
		{name: "flag wins over env", args: []string{"--fail-fast"}, env: "false", want: new(true)},
		{name: "invalid env", env: "yes", wantErr: "DATAMITSU_FAIL_FAST"},
		{name: "flag ignores invalid env", args: []string{"--fail-fast=false"}, env: "yes", want: new(false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATAMITSU_FAIL_FAST", tt.env)
			var value bool
			cmd := &cobra.Command{Use: "x"}
			addFailFastFlag(cmd, &value)
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags(%v): %v", tt.args, err)
			}
			var opts runner.Options
			err := applyFailFast(cmd, value, &opts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("applyFailFast() error = %v, want one naming %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("applyFailFast() error = %v", err)
			}
			got := opts.FailFast
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("FailFast = %v, want nil", *got)
			case tt.want != nil && (got == nil || *got != *tt.want):
				t.Errorf("FailFast = %v, want %v", got, *tt.want)
			}
		})
	}
}
