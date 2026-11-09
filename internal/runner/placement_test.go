package runner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/report/render"
)

// placementFixture is a git repository whose root is clean but that carries
// Datamitsu-owned config artifacts below the root: a nested config file, a
// nested .datamitsu directory and a nested .datamitsuignore.
func placementFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Legal at the root:
	write("datamitsu.config.ts", "export default {}\n")
	write(".datamitsuignore", "*.go: golangci-lint\n")
	write(".datamitsu/keep", "x\n")
	// Violations below the root:
	write("pkg/datamitsu.config.js", "export default {}\n")
	write("sub/.datamitsu/nested", "x\n")
	write("deep/nest/.datamitsuignore", "*.ts: eslint\n")
	t.Chdir(root)
	return root
}

func runOnFixture(t *testing.T, ops []config.OperationType, opts Options) error {
	t.Helper()
	return RunSequential(ops, nil, "", false, "", false, opts,
		func() (*config.Config, string, error) {
			return &config.Config{
				Tools:        config.MapOfTools{"golangci-lint": {Name: "golangci-lint"}},
				ProjectTypes: config.MapOfProjectTypes{},
			}, "", nil
		},
	)
}

// A lint run reports Datamitsu config artifacts below the git root and fails;
// the report holds them under the pseudo tool "datamitsu" with the
// config-placement code, and the same names at the root are legal.
func TestConfigPlacementBelowGitRootFailsLint(t *testing.T) {
	placementFixture(t)

	reportPath := filepath.Join(t.TempDir(), "run.json")
	err := runOnFixture(t, []config.OperationType{config.OpLint}, Options{
		Reports: []render.Spec{{Format: "json", Path: reportPath}},
	})
	if err == nil {
		t.Fatal("lint with misplaced configs must fail")
	}
	if !strings.Contains(err.Error(), "config placement violation") {
		t.Errorf("lint error = %v, want the placement violation", err)
	}

	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Operations []struct {
			Name  string `json:"name"`
			Tools []struct {
				Name        string `json:"name"`
				Complete    bool   `json:"complete"`
				Invocations []struct {
					Findings []struct {
						Code     string `json:"code"`
						Severity string `json:"severity"`
						Gates    bool   `json:"gates"`
						Location struct {
							Path string `json:"path"`
						} `json:"location"`
					} `json:"findings"`
				} `json:"invocations"`
			} `json:"tools"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("report is not JSON: %v", err)
	}
	var findings []string
	for _, op := range doc.Operations {
		if op.Name != "lint" {
			continue
		}
		for _, tr := range op.Tools {
			if tr.Name != "datamitsu" {
				continue
			}
			if !tr.Complete {
				t.Error("the pseudo tool run must be complete")
			}
			for _, inv := range tr.Invocations {
				for _, f := range inv.Findings {
					if f.Code != "config-placement" || f.Severity != "error" || !f.Gates {
						t.Errorf("finding = %+v, want a gated config-placement error", f)
					}
					findings = append(findings, filepath.ToSlash(f.Location.Path))
				}
			}
		}
	}
	slices.Sort(findings)
	want := []string{"deep/nest/.datamitsuignore", "pkg/datamitsu.config.js", "sub/.datamitsu"}
	if !slices.Equal(findings, want) {
		t.Errorf("report findings = %v, want %v", findings, want)
	}

	// The check is silent in a fix-only run: it never moves or deletes.
	if err := runOnFixture(t, []config.OperationType{config.OpFix}, Options{}); err != nil {
		t.Errorf("fix-only run must not fail on placement: %v", err)
	}
}

// A fix run with real, applicable tasks must neither fail on nor print the
// placement findings: the lint operation owns them (regression guard for the
// fix phase of check leaking them under fail-fast).
func TestConfigPlacementSilentInFixWithTasks(t *testing.T) {
	placementFixture(t)
	if err := os.WriteFile("go.mod", []byte("module demo\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Apps: binmanager.MapOfApps{
			"echo": {Shell: &binmanager.AppConfigShell{Name: "echo"}},
		},
		Tools: config.MapOfTools{
			"echo": {
				Name: "echo",
				Operations: map[config.OperationType]config.ToolOperation{
					config.OpFix:  {App: "echo", Args: []string{"fix"}, Scope: config.ToolScopeRepository},
					config.OpLint: {App: "echo", Args: []string{"lint"}, Scope: config.ToolScopeRepository},
				},
			},
		},
		ProjectTypes: config.MapOfProjectTypes{
			"go": {Markers: []string{"go.mod"}},
		},
	}
	run := func(ops []config.OperationType) error {
		t.Helper()
		return RunSequential(ops, nil, "", false, "", false, Options{}, func() (*config.Config, string, error) {
			return cfg, "", nil
		})
	}
	if err := run([]config.OperationType{config.OpFix}); err != nil {
		t.Errorf("fix-only run with tasks must not fail on placement: %v", err)
	}
	// The lint operation of a check still owns the findings and fails.
	if err := run([]config.OperationType{config.OpFix, config.OpLint}); err == nil {
		t.Error("check must fail on placement violations")
	}
}

// Agent output must count the failing core tool even on the
// no-applicable-tools path: `lint --output agent` with only placement
// violations reports 1 tool, 1 failed — not 0/0.
func TestConfigPlacementAgentOutputCountsCoreTool(t *testing.T) {
	placementFixture(t)

	out := captureStdout(t, func() {
		if err := RunSequential([]config.OperationType{config.OpLint}, nil, "", false, "", false,
			Options{Output: OutputAgent},
			func() (*config.Config, string, error) {
				return &config.Config{
					Tools:        config.MapOfTools{"golangci-lint": {Name: "golangci-lint"}},
					ProjectTypes: config.MapOfProjectTypes{},
				}, "", nil
			},
		); err == nil {
			t.Error("lint with misplaced configs must fail")
		}
	})
	if !strings.Contains(out, "1 tools · 0 runs · 1 failed") {
		t.Errorf("agent summary lacks the core tool's counts:\n%s", out)
	}
	if !strings.Contains(out, "config-placement") {
		t.Errorf("agent output lacks the placement findings:\n%s", out)
	}
}

// The same run in a clean repository finds nothing and passes.
func TestConfigPlacementCleanRootPasses(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	for _, rel := range []string{"datamitsu.config.ts", "main.go"} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".datamitsuignore"), []byte("*.go: golangci-lint\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".datamitsu"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	if err := runOnFixture(t, []config.OperationType{config.OpLint}, Options{}); err != nil {
		t.Errorf("clean root must pass: %v", err)
	}
}
