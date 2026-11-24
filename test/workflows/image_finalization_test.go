package workflows_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// cspell:ignore Ftags
const imageFinalizationFixture = `
gh() {
  case "$1 $2" in
    'api --method')
      [[ "$*" == *"refs/heads/release-finalization/images/$REGISTRY/$APPROVED_TAG"* ]]
      [[ "$REGISTRY" == dockerhub ]] || return 1
      printf '%s\n' 'dockerhub claim' >> "$RUNNER_TEMP/writes"
      ;;
    'api repos/datamitsu/datamitsu/commits/refs%2Ftags%2Fv1.2.3')
      printf '%s\n' "$APPROVED_SHA"
      ;;
    'attestation verify')
      [[ "$REGISTRY" == ghcr && "$*" == *"--signer-digest $GHCR_SIGNER"* ]]
      [[ "$(<"$3")" == *"id=42"*$'\n'"index=sha256:$FIXTURE_INDEX"* ]]
      ;;
    *) return 1 ;;
  esac
}
python3() {
  [[ "$1" == .github/scripts/publish-prepared-images.py && "$2" == "$RUNNER_TEMP/release-payloads/release/dist" ]]
  [[ "$3" == "$APPROVED_TAG" && "$4" == --registry && "$5" == "$REGISTRY" ]]
  if [[ "$REGISTRY" == ghcr ]]; then
    [[ "${6:-}" == --require-present ]]
    local repo=ghcr.io/datamitsu/datamitsu
  else
    [[ $# == 5 ]]
    local repo=docker.io/datamitsu/datamitsu
    printf '%s\n' 'dockerhub publish' >> "$RUNNER_TEMP/writes"
  fi
  printf '%s:%s sha256:%064d\n' "$repo" 1.2.3 1 "$repo" 1.2.3-alpine 2
}
cosign() {
  local expected=$GITHUB_WORKFLOW_SHA
  if [[ "$REGISTRY" == ghcr ]]; then expected=$GHCR_SIGNER; fi
  case "$1" in
    sign) printf '%s\n' "$REGISTRY sign" >> "$RUNNER_TEMP/writes" ;;
    verify) [[ "$*" == *"--certificate-github-workflow-sha $expected"* ]] ;;
    *) return 1 ;;
  esac
}
jq() {
  [[ "$1" == -n ]]; shift
  while [[ "$1" == --arg ]]; do
    printf '%s=%s\n' "$2" "$3"
    shift 3
  done
}
`

func TestTaggedImageRecoveryMixesReuseAndFirst(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release-preflight.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range workflow.Jobs["publish-images"].Steps {
		if step.ID == "finalize-images" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("image finalization step is missing")
	}

	originalIndex := strings.Repeat("b", 64)
	for _, tc := range []struct {
		name     string
		registry string
		index    string
		wantFail bool
		writes   string
		output   string
	}{
		{name: "reuse completed GHCR", registry: "ghcr", index: originalIndex, wantFail: false, writes: "", output: ""},
		{
			name: "first Docker Hub", registry: "dockerhub", index: originalIndex, wantFail: false,
			writes: "dockerhub claim\ndockerhub publish\ndockerhub sign\ndockerhub sign\n", output: "first_finalization=true\n",
		},
		{name: "refuse evidence for a different index", registry: "ghcr", index: strings.Repeat("e", 64), wantFail: true, writes: "", output: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writes := filepath.Join(dir, "writes")
			output := filepath.Join(dir, "output")
			for _, file := range []string{writes, output} {
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.CommandContext(t.Context(), "bash", "-euo", "pipefail", "-c", imageFinalizationFixture+script)
			cmd.Dir = dir
			cmd.Env = []string{
				"RUNNER_TEMP=" + dir, "GITHUB_OUTPUT=" + output,
				"GITHUB_REPOSITORY=datamitsu/datamitsu", "GITHUB_SERVER_URL=https://github.com",
				"GITHUB_WORKFLOW_REF=datamitsu/datamitsu/.github/workflows/release-preflight.yml@refs/heads/main",
				"GITHUB_EVENT_NAME=workflow_dispatch", "GITHUB_RUN_ATTEMPT=1", "GITHUB_WORKFLOW_SHA=" + strings.Repeat("c", 40),
				"REGISTRY=" + tc.registry, "RELEASE_MODE=tag", "APPROVED_TAG=v1.2.3", "APPROVED_SHA=" + strings.Repeat("a", 40),
				"INDEX_ID=42", "INDEX_DIGEST=sha256:" + tc.index, "FIXTURE_INDEX=" + originalIndex,
				"FIRST_GHCR=false", "GHCR_SIGNER=" + strings.Repeat("d", 40), "FIRST_DOCKERHUB=true", "DOCKERHUB_SIGNER=",
			}
			log, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantFail {
				t.Fatalf("finalization error = %v, want failure %v: %s", err, tc.wantFail, log)
			}
			for file, want := range map[string]string{writes: tc.writes, output: tc.output} {
				got, err := os.ReadFile(file)
				if err != nil || string(got) != want {
					t.Fatalf("%s = %q, %v; want %q", filepath.Base(file), got, err, want)
				}
			}
		})
	}
}
