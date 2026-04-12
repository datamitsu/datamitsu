package cmd

import (
	"testing"
)

func TestCheckCommandRegistered(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "check [files...]" {
			found = true
			break
		}
	}
	if !found {
		t.Error("check command not registered with rootCmd")
	}
}

func TestCheckCommandFlags(t *testing.T) {
	flags := checkCmd.Flags()

	tests := []struct {
		name         string
		defaultValue string
	}{
		{"explain", ""},
		{"file-scoped", "false"},
		{"tools", ""},
	}

	for _, tt := range tests {
		f := flags.Lookup(tt.name)
		if f == nil {
			t.Errorf("flag %q not found on check command", tt.name)
			continue
		}
		if f.DefValue != tt.defaultValue {
			t.Errorf("flag %q default = %q, want %q", tt.name, f.DefValue, tt.defaultValue)
		}
	}
}

func TestCheckCommandExplainNoOptDefVal(t *testing.T) {
	f := checkCmd.Flags().Lookup("explain")
	if f == nil {
		t.Fatal("explain flag not found")
	}
	if f.NoOptDefVal != "summary" {
		t.Errorf("explain NoOptDefVal = %q, want %q", f.NoOptDefVal, "summary")
	}
}
