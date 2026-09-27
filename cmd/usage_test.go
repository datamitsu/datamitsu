package cmd

import (
	"errors"
	"testing"

	"github.com/datamitsu/datamitsu/internal/exitcode"

	"github.com/spf13/cobra"
)

func exitCodeOf(err error) int {
	if coded, ok := errors.AsType[CodedError](err); ok {
		return coded.ExitCode()
	}
	return 1
}

// A refused positional argument keeps cobra's message and exits with the usage
// code; an accepted one passes through untouched.
func TestUsageArgs(t *testing.T) {
	check := usageArgs(cobra.ExactArgs(1))
	cmd := &cobra.Command{Use: "x"}

	err := check(cmd, nil)
	if err == nil || exitCodeOf(err) != exitcode.Usage {
		t.Fatalf("usageArgs(ExactArgs(1)) with no argument = %v, want a usage error", err)
	}
	if want := cobra.ExactArgs(1)(cmd, nil).Error(); err.Error() != want {
		t.Errorf("message = %q, want cobra's %q", err.Error(), want)
	}
	if err := check(cmd, []string{"one"}); err != nil {
		t.Errorf("usageArgs(ExactArgs(1)) with one argument = %v, want nil", err)
	}
}

// Every flag cobra cannot parse, on any command, exits with the usage code: the
// root's flag error function is inherited by its subcommands.
func TestFlagErrorsAreUsageErrors(t *testing.T) {
	for _, c := range []*cobra.Command{rootCmd, lintCmd, checkCmd, fixCmd} {
		err := c.FlagErrorFunc()(c, errors.New("unknown flag: --bogus"))
		if exitCodeOf(err) != exitcode.Usage || err.Error() != "unknown flag: --bogus" {
			t.Errorf("%s: flag error = %v (exit %d), want the message with exit %d", c.Name(), err, exitCodeOf(err), exitcode.Usage)
		}
	}
}

// A missing required flag and flags that cannot be combined are refused with
// the usage code; cobra would return both without one.
func TestValidateFlagConstraints(t *testing.T) {
	newCmd := func() *cobra.Command {
		c := &cobra.Command{Use: "x"}
		c.Flags().String("output", "", "")
		c.Flags().Int("port", 0, "")
		c.Flags().Bool("print", false, "")
		_ = c.MarkFlagRequired("output")
		c.MarkFlagsMutuallyExclusive("port", "print")
		return c
	}
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "satisfied", args: []string{"--output", "x"}, want: 0},
		{name: "required flag missing", args: nil, want: exitcode.Usage},
		{name: "exclusive flags", args: []string{"--output", "x", "--port", "1", "--print"}, want: exitcode.Usage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCmd()
			if err := c.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := validateFlagConstraints(c, nil)
			switch {
			case tt.want == 0 && err != nil:
				t.Errorf("validateFlagConstraints() = %v, want nil", err)
			case tt.want != 0 && exitCodeOf(err) != tt.want:
				t.Errorf("validateFlagConstraints() = %v (exit %d), want exit %d", err, exitCodeOf(err), tt.want)
			}
		})
	}
}

// An invalid DATAMITSU_FAIL_FAST is a caller mistake, refused with the usage code.
func TestApplyFailFastInvalidEnvIsAUsageError(t *testing.T) {
	t.Setenv("DATAMITSU_FAIL_FAST", "maybe")
	var value bool
	c := &cobra.Command{Use: "x"}
	addFailFastFlag(c, &value)
	if err := applyFailFast(c, value, nil); exitCodeOf(err) != exitcode.Usage {
		t.Errorf("applyFailFast() = %v, want a usage error", err)
	}
}
