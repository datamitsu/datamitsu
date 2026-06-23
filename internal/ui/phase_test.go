package ui

import (
	"strings"
	"testing"
)

func TestFormatDurationShort(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{0, "0ms"},
		{999, "999ms"},
		{1000, "1.00s"},
		{1500, "1.50s"},
		{59999, "60.00s"},
		{60000, "1m00s"},
		{125000, "2m05s"},
	}
	for _, tt := range tests {
		if got := FormatDurationShort(tt.ms); got != tt.want {
			t.Errorf("FormatDurationShort(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestRuleLine(t *testing.T) {
	// The plain title drives the width; the colored title is what is rendered.
	got := RuleLine("┏", "BUILD", "BUILD")
	if !strings.Contains(got, "BUILD") {
		t.Errorf("RuleLine() dropped title: %q", got)
	}
	if !strings.Contains(got, "┏") || !strings.Contains(got, "━") {
		t.Errorf("RuleLine() missing corner/fill: %q", got)
	}

	// A very long title still yields at least the minimum fill (no panic / negative repeat).
	long := strings.Repeat("X", 200)
	if out := RuleLine("┗", long, long); !strings.Contains(out, long) {
		t.Errorf("RuleLine() with long title dropped it")
	}
}
