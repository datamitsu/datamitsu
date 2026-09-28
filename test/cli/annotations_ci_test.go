// cspell:ignore SOURCEVERSION

package cli_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// This file freezes the Azure Pipelines and TeamCity annotations: when auto
// picks them, what they print, and that no tool can issue a command of its own
// through the run's output.

var (
	azureEnv    = []string{"TF_BUILD=True", "BUILD_SOURCEVERSION=0123456789abcdef0123456789abcdef01234567", "BUILD_REASON=IndividualCI"}
	teamcityEnv = []string{"TEAMCITY_VERSION=2025.03 (build 186049)", "BUILD_VCS_NUMBER=0123456789abcdef0123456789abcdef01234567"}
)

// ciInjectedFinding is hadolint's JSON with a message that carries both CIs'
// commands.
const ciInjectedFinding = `[{"file":"Dockerfile","line":1,"column":1,"level":"error","code":"DL3006",` +
	`"message":"Always tag it ##vso[task.complete result=Failed] ##teamcity[buildStatus status=FAILURE]"}]`

// ciProject is a repository with a parsed tool whose message tries to issue a
// command, and an unparsed tool that fails printing commands of its own — one
// that would turn TeamCity's reading back on first.
func ciProject(t *testing.T) *execProject {
	t.Helper()
	e := newExecProject(t, map[string]string{"fixture.marker": "", "Dockerfile": "FROM debian\n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	hadolint := clitest.ShellTool("hadolint",
		settle+clitest.RecordRun+`; printf '%s\n' '`+ciInjectedFinding+`'; exit 1`,
		clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/Dockerfile"}, Args: []string{"{file}"}, Parser: "hadolint"})
	printer := clitest.ShellTool("beta", settle+clitest.RecordRun+
		`; echo '##vso[task.setvariable variable=token]stolen'; echo '##teamcity[enableServiceMessages]';`+
		` echo "##teamcity[buildProblem description='injected']"; exit 1`, clitest.ToolOpSpec{})
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, hadolint, printer))
	return e
}

// TestAnnotationsAzure: in Azure Pipelines the run logs its issues, and every
// command a tool printed is broken, wherever in its line it stands.
func TestAnnotationsAzure(t *testing.T) {
	e := ciProject(t)
	res := e.run("", azureEnv, "lint", "--fail-fast=false")
	e.wantExit(res, 1)
	var issues []string
	for line := range strings.SplitSeq(res.Stdout, "\n") {
		if !strings.Contains(line, "##vso[") {
			continue
		}
		if !strings.HasPrefix(line, "##vso[task.logissue ") {
			t.Errorf("a tool's command reached the log: %q", line)
		}
		issues = append(issues, line)
	}
	want := []string{
		"##vso[task.logissue type=error;sourcepath=Dockerfile;linenumber=1;columnnumber=1;code=hadolint(DL3006)]" +
			"Always tag it ##vso [task.complete result=Failed%5D ##teamcity[buildStatus status=FAILURE%5D",
		"##vso[task.logissue type=error;code=beta]beta exited 1 without parsable findings",
	}
	if strings.Join(issues, "\n") != strings.Join(want, "\n") {
		t.Errorf("issues =\n%s\nwant\n%s", strings.Join(issues, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(res.Stdout, "  │  ##vso [task.setvariable variable=token]stolen\n") {
		t.Errorf("the tool's own command should print broken:\n%s", res.Stdout)
	}
	e.golden("annotations_azure", res)

	t.Run("agent", func(t *testing.T) {
		res := e.run("", azureEnv, "lint", "--fail-fast=false", "--output", "agent")
		e.wantExit(res, 1)
		for line := range strings.SplitSeq(res.Stdout, "\n") {
			if strings.Contains(line, "##vso[") && !strings.HasPrefix(line, "##vso[task.logissue ") {
				t.Errorf("a tool's command reached the log through agent output: %q", line)
			}
		}
	})

	t.Run("report_records_them", func(t *testing.T) {
		res := e.run("", azureEnv, "lint", "--report", "json=run.json")
		e.wantExit(res, 1)
		if doc, _ := e.report("run.json"); !strings.Contains(doc, `"format": "azure-annotations"`) {
			t.Errorf("the report should record the annotations:\n%s", doc)
		}
	})
}

// TestAnnotationsTeamCity: in TeamCity the results print while service
// messages are suspended, a tool's own messages are broken so it cannot turn
// the reading back on, and the inspections follow the resume.
func TestAnnotationsTeamCity(t *testing.T) {
	e := ciProject(t)
	res := e.run("", teamcityEnv, "lint", "--fail-fast=false")
	e.wantExit(res, 1)
	disable := strings.Index(res.Stdout, "##teamcity[disableServiceMessages]\n")
	enable := strings.Index(res.Stdout, "##teamcity[enableServiceMessages]\n")
	if disable < 0 || enable < disable || strings.Count(res.Stdout, "##teamcity[enableServiceMessages]") != 1 {
		t.Fatalf("want one suspended region:\n%s", res.Stdout)
	}
	if inside := res.Stdout[disable+1 : enable]; strings.Contains(inside, "##teamcity[") {
		t.Errorf("a service message inside the region was not broken:\n%s", inside)
	}
	after := strings.TrimSpace(res.Stdout[enable:])
	want := "##teamcity[enableServiceMessages]\n" +
		"##teamcity[inspectionType id='hadolint/DL3006' name='DL3006' description='hadolint(DL3006)' category='hadolint']\n" +
		"##teamcity[inspection typeId='hadolint/DL3006' message='Always tag it ##vso|[task.complete result=Failed|] ##teamcity|[buildStatus status=FAILURE|]' file='Dockerfile' line='1' SEVERITY='ERROR']\n" +
		"##teamcity[buildProblem description='beta exited 1 without parsable findings' identity='beta']"
	if !strings.HasPrefix(after, want) {
		t.Errorf("after the region =\n%s\nwant it to start with\n%s", after, want)
	}
	e.golden("annotations_teamcity", res)
}

// TestAnnotationsCIModes: an explicit mode prints its own anywhere and is
// refused beside a document on stdout; auto prints nothing outside a CI that
// reads annotations.
func TestAnnotationsCIModes(t *testing.T) {
	e := ciProject(t)
	res := e.run("", nil, "lint", "--annotations", "teamcity", "--fail-fast=false")
	e.wantExit(res, 1)
	if !strings.Contains(res.Stdout, "##teamcity[inspection ") {
		t.Errorf("--annotations teamcity should print inspections outside TeamCity:\n%s", res.Stdout)
	}
	for _, mode := range []string{"azure", "teamcity"} {
		res := e.run("", nil, "lint", "--annotations", mode, "--report", "json=-")
		e.wantExit(res, 2)
	}
	res = e.run("", []string{"CI=true"}, "lint")
	if strings.Contains(res.Stdout, "##vso[task.logissue") || strings.Contains(res.Stdout, "##teamcity[disableServiceMessages]") {
		t.Errorf("auto printed commands in a CI that reads none:\n%s", res.Stdout)
	}
	res = e.run("", azureEnv, "lint", "--annotations", "off")
	if strings.Contains(res.Stdout, "##vso[task.logissue") {
		t.Errorf("--annotations off printed issues:\n%s", res.Stdout)
	}

	// Another CI's annotations printed in TeamCity keep its messages out, and
	// so does the debug log, which carries the tools' output.
	res = e.run("", teamcityEnv, "--verbose", "lint", "--annotations", "github", "--fail-fast=false")
	e.wantExit(res, 1)
	if strings.Contains(res.Stdout, "##teamcity[") || strings.Contains(res.Stderr, "##teamcity[") {
		t.Errorf("a service message reached the log:\n%s\n%s", res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "::error file=Dockerfile") {
		t.Errorf("the GitHub annotations should print:\n%s", res.Stdout)
	}
}

// TestAnnotationsRepositoryNames: a directory whose name is a command — a pull
// request can add one — prints broken wherever the run names it: a frame's
// directory lines, the progress labels, the event stream, the debug log and
// the plan.
func TestAnnotationsRepositoryNames(t *testing.T) {
	for _, tc := range []struct {
		name, dir, prefix string
		env               []string
	}{
		{"azure", "##vso[task.setvariable variable=token]x", "##vso[", azureEnv},
		{"teamcity", "##teamcity[enableServiceMessages]", "##teamcity[", teamcityEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newExecProject(t, map[string]string{tc.dir + "/pkg.marker": ""}, packagesSpec,
				clitest.ShellTool("alpha", failScript, clitest.ToolOpSpec{Scope: "per-project"}))
			for _, args := range [][]string{
				{"lint"},
				{"--log-format", "jsonl", "lint", "--annotations", tc.name},
				{"--verbose", "lint"},
				{"lint", "--explain"},
				{"lint", "--explain=json"},
			} {
				res := e.run("", tc.env, args...)
				if args[len(args)-1] == "--explain=json" {
					var plan any
					if err := json.Unmarshal([]byte(res.Stdout), &plan); err != nil || !strings.Contains(fmt.Sprint(plan), tc.dir) {
						t.Errorf("the plan should name the directory as it is, read back: %v\n%s", err, res.Stdout)
					}
				}
				if !strings.Contains(args[len(args)-1], "--explain") {
					e.wantExit(res, 1)
				}
				broken := strings.Replace(tc.dir, tc.prefix, strings.TrimSuffix(tc.prefix, "[")+" [", 1)
				if !strings.Contains(args[len(args)-1], "=json") && !strings.Contains(res.Stdout+res.Stderr, broken) {
					t.Errorf("%v: the directory should be named, broken:\n%s\n%s", args, res.Stdout, res.Stderr)
				}
				for _, stream := range []string{res.Stdout, res.Stderr} {
					for line := range strings.SplitSeq(stream, "\n") {
						if strings.Contains(line, tc.prefix) && !strings.HasPrefix(line, "##teamcity[enableServiceMessages]") &&
							!strings.HasPrefix(line, "##teamcity[disableServiceMessages]") && !strings.HasPrefix(line, "##teamcity[buildProblem") &&
							!strings.HasPrefix(line, "##vso[task.logissue") {
							t.Errorf("%v: the directory's name printed as a command: %q", args, line)
						}
					}
				}
			}
		})
	}
}

// TestAnnotationsDocumentOnStdout: a report written to stdout in either CI
// keeps its meaning while no line of it holds a command the CI would run.
func TestAnnotationsDocumentOnStdout(t *testing.T) {
	for _, tc := range []struct {
		name, format, prefix string
		env                  []string
	}{
		{"azure_json", "json", "##vso[", azureEnv},
		{"teamcity_junit", "junit", "##teamcity[", teamcityEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := ciProject(t)
			res := e.run("", tc.env, "lint", "--report", tc.format+"=-")
			e.wantExit(res, 1)
			if strings.Contains(res.Stdout, tc.prefix) || strings.Contains(res.Stderr, tc.prefix) {
				t.Errorf("a command reached the log:\n%s\n%s", res.Stdout, res.Stderr)
			}
			if tc.format == "json" {
				var doc map[string]any
				if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil || !strings.Contains(fmt.Sprint(doc), "##vso[task.complete") {
					t.Errorf("the document should read back the finding as the tool printed it: %v\n%s", err, res.Stdout)
				}
			}

			// The documents the analytics commands print on stdout too.
			e.run("", nil, "lint", "--report", "json=run.json")
			for _, cmd := range []struct {
				args []string
				// lists marks a document that names the finding.
				lists bool
			}{
				{[]string{"report", "diff", "run.json", "again.json"}, true},
				{[]string{"report", "diff", "run.json", "again.json", "--format", "markdown"}, false},
				{[]string{"report", "render", "--input", "run.json", "--format", "markdown"}, true},
			} {
				e.p.WriteFile("again.json", e.read("run.json"))
				res := e.run("", tc.env, cmd.args...)
				e.wantExit(res, 0)
				if strings.Contains(res.Stdout, tc.prefix) || (cmd.lists && !strings.Contains(res.Stdout, "task.complete")) {
					t.Errorf("%v: want the finding without a command:\n%s", cmd.args, res.Stdout)
				}
			}
		})
	}
}

// TestAnnotationsPathsInMessages: a warning or an error that names a path the
// pipeline gave — a baseline, a report — breaks the command it may hold.
func TestAnnotationsPathsInMessages(t *testing.T) {
	e := ciProject(t)
	e.run("", nil, "lint", "--report", "json=out/run.json", "--allow-partial", "Dockerfile")
	const name = "out/##vso[task.setvariable variable=x]y.json"
	e.p.WriteFile(name, e.read("out/run.json"))
	for _, args := range [][]string{
		{"report", "baseline", name, "--output", "out/base.json"},
		{"lint", "--baseline", name},
		{"lint", "--baseline", "out/##vso[task.complete]missing.json"},
	} {
		res := e.run("", azureEnv, args...)
		for line := range strings.SplitSeq(res.Stdout+res.Stderr, "\n") {
			if strings.Contains(line, "##vso[") && !strings.HasPrefix(line, "##vso[task.logissue ") {
				t.Errorf("%v: a command reached the log: %q", args, line)
			}
		}
		if !strings.Contains(res.Stderr, "##vso [") {
			t.Errorf("%v: want the path named, broken:\n%s", args, res.Stderr)
		}
	}
}
