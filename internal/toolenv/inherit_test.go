package toolenv

import (
	"slices"
	"testing"
)

func TestCapture(t *testing.T) {
	environ := []string{"GITHUB_ACTIONS=true", "EMPTY=", "CI=1", "AI_AGENT=codex", "WITH_EQ=a=b", "=C:=C:\\"}
	tests := []struct {
		name  string
		names []string
		want  []string
	}{
		{"none asked", nil, nil},
		{"present values, sorted", []string{"GITHUB_ACTIONS", "AI_AGENT"}, []string{"AI_AGENT=codex", "GITHUB_ACTIONS=true"}},
		{"present but empty", []string{"EMPTY"}, []string{"EMPTY="}},
		{"absent adds nothing", []string{"FORCE_COLOR"}, nil},
		{"value carrying =", []string{"WITH_EQ"}, []string{"WITH_EQ=a=b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Capture(environ, tt.names).Pairs(); !slices.Equal(got, tt.want) {
				t.Errorf("Capture(%v).Pairs() = %q, want %q", tt.names, got, tt.want)
			}
		})
	}
}

func TestPairsIsACopy(t *testing.T) {
	in := Capture([]string{"A=1"}, []string{"A"})
	in.Pairs()[0] = "A=2"
	if got := in.Pairs(); !slices.Equal(got, []string{"A=1"}) {
		t.Errorf("Pairs() = %q after a caller wrote to its result", got)
	}
}
