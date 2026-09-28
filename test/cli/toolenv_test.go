package cli_test

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// probeScript prints the variables a tool could switch its output on and fails,
// so a lint run shows what it saw in its frame. It records no marker: exec runs
// it without the operation's env.
const probeScript = settle + `printf 'CI=%s GITHUB_ACTIONS=%s AI_AGENT=%s FORCE_COLOR=%s NO_COLOR=%s\n' ` +
	`"$CI" "$GITHUB_ACTIONS" "$AI_AGENT" "$FORCE_COLOR" "$NO_COLOR"; exit 1`

// hostEnv is a CI job under an agent with colour forced, as the tool's host.
var hostEnv = []string{"CI=true", "GITHUB_ACTIONS=true", "AI_AGENT=claude", "FORCE_COLOR=3", "NO_COLOR=host"}

func probeProject(t *testing.T, op clitest.ToolOpSpec) *execProject {
	t.Helper()
	return newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
		clitest.ShellTool("probe", probeScript, op))
}

// TestToolEnvironment freezes what fix, lint and check hand a tool: no
// GITHUB_ACTIONS, agent marker or FORCE_COLOR, CI kept, NO_COLOR=1 whatever the
// host says; inheritEnv returns a stripped variable with the host's value; env
// cannot set NO_COLOR; exec hands its app the host environment unchanged. The
// host is a GitHub job, whose annotations annotations_test.go freezes.
func TestToolEnvironment(t *testing.T) {
	t.Run("lint strips and sets", func(t *testing.T) {
		e := probeProject(t, clitest.ToolOpSpec{})
		res := e.run("", hostEnv, "lint", "--annotations", "off")
		e.wantExit(res, 1)
		if !strings.Contains(res.Stdout, "CI=true GITHUB_ACTIONS= AI_AGENT= FORCE_COLOR= NO_COLOR=1\n") {
			t.Errorf("the tool saw another environment:\n%s", res.Stdout)
		}
		e.golden("toolenv_lint", res)
	})

	t.Run("inheritEnv hands one back", func(t *testing.T) {
		e := probeProject(t, clitest.ToolOpSpec{InheritEnv: []string{"GITHUB_ACTIONS"}})
		res := e.run("", hostEnv, "lint", "--annotations", "off")
		e.wantExit(res, 1)
		if !strings.Contains(res.Stdout, "CI=true GITHUB_ACTIONS=true AI_AGENT= FORCE_COLOR= NO_COLOR=1\n") {
			t.Errorf("the tool should see the host's GITHUB_ACTIONS and nothing else stripped:\n%s", res.Stdout)
		}
		e.golden("toolenv_lint_inherit", res)
	})

	t.Run("env cannot set NO_COLOR", func(t *testing.T) {
		e := probeProject(t, clitest.ToolOpSpec{Env: map[string]string{"NO_COLOR": ""}})
		res := e.run("", hostEnv, "lint")
		e.wantExit(res, 1)
		if !strings.Contains(res.Stderr, `env "NO_COLOR": datamitsu sets NO_COLOR=1`) {
			t.Errorf("the config should be refused:\n%s", res.Stderr)
		}
		if strings.Contains(res.Stdout, "CI=") {
			t.Errorf("a refused config ran its tool:\n%s", res.Stdout)
		}
		e.golden("toolenv_env_no_color", res)
	})

	t.Run("exec changes nothing", func(t *testing.T) {
		e := probeProject(t, clitest.ToolOpSpec{})
		res := e.run("", hostEnv, "exec", "probe")
		e.wantExit(res, 1)
		if want := "CI=true GITHUB_ACTIONS=true AI_AGENT=claude FORCE_COLOR=3 NO_COLOR=host\n"; res.Stdout != want {
			t.Errorf("exec stdout = %q, want the host environment %q", res.Stdout, want)
		}
	})
}
