package toolenv

import (
	"slices"
	"testing"
)

func TestResolve(t *testing.T) {
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
			if got := Resolve(environ, tt.names); !slices.Equal(got, tt.want) {
				t.Errorf("Resolve(%v) = %q, want %q", tt.names, got, tt.want)
			}
		})
	}
}
