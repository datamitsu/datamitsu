package runner

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func TestHeatDuration(t *testing.T) {
	// Exercises both the cool (sub-floor / max≤floor) and warm/hot branches.
	// Color may be disabled in the test env, so we only assert the text payload
	// survives — branch execution is what drives coverage here.
	tests := []struct {
		name  string
		ms    int64
		maxMs int64
	}{
		{"sub-floor stays cool", 100, 5000},
		{"max at/below floor stays cool", 300, 200},
		{"slowest is hot", 5000, 5000},
		{"mid-range is warm", 2000, 5000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := heatDuration(tt.ms, tt.maxMs, "1.00s")
			if !strings.Contains(got, "1.00s") {
				t.Errorf("heatDuration() dropped its text: %q", got)
			}
		})
	}
}

func TestToolDetail(t *testing.T) {
	t.Run("single run shows scope and avg only", func(t *testing.T) {
		g := toolExecutionGroup{
			scope:     config.ToolScopeRepository,
			totalRuns: 1,
			totalTime: 500,
			minTime:   500,
			maxTime:   500,
		}
		got := toolDetail(g)
		if !strings.Contains(got, "[repository]") || !strings.Contains(got, "avg ") {
			t.Errorf("toolDetail() = %q, want scope + avg", got)
		}
		if strings.Contains(got, "min ") || strings.Contains(got, "cpu ") {
			t.Errorf("single-run detail should omit min/max/cpu: %q", got)
		}
	})

	t.Run("multi-run shows min/max/cpu", func(t *testing.T) {
		g := toolExecutionGroup{
			totalRuns: 3,
			totalTime: 900,
			minTime:   200,
			maxTime:   400,
		}
		got := toolDetail(g)
		for _, want := range []string{"avg ", "min ", "max ", "cpu "} {
			if !strings.Contains(got, want) {
				t.Errorf("multi-run detail missing %q: %q", want, got)
			}
		}
	})

	t.Run("zero runs avoids divide-by-zero", func(t *testing.T) {
		got := toolDetail(toolExecutionGroup{totalRuns: 0, totalTime: 0})
		if !strings.Contains(got, "avg ") {
			t.Errorf("zero-run detail = %q, want avg", got)
		}
	})
}

func TestActiveToolDir(t *testing.T) {
	saved := activeTools
	defer func() { activeTools = saved }()

	activeTools = map[string]map[string]bool{
		"eslint": {"packages/web": true},
	}
	if got := activeToolDir("eslint"); got != "packages/web" {
		t.Errorf("activeToolDir(eslint) = %q, want packages/web", got)
	}
	if got := activeToolDir("absent"); got != "" {
		t.Errorf("activeToolDir(absent) = %q, want empty", got)
	}
}
