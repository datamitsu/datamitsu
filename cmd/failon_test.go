package cmd

import (
	"errors"
	"testing"

	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/runner"

	"github.com/spf13/cobra"
)

// TestApplyFailOn: the flag wins over DATAMITSU_FAIL_ON, and an invalid value
// of either is a usage error even when the other is given.
func TestApplyFailOn(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		flag    string
		want    string
		wantErr bool
	}{
		{name: "neither"},
		{name: "env only", env: "info", want: "info"},
		{name: "flag only", flag: "warning", want: "warning"},
		{name: "flag wins", env: "hint", flag: "warning", want: "warning"},
		{name: "invalid env", env: "warnings", wantErr: true},
		{name: "invalid env under a valid flag", env: "warnings", flag: "warning", wantErr: true},
		{name: "invalid flag", flag: "Error", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATAMITSU_FAIL_ON", tc.env)
			c := &cobra.Command{}
			var value string
			addFailOnFlag(c, &value)
			if tc.flag != "" {
				if err := c.Flags().Set("fail-on", tc.flag); err != nil {
					t.Fatal(err)
				}
			}
			var opts runner.Options
			err := applyFailOn(c, value, &opts)
			if tc.wantErr {
				if _, ok := errors.AsType[exitcode.UsageError](err); !ok {
					t.Fatalf("applyFailOn() = %v, want a usage error", err)
				}
				return
			}
			if err != nil || opts.FailOn != tc.want {
				t.Errorf("applyFailOn() = %v with FailOn %q, want %q", err, opts.FailOn, tc.want)
			}
		})
	}
}
