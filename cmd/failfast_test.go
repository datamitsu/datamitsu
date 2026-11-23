package cmd

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/runner"

	"github.com/spf13/cobra"
)

// The flag is passed on only when it was given, so the runner can tell an
// explicit value from the default; without it DATAMITSU_FAIL_FAST decides. A
// value the variable does not accept is refused here, flag or not.
func TestFailFastOption(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     string
		want    *bool
		wantErr string
	}{
		{args: nil, env: "", wantErr: "", name: "default", want: nil},
		{env: "", wantErr: "", name: "bare flag", args: []string{"--fail-fast"}, want: new(true)},
		{env: "", wantErr: "", name: "flag false", args: []string{"--fail-fast=false"}, want: new(false)},
		{args: nil, wantErr: "", name: "env defers to the runner", env: "false", want: nil},
		{wantErr: "", name: "flag wins over env", args: []string{"--fail-fast"}, env: "false", want: new(true)},
		{args: nil, want: nil, name: "invalid env", env: "yes", wantErr: "DATAMITSU_FAIL_FAST"},
		{want: nil, name: "flag does not excuse invalid env", args: []string{"--fail-fast=false"}, env: "yes", wantErr: "DATAMITSU_FAIL_FAST"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATAMITSU_FAIL_FAST", tt.env)
			var value bool
			cmd := &cobra.Command{Aliases: nil, SuggestFor: nil, Short: "", GroupID: "", Long: "", Example: "", ValidArgs: nil, ValidArgsFunction: nil, Args: nil, ArgAliases: nil, BashCompletionFunction: "", Deprecated: "", Annotations: nil, Version: "", PersistentPreRun: nil, PersistentPreRunE: nil, PreRun: nil, PreRunE: nil, Run: nil, RunE: nil, PostRun: nil, PostRunE: nil, PersistentPostRun: nil, PersistentPostRunE: nil, FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: false, UnknownFlagsHandling: 0}, CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: false, DisableNoDescFlag: false, DisableDescriptions: false, HiddenDefaultCmd: false, DefaultShellCompDirective: nil}, TraverseChildren: false, Hidden: false, SilenceErrors: false, SilenceUsage: false, DisableFlagParsing: false, DisableAutoGenTag: false, DisableFlagsInUseLine: false, DisableSuggestions: false, SuggestionsMinimumDistance: 0, Use: "x"}
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
