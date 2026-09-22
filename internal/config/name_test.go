package config

import (
	"strings"
	"testing"
)

func TestDisplayNameFallsBackToDefault(t *testing.T) {
	for _, tc := range []struct {
		configured string
		name       string
		want       string
	}{
		{name: "unset", want: DefaultName},
		{configured: "   ", name: "blank", want: DefaultName},
		{configured: " @acme/config ", name: "trimmed", want: "@acme/config"},
		{configured: "@acme/config", name: "set", want: "@acme/config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Name: tc.configured}
			if got := cfg.DisplayName(); got != tc.want {
				t.Fatalf("DisplayName() = %q, want %q", got, tc.want)
			}
		})
	}
	var missing *Config
	if got := missing.DisplayName(); got != DefaultName {
		t.Fatalf("nil config DisplayName() = %q, want %q", got, DefaultName)
	}
}

func TestValidateNameRejectsNamesNoSurfaceCanShow(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "unset"},
		{name: "plain", value: "@acme/datamitsu-config"},
		{name: "at the limit", value: strings.Repeat("n", MaxNameLength)},
		{name: "blank", value: "  ", wantErr: "remove it to use the default name"},
		{name: "too long", value: strings.Repeat("n", MaxNameLength+1), wantErr: "exceeds the 80 allowed"},
		{name: "newline", value: "acme\nconfig", wantErr: "control character"},
		{name: "escape", value: "acme\x1b[31m-config", wantErr: "control character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateName(tc.value)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateName(%q) = %v, want nil", tc.value, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateName(%q) = %v, want error containing %q", tc.value, err, tc.wantErr)
			}
		})
	}
}
