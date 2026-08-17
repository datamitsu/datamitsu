// Package workflows_test guards structural invariants of the release workflow
// that no linter checks and no run ever reports. A release job that silently
// never executes looks exactly like a release job with nothing to do.
package workflows_test

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

const releaseWorkflowPath = "../../.github/workflows/release.yml"

type workflowJob struct {
	If    string `yaml:"if"`
	Needs any    `yaml:"needs"`
}

type workflowFile struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

// needsList normalizes the `needs:` key, which YAML allows as either a bare
// string or a sequence.
func (j workflowJob) needsList(t *testing.T, name string) []string {
	t.Helper()
	switch v := j.Needs.(type) {
	case nil:
		return nil
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				t.Fatalf("job %q: needs entry %v is not a string", name, item)
			}
			out = append(out, s)
		}
		return out
	default:
		t.Fatalf("job %q: unexpected needs type %T", name, j.Needs)
		return nil
	}
}

func loadReleaseWorkflow(t *testing.T) workflowFile {
	t.Helper()
	raw, err := os.ReadFile(releaseWorkflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", releaseWorkflowPath, err)
	}
	var wf workflowFile
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse %s: %v", releaseWorkflowPath, err)
	}
	if len(wf.Jobs) == 0 {
		t.Fatalf("%s declares no jobs — the guard would pass vacuously", releaseWorkflowPath)
	}
	return wf
}

// TestReleaseJobsDownstreamOfAConditionalJobOptOutOfSkipPropagation pins the
// single rule that made "Publish to GitHub Releases" unreachable on every
// unstable dispatch: GitHub propagates a skipped job down the WHOLE dependency
// chain, and only a job whose own `if` says "continue anyway" — always() or
// !cancelled() — escapes it. Its `if` never even gets consulted otherwise.
//
// This workflow is structurally guaranteed to skip something on every single
// run: validate-stable and generate-unstable-version are mutually exclusive, so
// exactly one of them is always skipped, and build (which needs both) is
// therefore always downstream of a skip. Any job depending on a conditional job
// and lacking the preamble is dead code that reports itself as "skipped", which
// reads like a deliberate no-op rather than a bug.
func TestReleaseJobsDownstreamOfAConditionalJobOptOutOfSkipPropagation(t *testing.T) {
	wf := loadReleaseWorkflow(t)

	// A job with a condition is a job that can be skipped. Skip-ness is
	// transitive, but so is this property — a job needing always() has an `if`
	// itself — so checking direct parents covers the whole chain.
	conditional := make(map[string]bool, len(wf.Jobs))
	for name, job := range wf.Jobs {
		conditional[name] = strings.TrimSpace(job.If) != ""
	}

	names := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		job := wf.Jobs[name]
		for _, parent := range job.needsList(t, name) {
			if _, ok := wf.Jobs[parent]; !ok {
				t.Errorf("job %q needs %q, which is not a job in this workflow", name, parent)
				continue
			}
			if !conditional[parent] {
				continue
			}
			if strings.Contains(job.If, "always()") || strings.Contains(job.If, "!cancelled()") {
				continue
			}
			t.Errorf("job %q needs the conditional job %q but its `if` contains neither always() nor "+
				"!cancelled(), so GitHub skips it whenever %q is skipped — regardless of what its own "+
				"condition evaluates to. Add the `always() && !cancelled() && needs.<job>.result == 'success'` "+
				"preamble the other jobs use.\n  if: %s", name, parent, parent, job.If)
		}
	}
}

// TestPublishGitHubGateIsReachableForUnstable pins the specific gate the
// prerelease depends on. The prerelease is deliberately opt-in, so the only way
// to tell "correctly not requested" from "structurally unreachable" is to state
// what the condition must contain.
func TestPublishGitHubGateIsReachableForUnstable(t *testing.T) {
	wf := loadReleaseWorkflow(t)

	job, ok := wf.Jobs["publish-github"]
	if !ok {
		t.Fatal("release.yml has no publish-github job")
	}
	for _, want := range []string{
		"always()",
		"needs.build.result == 'success'",
		"needs.determine-release-type.outputs.is_unstable == 'true'",
		"needs.determine-release-type.outputs.want_prerelease == 'true'",
	} {
		if !strings.Contains(job.If, want) {
			t.Errorf("publish-github `if` is missing %q\n  if: %s", want, job.If)
		}
	}
}
