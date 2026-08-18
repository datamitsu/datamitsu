package tooling

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/project"
	"github.com/datamitsu/datamitsu/internal/timing"
)

// fakePlatformChecker reports the apps in unavailable as having no binary for the
// host; everything else is available.
type fakePlatformChecker struct {
	unavailable map[string]string // app -> host detail
}

func (f fakePlatformChecker) BinaryAvailable(app string) (bool, string) {
	if detail, ok := f.unavailable[app]; ok {
		return false, detail
	}
	return true, ""
}

func repoScopedTool(name, app string) config.Tool {
	return config.Tool{
		Name: name,
		Operations: map[config.OperationType]config.ToolOperation{
			config.OpLint: {App: app, Scope: config.ToolScopeRepository},
		},
	}
}

func newSkipTestPlanner(tools config.MapOfTools) *Planner {
	return &Planner{
		rootPath:         "/repo",
		cwdPath:          "/repo",
		detectedTypes:    []string{},
		tools:            tools,
		cachedFiles:      []string{},
		cachedProjects:   []project.ProjectLocation{},
		cacheInitialized: true,
		timings:          timing.New(),
	}
}

func TestCollectTasks_ConfigSkip(t *testing.T) {
	tool := repoScopedTool("trufflehog", "trufflehog")
	tool.Skip = true
	tool.SkipReason = "runs in CI only"
	p := newSkipTestPlanner(config.MapOfTools{"trufflehog": tool})

	tasks, skipped := p.collectTasks(context.Background(), config.OpLint, Selection{})

	if len(tasks) != 0 {
		t.Fatalf("expected no tasks for a skip:true tool, got %d", len(tasks))
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped tool, got %d", len(skipped))
	}
	got := skipped[0]
	if got.ToolName != "trufflehog" || got.Reason != SkipReasonConfig || got.Detail != "runs in CI only" {
		t.Errorf("unexpected skipped entry: %+v", got)
	}
	if got.Operation != config.OpLint {
		t.Errorf("expected operation lint, got %q", got.Operation)
	}
}

func TestCollectTasks_PlatformSkip(t *testing.T) {
	p := newSkipTestPlanner(config.MapOfTools{"typstyle": repoScopedTool("typstyle", "typstyle")})
	p.SetPlatformChecker(fakePlatformChecker{unavailable: map[string]string{"typstyle": "linux/arm64/musl"}})

	tasks, skipped := p.collectTasks(context.Background(), config.OpLint, Selection{})

	if len(tasks) != 0 {
		t.Fatalf("expected no tasks for a platform-unsupported tool, got %d", len(tasks))
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped tool, got %d", len(skipped))
	}
	got := skipped[0]
	if got.Reason != SkipReasonUnsupportedPlatform || got.Detail != "linux/arm64/musl" {
		t.Errorf("unexpected skipped entry: %+v", got)
	}
}

func TestCollectTasks_AvailableToolNotSkipped(t *testing.T) {
	p := newSkipTestPlanner(config.MapOfTools{"shellcheck": repoScopedTool("shellcheck", "shellcheck")})
	p.SetPlatformChecker(fakePlatformChecker{unavailable: map[string]string{"typstyle": "linux/arm64/musl"}})

	tasks, skipped := p.collectTasks(context.Background(), config.OpLint, Selection{})

	if len(tasks) != 1 {
		t.Fatalf("expected 1 task for an available tool, got %d", len(tasks))
	}
	if len(skipped) != 0 {
		t.Fatalf("expected no skips, got %d", len(skipped))
	}
}

// A skip:true tool that does not apply to the detected project types must not be
// reported as skipped — applicability is checked before the skip.
func TestCollectTasks_InapplicableSkipNotReported(t *testing.T) {
	tool := repoScopedTool("go-thing", "go-thing")
	tool.Skip = true
	tool.ProjectTypes = []string{"golang"} // not in detectedTypes
	p := newSkipTestPlanner(config.MapOfTools{"go-thing": tool})

	tasks, skipped := p.collectTasks(context.Background(), config.OpLint, Selection{})

	if len(tasks) != 0 || len(skipped) != 0 {
		t.Fatalf("expected no tasks and no skips for an inapplicable tool, got %d tasks / %d skips", len(tasks), len(skipped))
	}
}

// A skip:true tool that lacks the requested operation is not reported for that op.
func TestCollectTasks_SkipScopedToOperation(t *testing.T) {
	tool := repoScopedTool("trufflehog", "trufflehog") // lint only
	tool.Skip = true
	p := newSkipTestPlanner(config.MapOfTools{"trufflehog": tool})

	_, skippedFix := p.collectTasks(context.Background(), config.OpFix, Selection{})
	if len(skippedFix) != 0 {
		t.Errorf("skip:true tool without a fix op should not be reported in fix, got %d", len(skippedFix))
	}

	_, skippedLint := p.collectTasks(context.Background(), config.OpLint, Selection{})
	if len(skippedLint) != 1 {
		t.Errorf("expected the skip to be reported in lint, got %d", len(skippedLint))
	}
}

func TestPlan_SkippedFilteredBySelectedTools(t *testing.T) {
	trufflehog := repoScopedTool("trufflehog", "trufflehog")
	trufflehog.Skip = true
	tools := config.MapOfTools{
		"trufflehog": trufflehog,
		"shellcheck": repoScopedTool("shellcheck", "shellcheck"),
	}
	p := newSkipTestPlanner(tools)

	// Selecting only shellcheck drops trufflehog's skip from the plan.
	plan, err := p.Plan(context.Background(), config.OpLint, Selection{}, []string{"shellcheck"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Skipped) != 0 {
		t.Errorf("expected skipped filtered out by --tools, got %+v", plan.Skipped)
	}

	// Selecting trufflehog keeps it.
	plan, err = p.Plan(context.Background(), config.OpLint, Selection{}, []string{"trufflehog"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].ToolName != "trufflehog" {
		t.Errorf("expected trufflehog skip retained, got %+v", plan.Skipped)
	}
}

func TestSkippedTool_ReasonText(t *testing.T) {
	cases := []struct {
		name string
		in   SkippedTool
		want string
	}{
		{"config with detail", SkippedTool{Reason: SkipReasonConfig, Detail: "runs in CI only"}, "runs in CI only"},
		{"config no detail", SkippedTool{Reason: SkipReasonConfig}, "disabled in config"},
		{"platform with host", SkippedTool{Reason: SkipReasonUnsupportedPlatform, Detail: "linux/arm64/musl"}, "no binary for linux/arm64/musl"},
		{"platform no host", SkippedTool{Reason: SkipReasonUnsupportedPlatform}, "no binary for this platform"},
	}
	for _, tc := range cases {
		if got := tc.in.ReasonText(); got != tc.want {
			t.Errorf("%s: ReasonText() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSkipReason_String(t *testing.T) {
	if SkipReasonConfig.String() != "config" {
		t.Errorf("config string = %q", SkipReasonConfig.String())
	}
	if SkipReasonUnsupportedPlatform.String() != "unsupported-platform" {
		t.Errorf("platform string = %q", SkipReasonUnsupportedPlatform.String())
	}
}

func TestJSONFormatter_IncludesSkipped(t *testing.T) {
	plan := &ExecutionPlan{
		Skipped: []SkippedTool{
			{ToolName: "trufflehog", Operation: config.OpLint, Reason: SkipReasonConfig, Detail: "runs in CI only"},
			{ToolName: "typstyle", Operation: config.OpLint, Reason: SkipReasonUnsupportedPlatform, Detail: "linux/arm64/musl"},
		},
	}
	out := NewJSONFormatter().Format(plan, "/repo", "/repo", config.OpLint)

	var parsed PlanJSON
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(parsed.Skipped) != 2 {
		t.Fatalf("expected 2 skipped in JSON, got %d", len(parsed.Skipped))
	}
	if parsed.Skipped[0].Reason != "config" || parsed.Skipped[0].Detail != "runs in CI only" {
		t.Errorf("unexpected skipped[0]: %+v", parsed.Skipped[0])
	}
	if parsed.Skipped[1].Reason != "unsupported-platform" {
		t.Errorf("unexpected skipped[1] reason: %q", parsed.Skipped[1].Reason)
	}
}

func TestSummaryFormatter_ShowsSkippedWhenNoGroups(t *testing.T) {
	plan := &ExecutionPlan{
		Skipped: []SkippedTool{{ToolName: "trufflehog", Reason: SkipReasonConfig, Detail: "runs in CI only"}},
	}
	out := NewSummaryFormatter().Format(plan, "/repo", "/repo", config.OpLint)
	if !strings.Contains(out, "Skipped (1)") || !strings.Contains(out, "trufflehog") || !strings.Contains(out, "runs in CI only") {
		t.Errorf("summary missing skipped section: %s", out)
	}
}
