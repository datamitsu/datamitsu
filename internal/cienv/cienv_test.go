// cspell:ignore SOURCEVERSION SOURCEBRANCH SOURCESDIRECTORY PULLREQUESTID PULLREQUESTNUMBER TARGETBRANCH

package cienv

import (
	"slices"
	"testing"
)

func lookup(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		info Info
		rt   Runtime
	}{
		{name: "none", env: map[string]string{"HOME": "/home/u"}},
		{name: "ci false", env: map[string]string{"CI": "false"}},
		{name: "ci zero", env: map[string]string{"CI": "0"}},
		{name: "generic", env: map[string]string{"CI": "1"}, info: Info{Vendor: VendorGeneric, IsCI: true}},
		{
			name: "github pull request",
			env: map[string]string{
				"CI": "true", "GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "pull_request",
				"GITHUB_SHA": "abc", "GITHUB_REF": "refs/pull/42/merge", "GITHUB_BASE_REF": "main",
				"GITHUB_WORKSPACE": "/w", "GITHUB_STEP_SUMMARY": "/s.md", "GITHUB_EVENT_PATH": "/e.json",
			},
			info: Info{Vendor: VendorGitHub, IsCI: true, IsPR: true, SHA: "abc", Ref: "refs/pull/42/merge", BaseRef: "main", PRNumber: "42"},
			rt:   Runtime{EventName: "pull_request", Workspace: "/w", StepSummaryPath: "/s.md", EventPath: "/e.json"},
		},
		{
			name: "github push",
			env:  map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "push", "GITHUB_SHA": "abc", "GITHUB_REF": "refs/heads/main"},
			info: Info{Vendor: VendorGitHub, IsCI: true, SHA: "abc", Ref: "refs/heads/main"},
			rt:   Runtime{EventName: "push"},
		},
		{
			name: "github pull_request_target builds the base",
			env:  map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "pull_request_target", "GITHUB_REF": "refs/heads/main", "GITHUB_BASE_REF": "main"},
			info: Info{Vendor: VendorGitHub, IsCI: true, IsPR: true, Ref: "refs/heads/main", BaseRef: "main"},
			rt:   Runtime{EventName: "pull_request_target"},
		},
		{
			name: "gitea wins over the github variables it also sets",
			env: map[string]string{
				"GITHUB_ACTIONS": "true", "GITEA_ACTIONS": "true", "GITHUB_EVENT_NAME": "pull_request",
				"GITHUB_REF": "refs/pull/7/head", "GITHUB_STEP_SUMMARY": "/s.md",
			},
			info: Info{Vendor: VendorGitea, IsCI: true, IsPR: true, Ref: "refs/pull/7/head", PRNumber: "7"},
			rt:   Runtime{EventName: "pull_request"},
		},
		{
			name: "forgejo",
			env:  map[string]string{"GITHUB_ACTIONS": "true", "FORGEJO_ACTIONS": "true"},
			info: Info{Vendor: VendorGitea, IsCI: true},
		},
		{
			name: "gitlab merge request",
			env: map[string]string{
				"GITLAB_CI": "true", "CI_COMMIT_SHA": "abc", "CI_COMMIT_REF_NAME": "feature",
				"CI_MERGE_REQUEST_IID": "9", "CI_MERGE_REQUEST_TARGET_BRANCH_NAME": "main", "CI_PROJECT_DIR": "/builds/p",
			},
			info: Info{Vendor: VendorGitLab, IsCI: true, IsPR: true, SHA: "abc", Ref: "feature", BaseRef: "main", PRNumber: "9"},
			rt:   Runtime{Workspace: "/builds/p"},
		},
		{
			name: "azure pull request",
			env: map[string]string{
				"TF_BUILD": "True", "BUILD_SOURCEVERSION": "abc", "BUILD_SOURCEBRANCH": "refs/pull/5/merge",
				"BUILD_REASON": "PullRequest", "SYSTEM_PULLREQUEST_PULLREQUESTID": "123",
				"SYSTEM_PULLREQUEST_TARGETBRANCH": "refs/heads/main", "BUILD_SOURCESDIRECTORY": "/a/s",
			},
			info: Info{Vendor: VendorAzure, IsCI: true, IsPR: true, SHA: "abc", Ref: "refs/pull/5/merge", BaseRef: "refs/heads/main", PRNumber: "123"},
			rt:   Runtime{Workspace: "/a/s"},
		},
		{
			name: "azure prefers the pull request number",
			env:  map[string]string{"TF_BUILD": "True", "SYSTEM_PULLREQUEST_PULLREQUESTID": "123", "SYSTEM_PULLREQUEST_PULLREQUESTNUMBER": "5"},
			info: Info{Vendor: VendorAzure, IsCI: true, PRNumber: "5"},
		},
		{
			name: "teamcity",
			env:  map[string]string{"TEAMCITY_VERSION": "2025.03", "BUILD_VCS_NUMBER": "abc"},
			info: Info{Vendor: VendorTeamCity, IsCI: true, SHA: "abc"},
		},
		{
			name: "buildkite not a pull request",
			env:  map[string]string{"BUILDKITE": "true", "BUILDKITE_COMMIT": "abc", "BUILDKITE_BRANCH": "main", "BUILDKITE_PULL_REQUEST": "false"},
			info: Info{Vendor: VendorBuildkite, IsCI: true, SHA: "abc", Ref: "main"},
		},
		{
			name: "buildkite pull request",
			env: map[string]string{
				"BUILDKITE": "true", "BUILDKITE_PULL_REQUEST": "12", "BUILDKITE_PULL_REQUEST_BASE_BRANCH": "main",
				"BUILDKITE_BUILD_CHECKOUT_PATH": "/b",
			},
			info: Info{Vendor: VendorBuildkite, IsCI: true, IsPR: true, BaseRef: "main", PRNumber: "12"},
			rt:   Runtime{Workspace: "/b"},
		},
		{
			name: "bitbucket pull request",
			env: map[string]string{
				"BITBUCKET_BUILD_NUMBER": "3", "BITBUCKET_COMMIT": "abc", "BITBUCKET_BRANCH": "feature",
				"BITBUCKET_PR_ID": "8", "BITBUCKET_PR_DESTINATION_BRANCH": "main", "BITBUCKET_CLONE_DIR": "/c",
			},
			info: Info{Vendor: VendorBitbucket, IsCI: true, IsPR: true, SHA: "abc", Ref: "feature", BaseRef: "main", PRNumber: "8"},
			rt:   Runtime{Workspace: "/c"},
		},
		{
			name: "bitbucket tag",
			env:  map[string]string{"BITBUCKET_BUILD_NUMBER": "3", "BITBUCKET_TAG": "v1"},
			info: Info{Vendor: VendorBitbucket, IsCI: true, Ref: "v1"},
		},
		{
			name: "jenkins change",
			env: map[string]string{
				"JENKINS_URL": "https://ci", "GIT_COMMIT": "abc", "BRANCH_NAME": "PR-4",
				"CHANGE_ID": "4", "CHANGE_TARGET": "main", "WORKSPACE": "/j",
			},
			info: Info{Vendor: VendorJenkins, IsCI: true, IsPR: true, SHA: "abc", Ref: "PR-4", BaseRef: "main", PRNumber: "4"},
			rt:   Runtime{Workspace: "/j"},
		},
		{
			name: "jenkins git branch",
			env:  map[string]string{"JENKINS_URL": "https://ci", "GIT_BRANCH": "origin/main"},
			info: Info{Vendor: VendorJenkins, IsCI: true, Ref: "origin/main"},
		},
		{
			name: "circleci pull request url",
			env: map[string]string{
				"CIRCLECI": "true", "CIRCLE_SHA1": "abc", "CIRCLE_BRANCH": "feature",
				"CIRCLE_PULL_REQUEST": "https://example.com/o/r/pull/31", "CIRCLE_WORKING_DIRECTORY": "~/project",
			},
			info: Info{Vendor: VendorCircleCI, IsCI: true, IsPR: true, SHA: "abc", Ref: "feature", PRNumber: "31"},
			rt:   Runtime{Workspace: "~/project"},
		},
		{
			name: "circleci fork number and tag",
			env:  map[string]string{"CIRCLECI": "true", "CIRCLE_TAG": "v2", "CIRCLE_PR_NUMBER": "6"},
			info: Info{Vendor: VendorCircleCI, IsCI: true, IsPR: true, Ref: "v2", PRNumber: "6"},
		},
		{
			name: "a vendor wins over generic",
			env:  map[string]string{"CI": "true", "GITLAB_CI": "true"},
			info: Info{Vendor: VendorGitLab, IsCI: true},
		},
		{
			name: "a marker that is not true",
			env:  map[string]string{"GITHUB_ACTIONS": "false", "CI": "true"},
			info: Info{Vendor: VendorGeneric, IsCI: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, rt := Detect(lookup(tt.env))
			if info != tt.info {
				t.Errorf("Info = %+v, want %+v", info, tt.info)
			}
			if rt != tt.rt {
				t.Errorf("Runtime = %+v, want %+v", rt, tt.rt)
			}
		})
	}
}

// Variables is the list the blackbox harness strips: a name Detect reads that
// it leaves out would let a CI job change a golden. Every vendor's marker is
// set in turn, so every branch of Detect reads its names.
func TestVariablesListsEveryNameDetectReads(t *testing.T) {
	listed := Variables()
	if !slices.IsSorted(listed) {
		t.Errorf("Variables() = %v, want it sorted", listed)
	}
	read := map[string]bool{}
	record := func(env map[string]string) func(string) string {
		return func(name string) string {
			read[name] = true
			return env[name]
		}
	}
	markers := []map[string]string{
		{"GITEA_ACTIONS": "true"},
		{"FORGEJO_ACTIONS": "true"},
		{"GITHUB_ACTIONS": "true"},
		{"GITLAB_CI": "true"},
		{"TF_BUILD": "True"},
		{"TEAMCITY_VERSION": "1"},
		{"BUILDKITE": "true"},
		{"BITBUCKET_BUILD_NUMBER": "1"},
		{"JENKINS_URL": "x"},
		{"CIRCLECI": "true", "CIRCLE_PULL_REQUEST": "x/1"},
		{"CI": "true"},
		{},
	}
	for _, env := range markers {
		Detect(record(env))
	}
	for name := range read {
		if !slices.Contains(listed, name) {
			t.Errorf("Detect reads %s, which Variables() does not list", name)
		}
	}
	for _, name := range listed {
		if !read[name] {
			t.Errorf("Variables() lists %s, which Detect never reads", name)
		}
	}
}
