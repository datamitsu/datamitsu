package lsp

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// The session policy is ambient — it applies to every save — so it may narrow
// what runs but never widen it. "repo" is not a legal editor policy at all: a
// repository-wide fix on save is never acceptable.
func TestEditorWidenTo(t *testing.T) {
	tests := []struct {
		set  string
		want config.WidenTo
	}{
		{"target", config.WidenToTarget},
		{"unit", config.WidenToUnit},
		{"repo", config.WidenToUnit},
		{"", config.WidenToUnit},
		{"nonsense", config.WidenToUnit},
	}

	for _, tt := range tests {
		t.Run("policy="+tt.set, func(t *testing.T) {
			t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", tt.set)
			if got := editorWidenTo(); got != tt.want {
				t.Errorf("editorWidenTo() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEditorTimeoutMs(t *testing.T) {
	tests := []struct {
		set  string
		want int
	}{
		{"", 15000},
		{"250", 250},
		{"0", 0},
		{"-1", 15000},
		{"soon", 15000},
		{"18446744073710", 15000},
	}

	for _, tt := range tests {
		t.Run("timeout="+tt.set, func(t *testing.T) {
			t.Setenv("DATAMITSU_LSP_FORMAT_TIMEOUT_MS", tt.set)
			if got := editorTimeoutMs(); got != tt.want {
				t.Errorf("editorTimeoutMs() = %d, want %d", got, tt.want)
			}
		})
	}
}

// initializationOptions are lenient: whatever the editor sends, initialize
// succeeds, a rejected value warns and leaves its key on the environment or the
// default, and only accepted values reach the policy.
func TestResolveFormatPolicy(t *testing.T) {
	base := formatPolicy{WidenTo: config.WidenToUnit, TimeoutMs: 15000, Tools: map[string]bool{}}
	isTool := func(name string) bool { return name == "eslint" || name == "prettier" }

	tests := []struct {
		name      string
		options   string // raw initializationOptions; "" means absent
		want      formatPolicy
		wantFrom  []string
		wantWarns []string // substrings, one per expected warning, in order
	}{
		{name: "absent options", options: "", want: base},
		{name: "null options", options: "null", want: base},
		{name: "empty object", options: "{}", want: base},
		{
			name:      "options not an object",
			options:   `["format"]`,
			want:      base,
			wantWarns: []string{"initializationOptions is not an object"},
		},
		{
			name:    "reserved and unknown top-level keys are silent",
			options: `{"diagnostics": {"enabled": true}, "future": 1}`,
			want:    base,
		},
		{name: "null format", options: `{"format": null}`, want: base},
		{
			name:      "format not an object",
			options:   `{"format": "unit"}`,
			want:      base,
			wantWarns: []string{"initializationOptions.format is not an object"},
		},
		{
			name:     "widenTo target",
			options:  `{"format": {"widenTo": "target"}}`,
			want:     formatPolicy{WidenTo: config.WidenToTarget, TimeoutMs: 15000, Tools: map[string]bool{}},
			wantFrom: []string{"widenTo"},
		},
		{
			name:      "widenTo repo is rejected",
			options:   `{"format": {"widenTo": "repo"}}`,
			want:      base,
			wantWarns: []string{`"repo" is not allowed in the editor: a repository-wide fix never runs on save`},
		},
		{
			name:      "widenTo is case-sensitive",
			options:   `{"format": {"widenTo": "Unit"}}`,
			want:      base,
			wantWarns: []string{`format.widenTo: expected "target" or "unit", got "Unit"`},
		},
		{
			name:      "widenTo of the wrong type",
			options:   `{"format": {"widenTo": 1}}`,
			want:      base,
			wantWarns: []string{"format.widenTo: expected"},
		},
		{
			name:     "timeoutMs zero disables the watchdog",
			options:  `{"format": {"timeoutMs": 0}}`,
			want:     formatPolicy{WidenTo: config.WidenToUnit, TimeoutMs: 0, Tools: map[string]bool{}},
			wantFrom: []string{"timeoutMs"},
		},
		{
			name:     "timeoutMs in exponent form is still an integer",
			options:  `{"format": {"timeoutMs": 1.5e3}}`,
			want:     formatPolicy{WidenTo: config.WidenToUnit, TimeoutMs: 1500, Tools: map[string]bool{}},
			wantFrom: []string{"timeoutMs"},
		},
		{
			name:      "negative timeoutMs",
			options:   `{"format": {"timeoutMs": -1}}`,
			want:      base,
			wantWarns: []string{"format.timeoutMs: expected a non-negative integer, got -1; using 15000"},
		},
		{
			name:      "fractional timeoutMs",
			options:   `{"format": {"timeoutMs": 2.5}}`,
			want:      base,
			wantWarns: []string{"format.timeoutMs: expected a non-negative integer"},
		},
		{
			name:     "the largest timeoutMs a Duration holds",
			options:  `{"format": {"timeoutMs": 9223372036854}}`,
			want:     formatPolicy{WidenTo: config.WidenToUnit, TimeoutMs: 9223372036854, Tools: map[string]bool{}},
			wantFrom: []string{"timeoutMs"},
		},
		{
			// It would wrap to a negative Duration and disable the watchdog.
			name:      "timeoutMs past what a Duration holds",
			options:   `{"format": {"timeoutMs": 1e13}}`,
			want:      base,
			wantWarns: []string{"format.timeoutMs: 1e13 is more than the largest limit, 9223372036854; using 15000"},
		},
		{
			// It would wrap to about 448µs and stop every save after one group.
			name:      "timeoutMs that wraps to microseconds",
			options:   `{"format": {"timeoutMs": 18446744073710}}`,
			want:      base,
			wantWarns: []string{"format.timeoutMs: 18446744073710 is more than the largest limit"},
		},
		{
			name:      "timeoutMs as a string",
			options:   `{"format": {"timeoutMs": "15000"}}`,
			want:      base,
			wantWarns: []string{"format.timeoutMs: expected a non-negative integer"},
		},
		{
			name:     "tools",
			options:  `{"format": {"tools": {"eslint": true, "prettier": false}}}`,
			want:     formatPolicy{WidenTo: config.WidenToUnit, TimeoutMs: 15000, Tools: map[string]bool{"eslint": true, "prettier": false}},
			wantFrom: []string{"tools"},
		},
		{
			name:      "unknown tool has no effect",
			options:   `{"format": {"tools": {"eslint": true, "nonesuch": false}}}`,
			want:      formatPolicy{WidenTo: config.WidenToUnit, TimeoutMs: 15000, Tools: map[string]bool{"eslint": true}},
			wantFrom:  []string{"tools"},
			wantWarns: []string{"format.tools.nonesuch: unknown tool"},
		},
		{
			name:      "non-boolean tool entry is ignored",
			options:   `{"format": {"tools": {"eslint": "yes", "prettier": true}}}`,
			want:      formatPolicy{WidenTo: config.WidenToUnit, TimeoutMs: 15000, Tools: map[string]bool{"prettier": true}},
			wantFrom:  []string{"tools"},
			wantWarns: []string{`format.tools.eslint: expected a boolean, got "yes"`},
		},
		{
			name:      "tools not an object",
			options:   `{"format": {"tools": ["eslint"]}}`,
			want:      base,
			wantWarns: []string{"format.tools: expected an object"},
		},
		{
			name:      "unknown key inside format",
			options:   `{"format": {"widenTo": "target", "timeout": 10}}`,
			want:      formatPolicy{WidenTo: config.WidenToTarget, TimeoutMs: 15000, Tools: map[string]bool{}},
			wantFrom:  []string{"widenTo"},
			wantWarns: []string{"format.timeout: unknown key"},
		},
		{
			name:     "null values read as not set",
			options:  `{"format": {"widenTo": null, "timeoutMs": null, "tools": {"eslint": null}}}`,
			want:     base,
			wantFrom: []string{"tools"},
		},
		{
			name:     "every key at once",
			options:  `{"format": {"widenTo": "target", "timeoutMs": 500, "tools": {"eslint": true}}}`,
			want:     formatPolicy{WidenTo: config.WidenToTarget, TimeoutMs: 500, Tools: map[string]bool{"eslint": true}},
			wantFrom: []string{"timeoutMs", "tools", "widenTo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw json.RawMessage
			if tt.options != "" {
				raw = json.RawMessage(tt.options)
			}
			got := resolveFormatPolicy(raw, base, isTool)

			if got.Policy.WidenTo != tt.want.WidenTo || got.Policy.TimeoutMs != tt.want.TimeoutMs ||
				!maps.Equal(got.Policy.Tools, tt.want.Tools) {
				t.Errorf("policy = %+v, want %+v", got.Policy, tt.want)
			}
			if got.Policy.Tools == nil {
				t.Error("Tools must never be nil: the echo promises {}")
			}
			if !slices.Equal(got.FromOptions, tt.wantFrom) {
				t.Errorf("FromOptions = %v, want %v", got.FromOptions, tt.wantFrom)
			}
			if len(got.Warnings) != len(tt.wantWarns) {
				t.Fatalf("warnings = %q, want %d matching %q", got.Warnings, len(tt.wantWarns), tt.wantWarns)
			}
			for i, want := range tt.wantWarns {
				if !strings.Contains(got.Warnings[i], want) {
					t.Errorf("warning %d = %q, want it to contain %q", i, got.Warnings[i], want)
				}
			}
		})
	}
}

// Per key: initializationOptions, then the environment, then the default. A
// rejected option falls back to the environment, not straight to the default.
func TestFormatPolicyPrecedence(t *testing.T) {
	isTool := func(string) bool { return true }

	t.Run("defaults", func(t *testing.T) {
		t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", "")
		t.Setenv("DATAMITSU_LSP_FORMAT_TIMEOUT_MS", "")
		got := resolveFormatPolicy(nil, envFormatPolicy(), isTool).Policy
		if got.WidenTo != config.WidenToUnit || got.TimeoutMs != 15000 || len(got.Tools) != 0 {
			t.Errorf("policy = %+v, want unit / 15000 / {}", got)
		}
	})

	t.Run("environment over defaults", func(t *testing.T) {
		t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", "target")
		t.Setenv("DATAMITSU_LSP_FORMAT_TIMEOUT_MS", "750")
		got := resolveFormatPolicy(nil, envFormatPolicy(), isTool).Policy
		if got.WidenTo != config.WidenToTarget || got.TimeoutMs != 750 {
			t.Errorf("policy = %+v, want target / 750", got)
		}
	})

	t.Run("initializationOptions over the environment, per key", func(t *testing.T) {
		t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", "target")
		t.Setenv("DATAMITSU_LSP_FORMAT_TIMEOUT_MS", "750")
		raw := json.RawMessage(`{"format": {"widenTo": "unit"}}`)
		got := resolveFormatPolicy(raw, envFormatPolicy(), isTool).Policy
		if got.WidenTo != config.WidenToUnit {
			t.Errorf("widenTo = %q, want the option's unit", got.WidenTo)
		}
		if got.TimeoutMs != 750 {
			t.Errorf("timeoutMs = %d, want the environment's 750", got.TimeoutMs)
		}
	})

	t.Run("a rejected option keeps the environment", func(t *testing.T) {
		t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", "target")
		raw := json.RawMessage(`{"format": {"widenTo": "repo"}}`)
		got := resolveFormatPolicy(raw, envFormatPolicy(), isTool)
		if got.Policy.WidenTo != config.WidenToTarget {
			t.Errorf("widenTo = %q, want the environment's target", got.Policy.WidenTo)
		}
		if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], `using "target"`) {
			t.Errorf("warnings = %q, want one naming the fallback", got.Warnings)
		}
	})
}

func TestPolicySummary(t *testing.T) {
	tests := []struct {
		name string
		res  policyResolution
		want string
	}{
		{
			"nothing from the editor",
			policyResolution{Policy: formatPolicy{WidenTo: config.WidenToUnit, TimeoutMs: 15000, Tools: map[string]bool{}}},
			"format policy: widenTo=unit timeoutMs=15000 tools={} (from initializationOptions: none)",
		},
		{
			"keys from the editor",
			policyResolution{
				Policy:      formatPolicy{WidenTo: config.WidenToTarget, TimeoutMs: 0, Tools: map[string]bool{"prettier": false, "eslint": true}},
				FromOptions: []string{"tools", "widenTo"},
			},
			`format policy: widenTo=target timeoutMs=0 tools={"eslint":true,"prettier":false} (from initializationOptions: tools, widenTo)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.res.summary(); got != tt.want {
				t.Errorf("summary() =\n  %s\nwant\n  %s", got, tt.want)
			}
		})
	}
}

// The session policy is ambient, so it is clamped to the project's. A project
// that declared fix: "target" used to get the editor default of "unit" anyway,
// and saving one file ran an in-place, unit-granularity formatter over the whole
// project — precisely the blast radius that setting exists to prevent.
func TestFilterPlanForEditorClampsToTheProjectPolicy(t *testing.T) {
	newPlan := func() *tooling.ExecutionPlan {
		return &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{
			{ToolName: "gofmt", OpConfig: unitOp, ProjectPath: "/repo/pkg"},
			{ToolName: "shfmt", OpConfig: fileOp, ProjectPath: "/repo/pkg"},
		}}}}
	}

	t.Run("project target overrides the looser session default", func(t *testing.T) {
		plan := newPlan()
		filterPlanForEditor(plan, "/repo/pkg/a.go", config.WidenToTarget, sessionPolicy(config.WidenToUnit, nil))

		got := toolNames(plan)
		if len(got) != 1 || got[0] != "shfmt" {
			t.Errorf("kept %v, want only the file-granularity task", got)
		}
	})

	t.Run("a looser project policy does not widen the session", func(t *testing.T) {
		plan := newPlan()
		filterPlanForEditor(plan, "/repo/pkg/a.go", config.WidenToRepo, sessionPolicy(config.WidenToTarget, nil))

		got := toolNames(plan)
		if len(got) != 1 || got[0] != "shfmt" {
			t.Errorf("kept %v; the narrower of the two must win", got)
		}
	})

	t.Run("agreeing policies keep the unit task", func(t *testing.T) {
		plan := newPlan()
		filterPlanForEditor(plan, "/repo/pkg/a.go", config.WidenToUnit, sessionPolicy(config.WidenToUnit, nil))

		if got := toolNames(plan); len(got) != 2 {
			t.Errorf("kept %v, want both tasks", got)
		}
	})
}

// A unit operation with no globs is planned once per project regardless of what
// was saved, so without a containment check saving one file runs it in every
// module — and these tools fix in place, rewriting files the editor never opened.
func TestTaskCoversPath(t *testing.T) {
	unit := filepath.FromSlash("/repo/svc/a")

	tests := []struct {
		name string
		task tooling.Task
		path string
		want bool
	}{
		{
			"file inside the unit",
			tooling.Task{ProjectPath: unit},
			filepath.Join(unit, "main.go"), true,
		},
		{
			"file in a nested directory",
			tooling.Task{ProjectPath: unit},
			filepath.Join(unit, "internal", "x.go"), true,
		},
		{
			"file in a sibling unit",
			tooling.Task{ProjectPath: unit},
			filepath.FromSlash("/repo/svc/b/main.go"), false,
		},
		{
			// "/repo/svc/ab" must not count as inside "/repo/svc/a".
			"sibling with a shared name prefix",
			tooling.Task{ProjectPath: unit},
			filepath.FromSlash("/repo/svc/ab/main.go"), false,
		},
		{
			"file above the unit",
			tooling.Task{ProjectPath: unit},
			filepath.FromSlash("/repo/main.go"), false,
		},
		{
			// No project path means the task is not unit-bound.
			"task with no unit covers everything",
			tooling.Task{},
			filepath.FromSlash("/repo/anywhere.go"), true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskCoversPath(tt.task, tt.path); got != tt.want {
				t.Errorf("taskCoversPath(%q, %q) = %v, want %v",
					tt.task.ProjectPath, tt.path, got, tt.want)
			}
		})
	}
}

func sessionPolicy(widen config.WidenTo, tools map[string]bool) formatPolicy {
	if tools == nil {
		tools = map[string]bool{}
	}
	return formatPolicy{WidenTo: widen, TimeoutMs: 15000, Tools: tools}
}
