package config

import (
	"strings"
	"testing"
)

func TestSeverityLevel(t *testing.T) {
	cases := map[Severity]uint8{
		SeverityError: 1, SeverityWarning: 2, SeverityInfo: 3, SeverityHint: 4,
		"": 0, "Error": 0, "warnings": 0, "fatal": 0,
	}
	for s, want := range cases {
		if got := s.Level(); got != want {
			t.Errorf("Severity(%q).Level() = %d, want %d", s, got, want)
		}
		if s.Valid() != (want != 0) {
			t.Errorf("Severity(%q).Valid() = %v", s, s.Valid())
		}
	}
}

func TestParseSeverity(t *testing.T) {
	for _, raw := range []string{"error", "warning", "info", "hint"} {
		if s, err := ParseSeverity(raw); err != nil || string(s) != raw {
			t.Errorf("ParseSeverity(%q) = %q, %v", raw, s, err)
		}
	}
	for _, raw := range []string{"", "warnings", "ERROR", "3"} {
		_, err := ParseSeverity(raw)
		if err == nil || !strings.Contains(err.Error(), "must be error, warning, info or hint") {
			t.Errorf("ParseSeverity(%q) error = %v, want one naming the levels", raw, err)
		}
	}
}

// TestEffectiveFailOn: the operation's own threshold (error when unset) is the
// floor, and the global value only ever makes it stricter.
func TestEffectiveFailOn(t *testing.T) {
	cases := []struct {
		name   string
		own    Severity
		global Severity
		want   Severity
	}{
		{"unset, no raise", "", "", SeverityError},
		{"own warning", SeverityWarning, "", SeverityWarning},
		{"raised to warning", "", SeverityWarning, SeverityWarning},
		{"raised to hint", SeverityError, SeverityHint, SeverityHint},
		{"a hint operation is not lowered", SeverityHint, SeverityWarning, SeverityHint},
		{"an error raise changes nothing", SeverityInfo, SeverityError, SeverityInfo},
		{"an invalid raise is ignored", SeverityWarning, "loud", SeverityWarning},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EffectiveFailOn(ToolOperation{FailOn: c.own}, c.global); got != c.want {
				t.Errorf("EffectiveFailOn(%q, %q) = %q, want %q", c.own, c.global, got, c.want)
			}
		})
	}
}

func TestValidateToolsFailOn(t *testing.T) {
	for _, s := range []Severity{"", SeverityError, SeverityWarning, SeverityInfo, SeverityHint} {
		if err := validateOp(ToolOperation{FailOn: s}); err != nil {
			t.Errorf("failOn %q: ValidateTools = %v, want it accepted", s, err)
		}
	}
	for _, s := range []Severity{"warnings", "Error", "critical"} {
		err := validateOp(ToolOperation{FailOn: s})
		if err == nil || !strings.Contains(err.Error(), `operation "lint": failOn "`+string(s)+`" is not a level`) {
			t.Errorf("failOn %q: ValidateTools = %v, want it refused", s, err)
		}
	}
}
