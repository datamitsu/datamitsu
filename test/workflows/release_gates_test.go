// Package workflows_test guards structural invariants of the release workflow
// that no linter checks and no run ever reports. A release job that silently
// never executes looks exactly like a release job with nothing to do.
package workflows_test

import (
	"errors"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

const releaseWorkflowPath = "../../.github/workflows/release.yml"

type workflowJob struct {
	If          string            `yaml:"if"`
	Needs       any               `yaml:"needs"`
	Permissions map[string]string `yaml:"permissions"`
	Env         map[string]string `yaml:"env"`
	Steps       []struct {
		ID   string            `yaml:"id"`
		Run  string            `yaml:"run"`
		Uses string            `yaml:"uses"`
		With map[string]string `yaml:"with"`
		Env  map[string]string `yaml:"env"`
	} `yaml:"steps"`
}

type workflowFile struct {
	Jobs        map[string]workflowJob `yaml:"jobs"`
	Permissions map[string]string      `yaml:"permissions"`
	Env         map[string]string      `yaml:"env"`
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

func TestReleaseRouting(t *testing.T) {
	var script string
	for _, step := range loadReleaseWorkflow(t).Jobs["determine-release-type"].Steps {
		if step.ID == "detect" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("release routing step missing")
	}
	sha := strings.Repeat("a", 40)
	for _, tc := range []struct{ name, event, ref, mode, want string }{
		{"verify", "workflow_dispatch", "refs/heads/feature", "verify", "verify=true\n"},
		{"explicit prerelease", "workflow_dispatch", "refs/heads/feature", "prerelease", "verify=false\n"},
		{"stable tag", "push", "refs/tags/v1.2.3", "", "tag_ref=refs/tags/v1.2.3\n"},
		{"candidate tag", "push", "refs/tags/v1.2.3-rc.1", "", "tag_ref=refs/tags/v1.2.3-rc.1\n"},
		{"invalid tag", "push", "refs/tags/v01.2.3", "", ""},
		{"manual stable denied", "workflow_dispatch", "refs/heads/main", "stable", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "output")
			cmd := exec.CommandContext(t.Context(), "bash", "-ec", script)
			cmd.Env = append(os.Environ(), "GITHUB_SHA="+sha, "GITHUB_OUTPUT="+output,
				"GITHUB_EVENT_NAME="+tc.event, "GITHUB_REF="+tc.ref,
				"RELEASE_MODE="+tc.mode)
			log, err := cmd.CombinedOutput()
			if tc.want == "" {
				if _, ok := errors.AsType[*exec.ExitError](err); !ok {
					t.Fatalf("expected release rejection, got %v: %s", err, log)
				}
				return
			}
			if err != nil {
				t.Fatalf("release routing failed: %v: %s", err, log)
			}
			got, err := os.ReadFile(output)
			if err != nil || !strings.Contains(string(got), "checkout_ref="+sha+"\n") || !strings.Contains(string(got), tc.want) {
				t.Fatalf("routing output %q, error %v; want event SHA and %q", got, err, tc.want)
			}
		})
	}
}

func TestUnstableDispatchResolvesProductFromProtectedWorkflow(t *testing.T) {
	var script string
	for _, step := range loadReleaseWorkflow(t).Jobs["determine-release-type"].Steps {
		if step.ID == "detect" {
			script = step.Run
		}
	}
	const mock = `gh() {
	[[ "$*" == "api $EXPECTED_API_PATH --jq .sha" ]] || return 2
	printf '%s\n' "$RESOLVED_SHA"
}
`
	dispatchSHA, productSHA := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, tc := range []struct {
		name, ref, product, protected, workflow, attempt, output string
	}{
		{"protected default branch", "refs/heads/main", "refs/heads/feature/demo", "true", dispatchSHA, "1", productSHA},
		{"feature workflow", "refs/heads/feature", "refs/heads/feature/demo", "true", dispatchSHA, "1", ""},
		{"unprotected default branch", "refs/heads/main", "refs/heads/feature/demo", "false", dispatchSHA, "1", ""},
		{"workflow revision mismatch", "refs/heads/main", "refs/heads/feature/demo", "true", productSHA, "1", ""},
		{"unstable rebuild", "refs/heads/main", "refs/heads/feature/demo", "true", dispatchSHA, "2", ""},
		{"empty product ref uses dispatch commit", "refs/heads/main", "", "true", dispatchSHA, "1", dispatchSHA},
		{"invalid explicit product ref", "refs/heads/main", "invalid ref", "true", dispatchSHA, "1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			output := filepath.Join(dir, "output")
			cmd := exec.CommandContext(t.Context(), "bash", "-ec", mock+script)
			cmd.Env = append(os.Environ(), "GITHUB_EVENT_NAME=workflow_dispatch", "RELEASE_MODE=unstable",
				"GITHUB_REPOSITORY=datamitsu/datamitsu", "DEFAULT_BRANCH=main",
				"GITHUB_REF="+tc.ref, "PROTECTED_REF="+tc.protected, "GITHUB_SHA="+dispatchSHA,
				"GITHUB_WORKFLOW_SHA="+tc.workflow, "GITHUB_RUN_ATTEMPT="+tc.attempt,
				"PRODUCT_REF="+tc.product, "RESOLVED_SHA="+productSHA, "GITHUB_OUTPUT="+output,
				"EXPECTED_API_PATH=repos/datamitsu/datamitsu/commits/"+url.PathEscape(tc.product),
				"GITHUB_STEP_SUMMARY="+filepath.Join(dir, "summary"))
			log, err := cmd.CombinedOutput()
			if tc.output == "" {
				if _, ok := errors.AsType[*exec.ExitError](err); !ok {
					t.Fatalf("expected unstable rejection, got %v: %s", err, log)
				}
				return
			}
			if err != nil {
				t.Fatalf("unstable routing failed: %v: %s", err, log)
			}
			got, err := os.ReadFile(output)
			if err != nil || !strings.Contains(string(got), "checkout_ref="+tc.output+"\n") || !strings.Contains(string(got), "mode=unstable\n") {
				t.Fatalf("unstable routing output %q, error %v; want resolved SHA %s", got, err, tc.output)
			}
		})
	}
}

var (
	actionExpression = regexp.MustCompile(`(?s)\$\{\{((?:'(?:[^']|'')*'|[^'}]|}[^}])*)\}\}`)
	actionString     = regexp.MustCompile(`'(?:[^']|'')*'`)
	secretContext    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9_.\s-])\s*secrets(?:$|[^a-z0-9_-])`)
)

func referencesReleaseSecrets(t *testing.T, fields map[string]any, job bool) bool {
	t.Helper()
	lookup := func(mapping map[string]any, name string) (any, bool) {
		for key, value := range mapping {
			if strings.EqualFold(key, name) {
				return value, true
			}
		}
		return nil, false
	}
	if _, forwards := lookup(fields, "secrets"); job && forwards {
		return true
	}
	hasCredentials := func(value any) bool {
		container, _ := value.(map[string]any)
		_, exists := lookup(container, "credentials")
		return exists
	}
	if container, _ := lookup(fields, "container"); job && hasCredentials(container) {
		return true
	}
	servicesValue, _ := lookup(fields, "services")
	services, _ := servicesValue.(map[string]any)
	for _, service := range services {
		if job && hasCredentials(service) {
			return true
		}
	}
	return hasSecretExpression(fields)
}

func hasSecretExpression(value any) bool {
	switch value := value.(type) {
	case string:
		for _, match := range actionExpression.FindAllStringSubmatch(value, -1) {
			if secretContext.MatchString(actionString.ReplaceAllString(match[1], "")) {
				return true
			}
		}
	case map[string]any:
		for key, child := range value {
			if hasSecretExpression(key) || hasSecretExpression(child) {
				return true
			}
		}
	case []any:
		if slices.ContainsFunc(value, hasSecretExpression) {
			return true
		}
	}
	return false
}

func TestDetectReleaseRegistryCredentials(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"service credentials", "services:\n  registry:\n    credentials:\n      password: ${{ secrets['REGISTRY_TOKEN'] }}\n", true},
		{"reusable forwarding", "secrets: inherit\n", true},
		{"literal container credentials", "container:\n  credentials:\n    password: fixture\n", true},
		{"mixed-case container credentials", "Container:\n  Credentials:\n    password: fixture\n", true},
		{"quoted brace before secret", `run: "${{ format('}', secrets.REGISTRY_TOKEN) }}"`, true},
		{"YAML single quoted expression", `run: '${{ format(''}}: '', secrets.REGISTRY_TOKEN) }}'`, true},
		{"quoted expression text", `run: "${{ 'No release secrets required' }}"`, false},
		{"plain diagnostic", "steps:\n  - run: echo 'No release secrets required'\n", false},
		{"variable property", `run: "${{ vars.RELEASE-SECRETS }} ${{ vars.secrets }}"`, false},
		{"expression in env key", "steps:\n  - env:\n      '${{ secrets.REGISTRY_TOKEN }}': fixture\n", true},
		{"literal env key", "steps:\n  - env:\n      secrets: fixture\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var job map[string]any
			if err := yaml.Unmarshal([]byte(tc.body), &job); err != nil {
				t.Fatal(err)
			}
			if got := referencesReleaseSecrets(t, job, true); got != tc.want {
				t.Errorf("credential detection = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDetectAliasedReleaseRegistryCredentials(t *testing.T) {
	const fixture = `jobs:
  held:
    if: ${{ false }}
    container: &registry
      credentials:
        password: fixture
  build:
    services:
      registry: *registry
`
	var workflow struct {
		Jobs map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(fixture), &workflow); err != nil {
		t.Fatal(err)
	}
	if !referencesReleaseSecrets(t, workflow.Jobs["build"], true) {
		t.Fatal("runnable job accepted registry credentials from a held job's alias")
	}
}

func TestReleasePreparationCannotPublish(t *testing.T) {
	wf := loadReleaseWorkflow(t)
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Fatalf("release workflow must declare an explicit read-only default, got %v", wf.Permissions)
	}
	for resource, level := range wf.Permissions {
		if level != "read" && level != "none" {
			t.Errorf("release workflow default must not have %s: %s", resource, level)
		}
	}
	raw, err := os.ReadFile(releaseWorkflowPath)
	if err != nil {
		t.Fatal(err)
	}
	var complete struct {
		Jobs map[string]map[string]any `yaml:"jobs"`
		Env  map[string]any            `yaml:"env"`
	}
	if err := yaml.Unmarshal(raw, &complete); err != nil {
		t.Fatal(err)
	}
	checkSecrets := func(scope string, fields map[string]any) {
		t.Helper()
		if referencesReleaseSecrets(t, fields, scope != "workflow environment") {
			t.Errorf("%s must not reference or forward repository secrets", scope)
		}
	}
	checkSecrets("workflow environment", complete.Env)
	for _, name := range []string{"build", "verify"} {
		job := wf.Jobs[name]
		if len(job.Permissions) != 1 || job.Permissions["contents"] != "read" {
			t.Errorf("%s must only have contents: read, got %v", name, job.Permissions)
		}
		found := false
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "goreleaser/goreleaser-action@") {
				found = true
				if !strings.Contains(step.With["args"], "}} --skip=sign,publish --clean") {
					t.Errorf("%s must disable GoReleaser signing and publication for both tag and manual builds", name)
				}
			}
		}
		if !found {
			t.Errorf("%s is missing GoReleaser preparation", name)
		}
	}
	for name, job := range wf.Jobs {
		switch name {
		case "build", "verify", "determine-release-type", "generate-unstable-version", "validate-stable":
			checkSecrets(name, complete.Jobs[name])
			permissions := job.Permissions
			if permissions == nil {
				permissions = wf.Permissions
			}
			if len(permissions) != 1 || permissions["contents"] != "read" {
				t.Errorf("%s must only have contents: read, got %v", name, permissions)
			}
			continue
		}
		if name != "attest-tagged-npm" {
			t.Errorf("unexpected job in the preparation workflow: %s", name)
			continue
		}
		checkSecrets(name, complete.Jobs[name])
		want := map[string]string{"actions": "read", "contents": "read", "id-token": "write", "attestations": "write"}
		if !maps.Equal(job.Permissions, want) || complete.Jobs[name]["environment"] != "release-build-attestation" {
			t.Errorf("npm attestation must have isolated signing permissions and environment")
		}
		for _, guard := range []string{"github.event_name == 'push'", "github.ref_type == 'tag'", "github.ref_protected", "vars.RELEASE_BUILD_ATTESTATION_ENABLED == 'true'", "needs.build.result == 'success'", "needs.validate-stable.result == 'success'"} {
			if !strings.Contains(job.If, "&& "+guard) {
				t.Errorf("tagged npm attestation is missing %q", guard)
			}
		}
	}
}

func TestReleasePreflightRequiresEnvironmentReviewers(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release-preflight.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf workflowFile
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range wf.Jobs["preflight"].Steps {
		if step.ID == "approvals" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("missing publication approval step")
	}
	const mock = `gh() {
	[[ "$#" == 2 && "$1" == api && "$2" == repos/datamitsu/datamitsu/environments/* ]] || return 2
	local name=${2##*/}
	if [[ "$name" == "$BLOCKED_ENVIRONMENT" ]]; then
		if [[ -z "$ENVIRONMENT_RESPONSE" ]]; then return 1; fi
		printf '%s\n' "$ENVIRONMENT_RESPONSE"
	else
		printf '{"name":"%s","protection_rules":[{"type":"required_reviewers","reviewers":[{"type":"User","reviewer":{"id":1}}]}]}\n' "$name"
	fi
}
`
	for _, tc := range []struct{ name, mode, version, pypi, blocked, response string }{
		{"unstable approval present", "unstable", "0.0.0-unstable.1.1", "false", "", ""},
		{"missing environment", "unstable", "0.0.0-unstable.1.1", "false", "release-publication", ""},
		{"branch rule alone", "unstable", "0.0.0-unstable.1.1", "false", "release-publication", `{"name":"release-publication","protection_rules":[{"type":"branch_policy"}]}`},
		{"empty reviewers", "unstable", "0.0.0-unstable.1.1", "false", "release-publication", `{"name":"release-publication","protection_rules":[{"type":"required_reviewers","reviewers":[]}]}`},
		{"missing selected PyPI environment", "tag", "1.2.0-rc.1", "true", "pypi", ""},
		{"missing stable-only environment", "tag", "1.2.0", "false", "winget", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "outputs")
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", mock+script)
			cmd.Env = append(os.Environ(), "GITHUB_REPOSITORY=datamitsu/datamitsu", "MODE="+tc.mode,
				"VERSION="+tc.version, "PUBLISH_PYPI="+tc.pypi, "PUBLISH_RUBYGEMS=false",
				"BLOCKED_ENVIRONMENT="+tc.blocked, "ENVIRONMENT_RESPONSE="+tc.response, "GITHUB_OUTPUT="+output)
			log, err := cmd.CombinedOutput()
			if tc.blocked != "" {
				if _, ok := errors.AsType[*exec.ExitError](err); !ok || !strings.Contains(string(log), tc.blocked+"; publication blocked") {
					t.Fatalf("expected missing approval for %s, got %v: %s", tc.blocked, err, log)
				}
				if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("blocked release must not emit readiness: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("protected release did not become ready: %v: %s", err, log)
			}
			got, err := os.ReadFile(output)
			if err != nil || string(got) != "publication_ready=true\n" {
				t.Fatalf("publication readiness = %q, %v", got, err)
			}
		})
	}
}

func TestReleasePreflightPublicationBoundary(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release-preflight.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf workflowFile
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	var complete struct {
		Jobs map[string]map[string]any `yaml:"jobs"`
		Env  map[string]any            `yaml:"env"`
	}
	if err := yaml.Unmarshal(raw, &complete); err != nil {
		t.Fatal(err)
	}
	if referencesReleaseSecrets(t, complete.Env, false) {
		t.Error("preflight workflow environment must not reference secrets")
	}
	const ready = "needs.preflight.outputs.publication_ready == 'true'"
	const stable = "needs.preflight.outputs.mode == 'tag' && !contains(needs.preflight.outputs.version, '-rc.')"
	guards := map[string][]string{
		"preflight":                 nil,
		"verify-parser":             {"finalize-parser", "preflight"},
		"finalize-parser":           {"preflight"},
		"publish-images":            {"preflight"},
		"finalize-github-assets":    {"preflight"},
		"publish-github":            {"finalize-github-assets", "preflight", "verify-parser"},
		"publisher-inputs":          {"preflight", "publish-github", "publish-images", "verify-parser"},
		"publish-tagged-pypi":       {"preflight"},
		"publish-tagged-rubygems":   {"preflight"},
		"publish-setup-action":      {"preflight", "publish-github"},
		"publish-editor-extension":  {"preflight", "publish-github"},
		"publish-install-manifests": {"preflight", "publish-github"},
		"promote-stable":            {"finalize-github-assets", "preflight", "publish-editor-extension", "publish-github", "publish-images", "publish-install-manifests", "publish-setup-action", "publish-tagged-pypi", "publish-tagged-rubygems", "publisher-inputs", "verify-parser"},
	}
	for name := range wf.Jobs {
		if _, known := guards[name]; !known {
			t.Errorf("unexpected preflight job %q", name)
		}
	}
	for name, guard := range guards {
		job, exists := wf.Jobs[name]
		if !exists {
			t.Errorf("missing preflight job %q", name)
			continue
		}
		condition := strings.TrimSpace(job.If)
		condition = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(condition, "${{"), "}}"))
		if name != "preflight" && name != "verify-parser" {
			gate := ready
			if name == "publisher-inputs" {
				gate = "!cancelled() && " + ready
			}
			if condition != gate && !strings.HasPrefix(condition, gate+" && ") {
				t.Errorf("%s can bypass preflight publication authorization: %q", name, condition)
			}
			if complete.Jobs[name]["environment"] == nil {
				t.Errorf("%s must require a publication environment", name)
			}
		}
		switch name {
		case "publish-setup-action", "publish-editor-extension", "publish-install-manifests", "promote-stable":
			if !strings.HasPrefix(condition, ready+" && "+stable) {
				t.Errorf("%s must not write for RC or unstable builds", name)
			}
		}
		needs := job.needsList(t, name)
		sort.Strings(needs)
		if !slices.Equal(needs, guard) {
			t.Errorf("%s dependencies = %v, want %v", name, needs, guard)
		}
		if name == "preflight" || name == "verify-parser" {
			if referencesReleaseSecrets(t, complete.Jobs[name], true) {
				t.Errorf("%s must not receive registry credentials or repository secrets", name)
			}
			permissions := job.Permissions
			if permissions == nil {
				permissions = wf.Permissions
			}
			want := map[string]string{"actions": "read", "attestations": "read", "contents": "read"}
			if !maps.Equal(permissions, want) {
				t.Errorf("%s permissions = %v, want %v", name, permissions, want)
			}
		}
	}
}
