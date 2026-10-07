// Package cienv detects the continuous-integration system a process runs
// under, from the variables each vendor sets for its own jobs. Configuration
// code reads the answer as facts().ci; the runner reads it to decide where
// annotations and a step summary go.
//
// These are third-party variables, so they are read here rather than through
// internal/env, which owns datamitsu's own. Detect takes the lookup as a
// parameter: the answer is a function of the environment it is given, which is
// what facts() and the tests both need.
package cienv

import (
	"os"
	"slices"
	"strings"
)

// Vendor values of Info.Vendor.
const (
	VendorGitHub    = "github"
	VendorGitLab    = "gitlab"
	VendorAzure     = "azure"
	VendorTeamCity  = "teamcity"
	VendorBuildkite = "buildkite"
	VendorBitbucket = "bitbucket"
	VendorJenkins   = "jenkins"
	VendorCircleCI  = "circleci"
	VendorGitea     = "gitea"
	// VendorGeneric is a CI that sets CI and nothing a vendor is known by.
	VendorGeneric = "generic"
)

// Info is what configuration code sees of the CI as facts().ci: which system
// runs the job and the identifiers of the change it builds. Every field is
// empty or false outside CI.
type Info struct {
	Vendor string `json:"vendor"`
	IsCI   bool   `json:"isCI"`
	// IsPR marks a job that builds a pull or merge request.
	IsPR bool   `json:"isPR"`
	SHA  string `json:"sha"`
	Ref  string `json:"ref"`
	// BaseRef is the branch a pull request targets, when the vendor says.
	BaseRef  string `json:"baseRef"`
	PRNumber string `json:"prNumber"`
}

// Runtime is what the runner needs besides Info to write to the CI; it is
// never exposed to configuration code.
type Runtime struct {
	// EventName is GITHUB_EVENT_NAME: push, pull_request, …
	EventName string
	// Workspace is the directory the vendor checked the repository out into.
	Workspace string
	// StepSummaryPath is GITHUB_STEP_SUMMARY, the Markdown file a GitHub step
	// appends to; empty where the vendor has none.
	StepSummaryPath string
	// EventPath is GITHUB_EVENT_PATH, the JSON of the event that started the
	// job.
	EventPath string
}

// vendorRule recognizes one vendor and reads its identifiers.
type vendorRule struct {
	vendor string
	// marker is true when the vendor's own marker says the job runs there.
	marker func(getenv func(string) string) bool
	read   func(getenv func(string) string) (Info, Runtime)
}

// rules are checked in order: Gitea and Forgejo set GITHUB_ACTIONS=true as
// well, and support neither annotations nor a step summary, so they come
// before GitHub.
var rules = []vendorRule{
	{VendorGitea, func(g func(string) string) bool {
		return isTrue(g("GITEA_ACTIONS")) || isTrue(g("FORGEJO_ACTIONS"))
	}, readGitea},
	{VendorGitHub, func(g func(string) string) bool { return isTrue(g("GITHUB_ACTIONS")) }, readGitHub},
	{VendorGitLab, func(g func(string) string) bool { return isTrue(g("GITLAB_CI")) }, readGitLab},
	{VendorAzure, func(g func(string) string) bool { return isTrue(g("TF_BUILD")) }, readAzure},
	{VendorTeamCity, func(g func(string) string) bool { return g("TEAMCITY_VERSION") != "" }, readTeamCity},
	{VendorBuildkite, func(g func(string) string) bool { return isTrue(g("BUILDKITE")) }, readBuildkite},
	{VendorBitbucket, func(g func(string) string) bool { return g("BITBUCKET_BUILD_NUMBER") != "" }, readBitbucket},
	{VendorJenkins, func(g func(string) string) bool { return g("JENKINS_URL") != "" }, readJenkins},
	{VendorCircleCI, func(g func(string) string) bool { return isTrue(g("CIRCLECI")) }, readCircleCI},
}

// Detect reports the CI a process with the environment getenv runs under.
// Without a vendor's marker, a CI variable other than false or 0 makes it
// generic; without either, Info is empty.
func Detect(getenv func(string) string) (Info, Runtime) {
	for _, r := range rules {
		if r.marker(getenv) {
			info, rt := r.read(getenv)
			info.Vendor, info.IsCI = r.vendor, true
			return info, rt
		}
	}
	if ci := strings.TrimSpace(getenv("CI")); ci != "" && !strings.EqualFold(ci, "false") && ci != "0" {
		return Info{Vendor: VendorGeneric, IsCI: true}, Runtime{}
	}
	return Info{}, Runtime{}
}

// Current is Detect over the process environment.
func Current() (Info, Runtime) {
	return Detect(os.Getenv) //nolint:forbidigo // CI vendors' variables, not datamitsu's own
}

// Variables lists, sorted, every name Detect may read. The blackbox harness
// strips them all, so a golden recorded inside a CI job is the one a shell
// records.
func Variables() []string {
	names := slices.Clone(variables)
	slices.Sort(names)
	return slices.Compact(names)
}

// cspell:ignore SOURCEVERSION SOURCEBRANCH SOURCESDIRECTORY PULLREQUESTID PULLREQUESTNUMBER TARGETBRANCH
var variables = []string{
	"CI",
	"GITEA_ACTIONS", "FORGEJO_ACTIONS",
	"GITHUB_ACTIONS", "GITHUB_SHA", "GITHUB_REF", "GITHUB_BASE_REF", "GITHUB_EVENT_NAME",
	"GITHUB_WORKSPACE", "GITHUB_STEP_SUMMARY", "GITHUB_EVENT_PATH",
	"GITLAB_CI", "CI_COMMIT_SHA", "CI_COMMIT_REF_NAME", "CI_MERGE_REQUEST_IID",
	"CI_MERGE_REQUEST_TARGET_BRANCH_NAME", "CI_PROJECT_DIR",
	"TF_BUILD", "BUILD_SOURCEVERSION", "BUILD_SOURCEBRANCH", "BUILD_REASON",
	"SYSTEM_PULLREQUEST_PULLREQUESTNUMBER", "SYSTEM_PULLREQUEST_PULLREQUESTID",
	"SYSTEM_PULLREQUEST_TARGETBRANCH", "BUILD_SOURCESDIRECTORY",
	"TEAMCITY_VERSION", "BUILD_VCS_NUMBER",
	"BUILDKITE", "BUILDKITE_COMMIT", "BUILDKITE_BRANCH", "BUILDKITE_PULL_REQUEST",
	"BUILDKITE_PULL_REQUEST_BASE_BRANCH", "BUILDKITE_BUILD_CHECKOUT_PATH",
	"BITBUCKET_BUILD_NUMBER", "BITBUCKET_COMMIT", "BITBUCKET_BRANCH", "BITBUCKET_TAG", "BITBUCKET_PR_ID",
	"BITBUCKET_PR_DESTINATION_BRANCH", "BITBUCKET_CLONE_DIR",
	"JENKINS_URL", "GIT_COMMIT", "BRANCH_NAME", "GIT_BRANCH", "CHANGE_ID", "CHANGE_TARGET", "WORKSPACE",
	"CIRCLECI", "CIRCLE_SHA1", "CIRCLE_BRANCH", "CIRCLE_TAG", "CIRCLE_PULL_REQUEST", "CIRCLE_PR_NUMBER",
	"CIRCLE_WORKING_DIRECTORY",
}

func readGitHub(g func(string) string) (Info, Runtime) {
	info, rt := readGitHubShape(g)
	rt.StepSummaryPath = g("GITHUB_STEP_SUMMARY")
	return info, rt
}

// readGitea reads the GITHUB_* variables Gitea and Forgejo set too; neither
// has a step summary.
func readGitea(g func(string) string) (Info, Runtime) {
	return readGitHubShape(g)
}

func readGitHubShape(g func(string) string) (Info, Runtime) {
	event := g("GITHUB_EVENT_NAME")
	ref := g("GITHUB_REF")
	info := Info{
		SHA:     g("GITHUB_SHA"),
		Ref:     ref,
		BaseRef: g("GITHUB_BASE_REF"),
		IsPR:    event == "pull_request" || event == "pull_request_target",
	}
	// refs/pull/<n>/merge for pull_request; pull_request_target builds the
	// base branch, whose ref carries no number.
	if rest, ok := strings.CutPrefix(ref, "refs/pull/"); ok {
		if n, _, found := strings.Cut(rest, "/"); found && isNumber(n) {
			info.PRNumber = n
		}
	}
	return info, Runtime{EventName: event, Workspace: g("GITHUB_WORKSPACE"), EventPath: g("GITHUB_EVENT_PATH")}
}

func readGitLab(g func(string) string) (Info, Runtime) {
	iid := g("CI_MERGE_REQUEST_IID")
	return Info{
		SHA:      g("CI_COMMIT_SHA"),
		Ref:      g("CI_COMMIT_REF_NAME"),
		BaseRef:  g("CI_MERGE_REQUEST_TARGET_BRANCH_NAME"),
		IsPR:     iid != "",
		PRNumber: iid,
	}, Runtime{Workspace: g("CI_PROJECT_DIR")}
}

func readAzure(g func(string) string) (Info, Runtime) {
	number := g("SYSTEM_PULLREQUEST_PULLREQUESTNUMBER")
	if number == "" {
		number = g("SYSTEM_PULLREQUEST_PULLREQUESTID")
	}
	return Info{
		SHA:      g("BUILD_SOURCEVERSION"),
		Ref:      g("BUILD_SOURCEBRANCH"),
		BaseRef:  g("SYSTEM_PULLREQUEST_TARGETBRANCH"),
		IsPR:     g("BUILD_REASON") == "PullRequest",
		PRNumber: number,
	}, Runtime{Workspace: g("BUILD_SOURCESDIRECTORY")}
}

// readTeamCity reads the revision; the branch and a pull request are build
// parameters TeamCity does not export by default.
func readTeamCity(g func(string) string) (Info, Runtime) {
	return Info{SHA: g("BUILD_VCS_NUMBER")}, Runtime{}
}

func readBuildkite(g func(string) string) (Info, Runtime) {
	pr := g("BUILDKITE_PULL_REQUEST")
	if pr == "false" {
		pr = ""
	}
	return Info{
		SHA:      g("BUILDKITE_COMMIT"),
		Ref:      g("BUILDKITE_BRANCH"),
		BaseRef:  g("BUILDKITE_PULL_REQUEST_BASE_BRANCH"),
		IsPR:     pr != "",
		PRNumber: pr,
	}, Runtime{Workspace: g("BUILDKITE_BUILD_CHECKOUT_PATH")}
}

func readBitbucket(g func(string) string) (Info, Runtime) {
	ref := g("BITBUCKET_BRANCH")
	if ref == "" {
		ref = g("BITBUCKET_TAG")
	}
	pr := g("BITBUCKET_PR_ID")
	return Info{
		SHA:      g("BITBUCKET_COMMIT"),
		Ref:      ref,
		BaseRef:  g("BITBUCKET_PR_DESTINATION_BRANCH"),
		IsPR:     pr != "",
		PRNumber: pr,
	}, Runtime{Workspace: g("BITBUCKET_CLONE_DIR")}
}

// readJenkins reads what the Git plugin and multi-branch pipelines export.
func readJenkins(g func(string) string) (Info, Runtime) {
	ref := g("BRANCH_NAME")
	if ref == "" {
		ref = g("GIT_BRANCH")
	}
	change := g("CHANGE_ID")
	return Info{
		SHA:      g("GIT_COMMIT"),
		Ref:      ref,
		BaseRef:  g("CHANGE_TARGET"),
		IsPR:     change != "",
		PRNumber: change,
	}, Runtime{Workspace: g("WORKSPACE")}
}

func readCircleCI(g func(string) string) (Info, Runtime) {
	ref := g("CIRCLE_BRANCH")
	if ref == "" {
		ref = g("CIRCLE_TAG")
	}
	url := g("CIRCLE_PULL_REQUEST")
	number := g("CIRCLE_PR_NUMBER")
	if number == "" && url != "" {
		if last := url[strings.LastIndexByte(url, '/')+1:]; isNumber(last) {
			number = last
		}
	}
	return Info{
		SHA:      g("CIRCLE_SHA1"),
		Ref:      ref,
		IsPR:     url != "" || number != "",
		PRNumber: number,
	}, Runtime{Workspace: g("CIRCLE_WORKING_DIRECTORY")}
}

func isTrue(v string) bool {
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
