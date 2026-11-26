// Package workflows_test guards structural invariants of the release workflow
// that no linter checks and no run ever reports. A release job that silently
// never executes looks exactly like a release job with nothing to do.
package workflows_test

import (
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/gitenv"
	"github.com/datamitsu/datamitsu/internal/gittest"

	"github.com/goccy/go-yaml"
)

func TestMain(m *testing.M) {
	gittest.Isolate()
	m.Run()
}

const releaseWorkflowPath = "../../.github/workflows/release.yml"

type workflowJob struct {
	If          string            `yaml:"if"`
	Needs       any               `yaml:"needs"`
	Permissions map[string]string `yaml:"permissions"`
	Env         map[string]string `yaml:"env"`
	Steps       []struct {
		Name string            `yaml:"name"`
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
	wf := loadReleaseWorkflow(t)
	var script string
	for _, step := range wf.Jobs["determine-release-type"].Steps {
		if step.ID == "detect" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("release routing step missing")
	}
	sha := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name, event, ref, mode, protected, attempt, wantMode, wantVerify, wantIsUnstable, wantAutoUnstable, wantPrerelease string
	}{
		{"verify from protected source", "workflow_dispatch", "refs/heads/main", "verify", "true", "1", "verify", "true", "true", "false", "false"},
		{"verify from feature source", "workflow_dispatch", "refs/heads/feature", "verify", "false", "1", "verify", "true", "true", "false", "false"},
		{"prerelease from protected source", "workflow_dispatch", "refs/heads/main", "prerelease", "true", "1", "prerelease", "false", "true", "false", "true"},
		{"unstable from protected source", "workflow_dispatch", "refs/heads/main", "unstable", "true", "1", "unstable", "false", "true", "true", "false"},
		{"unstable from unprotected source", "workflow_dispatch", "refs/heads/main", "unstable", "false", "1", "unstable", "false", "true", "false", "false"},
		{"unstable rerun", "workflow_dispatch", "refs/heads/main", "unstable", "true", "2", "unstable", "false", "true", "false", "false"},
		{"unstable from feature source", "workflow_dispatch", "refs/heads/feature", "unstable", "true", "1", "unstable", "false", "true", "false", "false"},
		{"workflow-run is not a native release dispatch", "workflow_run", "refs/heads/main", "unstable", "true", "1", "", "", "", "", ""},
		{"stable tag", "push", "refs/tags/v1.2.3", "", "true", "1", "tag", "false", "false", "false", "false"},
		{"candidate tag", "push", "refs/tags/v1.2.3-rc.1", "", "true", "1", "tag", "false", "false", "false", "false"},
		{"invalid tag", "push", "refs/tags/v01.2.3", "", "true", "1", "", "", "", "", ""},
		{"unsupported mode", "workflow_dispatch", "refs/heads/main", "stable", "true", "1", "", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "output")
			cmd := exec.CommandContext(t.Context(), "bash", "-ec", script)
			cmd.Env = append(os.Environ(), "GITHUB_SHA="+sha, "GITHUB_OUTPUT="+output,
				"GITHUB_EVENT_NAME="+tc.event, "GITHUB_REF="+tc.ref,
				"RELEASE_MODE="+tc.mode, "GITHUB_REPOSITORY=datamitsu/datamitsu", "DEFAULT_BRANCH=main",
				"PROTECTED_REF="+tc.protected, "GITHUB_WORKFLOW_SHA="+sha, "GITHUB_RUN_ATTEMPT="+tc.attempt)
			log, err := cmd.CombinedOutput()
			if tc.wantMode == "" {
				if _, ok := errors.AsType[*exec.ExitError](err); !ok {
					t.Fatalf("expected release rejection, got %v: %s", err, log)
				}
				return
			}
			if err != nil {
				t.Fatalf("release routing failed: %v: %s", err, log)
			}
			got, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			fields := string(got)
			for _, want := range []string{"checkout_ref=" + sha + "\n", "mode=" + tc.wantMode + "\n", "is_unstable=" + tc.wantIsUnstable + "\n"} {
				if !strings.Contains(fields, want) {
					t.Errorf("routing output %q does not contain %q", fields, want)
				}
			}
			verifyEnabled := strings.Contains(fields, "verify=true\n")
			if verifyEnabled != (tc.wantVerify == "true") {
				t.Errorf("verification routing = %v, want %s; output: %q", verifyEnabled, tc.wantVerify, fields)
			}
			for key, want := range map[string]string{"auto_unstable": tc.wantAutoUnstable, "auto_prerelease": tc.wantPrerelease} {
				present := strings.Contains(fields, key+"=true\n")
				if present != (want == "true") {
					t.Errorf("%s handoff = %v, want %s; output: %q", key, present, want, fields)
				}
			}
		})
	}
}

func TestUnstableVersionIdentityUsesRunTimestampAndAttempt(t *testing.T) {
	job := loadReleaseWorkflow(t).Jobs["generate-unstable-version"]
	var script string
	for _, step := range job.Steps {
		if step.ID == "version" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("unstable version generation step missing")
	}
	for _, tc := range []struct{ mode, kind string }{{"verify", "preview"}, {"prerelease", "preview"}, {"unstable", "unstable"}} {
		t.Run(tc.mode, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.CommandContext(t.Context(), "bash", "-ec", script)
			cmd.Dir = dir
			cmd.Env = append(gitenv.Environ(), "MODE="+tc.mode, "GITHUB_RUN_ID=12345", "GITHUB_RUN_ATTEMPT=7", "GITHUB_OUTPUT="+filepath.Join(dir, "output"))
			// The production step reads the checked-out commit. A temporary repository
			// makes its source-derived identity deterministic without pinning the clock.
			for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=workflow-test", "-c", "user.email=workflow@example.invalid", "commit", "--allow-empty", "-qm", "source"}} {
				git := exec.CommandContext(t.Context(), "git", args...)
				git.Dir = dir
				git.Env = gitenv.Environ()
				if output, err := git.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v: %s", args, err, output)
				}
			}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("version generation failed: %v: %s", err, output)
			}
			values, err := os.ReadFile(filepath.Join(dir, "output"))
			if err != nil {
				t.Fatal(err)
			}
			version := regexp.MustCompile(`(?m)^version=(0\.0\.0-` + tc.kind + `\.[0-9]{12}\.12345\.7)$`).FindStringSubmatch(string(values))
			if len(version) != 2 {
				t.Fatalf("generated version lacks timestamp/run/attempt identity: %s", values)
			}
			tag := regexp.MustCompile(`(?m)^tag_name=(` + tc.kind + `-[0-9]{12}-12345-7)$`).FindStringSubmatch(string(values))
			if len(tag) != 2 {
				t.Fatalf("generated tag lacks timestamp/run/attempt identity: %s", values)
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
		case "report-preparation-failure":
			checkSecrets(name, complete.Jobs[name])
			if len(job.Permissions) != 0 {
				t.Errorf("preparation failure reporting must have no token permissions, got %v", job.Permissions)
			}
			var run string
			var runSb423 strings.Builder
			for _, step := range job.Steps {
				runSb423.WriteString(step.Run)
			}
			run += runSb423.String()
			for _, field := range []string{"Actual outcome", "What to install", "Next safe action"} {
				if !strings.Contains(run, field) {
					t.Errorf("preparation failure summary omits %q information", field)
				}
			}
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
	if strings.Contains(script, "release-publication") {
		t.Fatal("publication preflight must not require the former shared release-publication environment")
	}
	for _, destination := range []string{"pypi", "rubygems", "setup-action", "open-vsx", "vscode-marketplace", "homebrew", "scoop", "winget"} {
		if !strings.Contains(script, destination) {
			t.Errorf("publication preflight does not protect destination environment %q", destination)
		}
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
	for _, tc := range []struct{ name, mode, version, pypi, blocked, response, attempt string }{
		{"destination environments present", "unstable", "0.0.0-unstable.1.1", "false", "", "", "1"},
		{"rerun with protected destination environment", "unstable", "0.0.0-unstable.1.1", "false", "", "", "2"},
		{"missing selected PyPI environment", "tag", "1.2.0-rc.1", "true", "pypi", "", "1"},
		{"destination without reviewer protection", "tag", "1.2.0-rc.1", "true", "pypi", `{"name":"pypi","protection_rules":[{"type":"branch_policy"}]}`, "1"},
		{"destination with empty reviewers", "tag", "1.2.0-rc.1", "true", "pypi", `{"name":"pypi","protection_rules":[{"type":"required_reviewers","reviewers":[]}]}`, "1"},
		{"missing stable-only environment", "tag", "1.2.0", "false", "winget", "", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "outputs")
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", mock+script)
			cmd.Env = append(os.Environ(), "GITHUB_REPOSITORY=datamitsu/datamitsu", "MODE="+tc.mode,
				"VERSION="+tc.version, "PUBLISH_PYPI="+tc.pypi, "PUBLISH_RUBYGEMS=false",
				"GITHUB_RUN_ATTEMPT="+tc.attempt, "BLOCKED_ENVIRONMENT="+tc.blocked,
				"ENVIRONMENT_RESPONSE="+tc.response, "GITHUB_OUTPUT="+output)
			log, err := cmd.CombinedOutput()
			var denial string
			if tc.attempt == "2" {
				denial = "Start a new Release Preflight"
			} else if tc.blocked != "" {
				denial = tc.blocked + "; publication blocked"
			}
			if denial != "" {
				if _, ok := errors.AsType[*exec.ExitError](err); !ok || !strings.Contains(string(log), denial) {
					t.Fatalf("expected %q, got %v: %s", denial, err, log)
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
			if err != nil || string(got) != "publication_attempt=1\n" {
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
	if strings.Contains(string(raw), "release-publication") {
		t.Error("preflight and publisher jobs must not use the former shared release-publication environment")
	}
	const ready = "needs.preflight.outputs.publication_attempt == github.run_attempt"
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
		"promote-nonstable":         {"preflight", "publisher-inputs", "verify-parser", "publish-images", "publish-tagged-pypi", "publish-tagged-rubygems"},
		"report-publication":        {"preflight", "finalize-parser", "publish-images", "finalize-github-assets", "verify-parser", "publish-github", "publisher-inputs", "publish-tagged-pypi", "publish-tagged-rubygems", "publish-setup-action", "publish-editor-extension", "publish-install-manifests", "promote-stable", "promote-nonstable"},
	}
	var preflightScript, branchScript string
	for _, step := range wf.Jobs["preflight"].Steps {
		if step.ID == "verify" {
			preflightScript = step.Run
		}
		if step.Name == "Require the default branch" {
			branchScript = step.Run
		}
	}
	for _, contract := range []string{"workflow_run", "handoff_mode", "COMPLETED_ATTEMPT", "head_branch", "DEFAULT_BRANCH", "automatic_mode"} {
		if !strings.Contains(preflightScript, contract) {
			t.Errorf("preflight handoff does not enforce %q", contract)
		}
	}
	for _, contract := range []string{"refs/heads/$DEFAULT_BRANCH", "GITHUB_REPOSITORY"} {
		if !strings.Contains(branchScript+preflightScript, contract) {
			t.Errorf("preflight does not require canonical protected default-branch source: missing %q", contract)
		}
	}
	for _, contract := range []string{"datamitsu/datamitsu", "PROTECTED_REF", `"$PROTECTED_REF" == true`, `"$GITHUB_WORKFLOW_SHA" == "$GITHUB_SHA"`, `"$GITHUB_REPOSITORY" == datamitsu/datamitsu`, `"$GITHUB_REF" == "refs/heads/$DEFAULT_BRANCH"`} {
		if !strings.Contains(branchScript+preflightScript, contract) {
			t.Errorf("preflight source check is missing %q", contract)
		}
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
		if name != "preflight" && name != "verify-parser" && name != "report-publication" {
			if !strings.Contains(condition, ready) {
				t.Errorf("%s can bypass preflight publication authorization: %q", name, condition)
			}
			destinationEnvironment := name == "publish-tagged-pypi" || name == "publish-tagged-rubygems" || name == "publish-setup-action" || name == "publish-editor-extension" || name == "publish-install-manifests"
			if (complete.Jobs[name]["environment"] != nil) != destinationEnvironment {
				t.Errorf("%s: unexpected destination environment policy", name)
			}
		}
		if name == "report-publication" {
			if !strings.Contains(condition, "always()") || !strings.Contains(condition, "!cancelled()") {
				t.Errorf("publication summary must run after success or failure, unless cancelled: %q", condition)
			}
			var run string
			for _, step := range job.Steps {
				if strings.Contains(step.Run, "GITHUB_STEP_SUMMARY") {
					run = step.Run
				}
			}
			for _, field := range []string{"Actual outcome", "What to install", "Next safe action"} {
				if !strings.Contains(run, field) {
					t.Errorf("publication summary omits its %q information", field)
				}
			}
			if run == "" || !strings.Contains(run, "incomplete") {
				t.Error("publication summary must report incomplete destinations")
			}
		}
		if name == "promote-stable" || name == "promote-nonstable" {
			for _, dependency := range job.needsList(t, name) {
				if dependency == "preflight" {
					continue
				}
				if !strings.Contains(condition, "needs."+dependency+".result == 'success'") &&
					!strings.Contains(condition, "needs."+dependency+".result == 'skipped'") {
					t.Errorf("%s alias promotion does not gate on %s outcome", name, dependency)
				}
			}
		}
		if name == "promote-stable" && !strings.Contains(condition, stable) {
			t.Errorf("stable alias promotion must exclude RC and nonstable builds: %q", condition)
		}
		if name == "promote-nonstable" {
			strategy, _ := complete.Jobs[name]["strategy"].(map[string]any)
			matrix, _ := strategy["matrix"].(map[string]any)
			channels, _ := matrix["channel"].(string)
			if !strings.Contains(channels, `"parser"`) || !strings.Contains(channels, `"ghcr"`) || !strings.Contains(channels, `"dockerhub"`) {
				t.Errorf("nonstable promotion matrix omits a required registry channel: %q", channels)
			}
			if !strings.Contains(channels, "needs.preflight.outputs.mode == 'tag'") {
				t.Errorf("Docker Hub must only be an eligible nonstable alias for tagged RC publication: %q", channels)
			}
			if !strings.Contains(condition, "needs.preflight.outputs.mode == 'unstable'") ||
				!strings.Contains(condition, "startsWith(needs.preflight.outputs.release_tag, 'preview-')") {
				t.Errorf("nonstable alias promotion lacks preview/unstable routing: %q", condition)
			}
		}
		sort.Strings(guard)
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
