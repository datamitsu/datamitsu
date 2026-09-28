package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// This file freezes --report codequality: GitLab's Code Quality array, what
// it leaves out, and the completeness companion beside it.

// outsider reports one finding in a file outside the repository, which GitLab
// cannot place.
var outsider = clitest.ShellTool("outsider",
	settle+clitest.RecordRun+`; echo '[{"file":"/outside/Dockerfile","line":2,"column":1,"level":"error","code":"DL3000","message":"Use absolute WORKDIR"}]'`,
	clitest.ToolOpSpec{Parser: "hadolint"})

func TestReportCodeQuality(t *testing.T) {
	t.Run("lint", func(t *testing.T) {
		e := reportProject(t, outsider)
		res := e.run("", nil, "lint", "--report", "codequality=gl-code-quality.json")
		e.wantExit(res, 1)
		raw := e.read("gl-code-quality.json")
		var issues []map[string]any
		if err := json.Unmarshal([]byte(raw), &issues); err != nil {
			t.Fatalf("not a JSON array: %v\n%s", err, raw)
		}
		if len(issues) != 1 || issues[0]["check_name"] != "hadolint/DL3006" || issues[0]["severity"] != "minor" {
			t.Errorf("issues = %v, want hadolint's warning alone: the other finding is outside the repository", issues)
		}
		companion, decoded := e.companion("gl-code-quality.json")
		if decoded["complete"] != false || !strings.Contains(companion, `"reason": "outside-repository"`) {
			t.Errorf("companion = %s", companion)
		}
		for _, want := range []string{
			"WARN report: lint tool alpha is incomplete (no-extraction): flagged in the completeness companion of codequality",
			"WARN report: codequality left out 1 finding it cannot place (1 outside the repository); its completeness companion counts them",
		} {
			if !strings.Contains(res.Stderr, want) {
				t.Errorf("stderr lacks %q:\n%s", want, res.Stderr)
			}
		}
		e.goldenReport("codequality_lint", e.normalize(raw))
		e.goldenReport("codequality_lint_companion", companion)
	})

	t.Run("narrowed", func(t *testing.T) {
		e := reportProject(t)
		e.wantExit(e.run("", nil, "lint", "--tools", "hadolint", "--report", "codequality=cq.json"), 2)
		if _, err := os.Stat(filepath.Join(e.p.Dir, "cq.json")); err == nil {
			t.Error("a refused run wrote the report")
		}
		e.wantExit(e.run("", nil, "lint", "--tools", "hadolint", "--report", "codequality=cq.json", "--allow-partial"), 0)
		companion, decoded := e.companion("cq.json")
		if decoded["complete"] != false || !strings.Contains(companion, `"tools-filter"`) {
			t.Errorf("companion = %s, want the reasons the run is narrowed", companion)
		}
	})
}
