package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/cienv"
)

func TestResolveAnnotations(t *testing.T) {
	tests := []struct {
		name          string
		requested     string
		vendor        string
		quiet, stdout bool
		mode, why     string
		recorded      bool
		export        string
	}{
		{name: "auto on GitHub", requested: AnnotationsAuto, vendor: cienv.VendorGitHub, mode: AnnotationsGitHub, recorded: true, export: "github-annotations"},
		{name: "auto on Azure", requested: AnnotationsAuto, vendor: cienv.VendorAzure, mode: AnnotationsAzure, recorded: true, export: "azure-annotations"},
		{name: "auto on TeamCity", requested: AnnotationsAuto, vendor: cienv.VendorTeamCity, mode: AnnotationsTeamCity, recorded: true, export: "teamcity-annotations"},
		{name: "auto elsewhere", requested: AnnotationsAuto, vendor: cienv.VendorGitLab, mode: AnnotationsOff, why: "not a CI job that reads annotations"},
		{name: "auto on Gitea", requested: AnnotationsAuto, vendor: cienv.VendorGitea, mode: AnnotationsOff, why: "not a CI job that reads annotations"},
		{
			name: "auto beside a document", requested: AnnotationsAuto, vendor: cienv.VendorGitHub, quiet: true, stdout: true,
			mode: AnnotationsOff, why: "stdout carries a document", recorded: true, export: "github-annotations",
		},
		{
			name: "auto beside a stream", requested: AnnotationsAuto, vendor: cienv.VendorTeamCity, quiet: true,
			mode: AnnotationsOff, why: "the run writes a JSON-L event stream", recorded: true, export: "teamcity-annotations",
		},
		{name: "github anywhere", requested: AnnotationsGitHub, quiet: true, mode: AnnotationsGitHub, recorded: true, export: "github-annotations"},
		{name: "azure on GitHub", requested: AnnotationsAzure, vendor: cienv.VendorGitHub, mode: AnnotationsAzure, recorded: true, export: "azure-annotations"},
		{name: "off on GitHub", requested: AnnotationsOff, vendor: cienv.VendorGitHub, mode: AnnotationsOff, why: "--annotations off", recorded: true, export: "github-annotations"},
		{name: "off elsewhere", requested: AnnotationsOff, mode: AnnotationsOff, why: "--annotations off"},
		{name: "a continuation asks for none", vendor: cienv.VendorGitHub, mode: AnnotationsOff, export: "github-annotations"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := resolveAnnotations(tt.requested, tt.vendor, tt.quiet, tt.stdout)
			if st.mode != tt.mode || st.why != tt.why || st.recorded != tt.recorded || st.export != tt.export {
				t.Errorf("resolveAnnotations = mode %q, why %q, recorded %v, export %q; want %q, %q, %v, %q",
					st.mode, st.why, st.recorded, st.export, tt.mode, tt.why, tt.recorded, tt.export)
			}
		})
	}
}

// TestNeutralizerOf: a CI that reads commands anywhere in a line gets them
// broken in tool text, whether the run prints its annotations or not.
func TestNeutralizerOf(t *testing.T) {
	const line = "x ##vso[task.setvariable variable=a]1 ##teamcity[buildProblem description='b'] ::error::c"
	const azureBroken = "x ##vso [task.setvariable variable=a]1 ##teamcity[buildProblem description='b'] ::error::c"
	const teamcityBroken = "x ##vso[task.setvariable variable=a]1 ##teamcity [buildProblem description='b'] ::error::c"
	tests := []struct {
		mode, vendor, want string
	}{
		{AnnotationsAzure, "", azureBroken},
		{AnnotationsOff, cienv.VendorAzure, azureBroken},
		{AnnotationsTeamCity, "", teamcityBroken},
		{AnnotationsOff, cienv.VendorTeamCity, teamcityBroken},
		{AnnotationsAzure, cienv.VendorTeamCity, "x ##vso [task.setvariable variable=a]1 ##teamcity [buildProblem description='b'] ::error::c"},
		{AnnotationsGitHub, cienv.VendorGitHub, line},
		{AnnotationsOff, "", line},
	}
	for _, tt := range tests {
		if got := neutralizerOf(tt.mode, tt.vendor)(line); got != tt.want {
			t.Errorf("%s under %q: %q, want %q", tt.mode, tt.vendor, got, tt.want)
		}
	}
}

// fixtureRepo builds a pull request's merge commit: a.txt on the base, b.txt
// on the branch, c.txt on the base after the branch left it, and the merge.
// It returns the repository, the first commit and the merge commit.
func fixtureRepo(t *testing.T) (repo, first, merge string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(file string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, file), []byte(file+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", file)
		run("commit", "-q", "-m", file)
	}
	run("init", "-q", "-b", "main")
	run("config", "commit.gpgSign", "false")
	commit("a.txt")
	first = run("rev-parse", "HEAD")
	run("checkout", "-q", "-b", "pr")
	commit("b.txt")
	run("checkout", "-q", "main")
	commit("c.txt")
	run("merge", "-q", "--no-ff", "-m", "merge", "pr")
	return repo, first, run("rev-parse", "HEAD")
}

func TestTouchedFiles(t *testing.T) {
	repo, first, merge := fixtureRepo(t)
	ctx := context.Background()

	event := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "event.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("pull_request diffs the merge against the base tip", func(t *testing.T) {
		touched, why := TouchedFiles(ctx, repo, cienv.Info{SHA: merge}, cienv.Runtime{EventName: "pull_request"})
		if why != "" || len(touched) != 1 || !touched["b.txt"] {
			t.Errorf("touched = %v, why %q; want only the branch's b.txt", touched, why)
		}
	})

	t.Run("pull_request not on the merge commit", func(t *testing.T) {
		_, why := TouchedFiles(ctx, repo, cienv.Info{SHA: first}, cienv.Runtime{EventName: "pull_request"})
		if why != "HEAD is not the merge commit" {
			t.Errorf("why = %q", why)
		}
	})

	t.Run("pull_request in a clone of depth 1", func(t *testing.T) {
		shallow := filepath.Join(t.TempDir(), "shallow")
		cmd := exec.Command("git", "clone", "-q", "--depth", "1", "file://"+repo, shallow)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("shallow clone: %v\n%s", err, out)
		}
		_, why := TouchedFiles(ctx, shallow, cienv.Info{SHA: merge}, cienv.Runtime{EventName: "pull_request"})
		if why != "HEAD^1 not fetched (set fetch-depth: 2)" {
			t.Errorf("why = %q", why)
		}
	})

	t.Run("push diffs from the commit it started at", func(t *testing.T) {
		touched, why := TouchedFiles(ctx, repo, cienv.Info{}, cienv.Runtime{EventName: "push", EventPath: event(t, `{"before":"`+first+`"}`)})
		if why != "" || len(touched) != 2 || !touched["b.txt"] || !touched["c.txt"] {
			t.Errorf("touched = %v, why %q; want b.txt and c.txt", touched, why)
		}
	})

	t.Run("push from a commit not fetched", func(t *testing.T) {
		_, why := TouchedFiles(ctx, repo, cienv.Info{}, cienv.Runtime{
			EventName: "push",
			EventPath: event(t, `{"before":"1111111111111111111111111111111111111111"}`),
		})
		if why != "before commit not fetched (set fetch-depth: 0)" {
			t.Errorf("why = %q", why)
		}
	})

	t.Run("push that created its branch", func(t *testing.T) {
		_, why := TouchedFiles(ctx, repo, cienv.Info{}, cienv.Runtime{
			EventName: "push",
			EventPath: event(t, `{"before":"0000000000000000000000000000000000000000"}`),
		})
		if why != "the push names no commit it started from" {
			t.Errorf("why = %q", why)
		}
	})

	t.Run("push whose before is not an object name", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "written")
		_, why := TouchedFiles(ctx, repo, cienv.Info{}, cienv.Runtime{
			EventName: "push",
			EventPath: event(t, `{"before":"--output=`+filepath.ToSlash(out)+`"}`),
		})
		if why != "the event names no commit" {
			t.Errorf("why = %q", why)
		}
		if _, err := os.Stat(out); err == nil {
			t.Error("an event's before reached git as an option")
		}
	})

	t.Run("an event without a change", func(t *testing.T) {
		_, why := TouchedFiles(ctx, repo, cienv.Info{}, cienv.Runtime{EventName: "workflow_dispatch"})
		if why != "a workflow_dispatch event names no change" {
			t.Errorf("why = %q", why)
		}
	})
}

// TestAzureTouchedFiles: an Azure pull request build diffs HEAD against its
// merge base with the target branch's remote-tracking ref.
func TestAzureTouchedFiles(t *testing.T) {
	repo, _, _ := fixtureRepo(t)
	ctx := context.Background()
	cmd := exec.Command("git", "update-ref", "refs/remotes/origin/main", "HEAD^1")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v\n%s", err, out)
	}
	touched, why := AzureTouchedFiles(ctx, repo, cienv.Info{BaseRef: "refs/heads/main"})
	if why != "" || len(touched) != 1 || !touched["b.txt"] {
		t.Errorf("touched = %v, why %q; want only the branch's b.txt", touched, why)
	}
	if _, why := AzureTouchedFiles(ctx, repo, cienv.Info{BaseRef: "refs/heads/release"}); why != "origin/release not fetched" {
		t.Errorf("why = %q", why)
	}
	if _, why := AzureTouchedFiles(ctx, repo, cienv.Info{}); why != "not a pull request build" {
		t.Errorf("why = %q", why)
	}
}
