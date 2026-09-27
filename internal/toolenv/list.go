package toolenv

import (
	"maps"
	"slices"
	"strings"
)

// Entry is one variable name, or one prefix, with the reason it is listed:
// which tool reacts to it and how. The reference page
// website/docs/reference/tool-environment.md is generated from these lists
// (task gen:toolenv-doc), so a reason is written for that page's reader.
type Entry struct {
	Name string
	Why  string
}

const agentFormat = "oxlint switches to its one-line agent output format"

var exactNames = []Entry{
	{"GITHUB_ACTIONS", "Set in every GitHub Actions job. editorconfig-checker, oxlint, sqruff, pinact and yamllint (without `-f`) switch to `::error` workflow commands, which a parser reading their usual format takes for clean output."},
	{"AI_AGENT", "Names the AI agent running the session; " + agentFormat + "."},
	{"AGENT", "Generic AI agent session marker, removed before the next agent-aware tool starts reading it."},
	{"CLAUDECODE", "Set by Claude Code; " + agentFormat + "."},
	{"CLAUDE_CODE", "Claude Code marker; " + agentFormat + "."},
	{"CLAUDE_CODE_CHILD_SESSION", "Claude Code session marker, removed with the others."},
	{"GEMINI_CLI", "Set by Gemini CLI; " + agentFormat + "."},
	{"CURSOR_AGENT", "Set by the Cursor agent; " + agentFormat + "."},
	{"OPENCODE", "Set by OpenCode; " + agentFormat + "."},
	{"AUGMENT_AGENT", "Augment agent session marker, removed with the others."},
	{"FORCE_COLOR", "Forces ANSI colour into a pipe; Node's colour detection reads it before `NO_COLOR`. Removed from the host environment, and datamitsu no longer adds it."},
	{"CLICOLOR_FORCE", "Forces ANSI colour into a pipe. Removed from the host environment, and datamitsu no longer adds it."},
}

var prefixes = []Entry{
	{"CODEX_", "OpenAI Codex CLI (`CODEX_SANDBOX`, `CODEX_THREAD_ID`); " + agentFormat + "."},
	{"COPILOT_", "GitHub Copilot CLI (`COPILOT_CLI`); " + agentFormat + "."},
	{"JUNIE_", "JetBrains Junie (`JUNIE_DATA`, `JUNIE_SHIM_PATH`); " + agentFormat + "."},
}

const noFormatSwitch = "no tool is known to change its output format on it, and a tool may read it for other things, such as a build's base."

// kept are variables left alone on purpose although a tool reads them. A
// Name ending in "*" is a prefix.
var kept = []Entry{
	{"CI", "Tools branch on it for good reasons, such as knip's plugins and tools that look up a pull request's base; without it a run in CI would look local to them."},
	{"GITHUB_*", "Every one but `GITHUB_ACTIONS`: tokens (gitleaks, zizmor and trufflehog read `GITHUB_TOKEN`), the workspace, and the refs a tool reads to find a pull request's base."},
	{"GITLAB_CI", "GitLab CI; " + noFormatSwitch},
	{"CI_*", "GitLab CI; " + noFormatSwitch},
	{"TF_BUILD", "Azure Pipelines; " + noFormatSwitch},
	{"TEAMCITY_VERSION", "TeamCity; " + noFormatSwitch},
	{"BUILDKITE*", "Buildkite; " + noFormatSwitch},
	{"BITBUCKET_*", "Bitbucket Pipelines; " + noFormatSwitch},
	{"JENKINS_URL", "Jenkins; " + noFormatSwitch},
	{"REPL_ID", "Set for every process on Replit, people included, so it is not an agent marker, although oxlint reads it as one."},
	{"CURSOR_TRACE_ID", "Set in every Cursor terminal, people included, so it is not an agent marker."},
	{"TERM", "The terminal type; colour is decided by `NO_COLOR`."},
	{"PATH", "oxlint reads a `.pi/agent` segment in it as an agent marker; no stripping can address that."},
	{"EDITOR", "oxlint reads `devin` in it as an agent marker; datamitsu leaves the user's editor alone."},
	{"TERM_PROGRAM", "oxlint reads `kiro` in it as an agent marker; datamitsu leaves it alone."},
}

// NoColor is the one variable nothing may override: Apply sets it after every
// layer. Parsed output must carry no colour.
const NoColor = "NO_COLOR=1"

// Entries returns the stripped names and prefixes.
func Entries() (exact, prefix []Entry) {
	return slices.Clone(exactNames), slices.Clone(prefixes)
}

// Kept returns the variables left alone on purpose, with the reason.
func Kept() []Entry {
	return slices.Clone(kept)
}

// Stripped reports whether a tool of fix, lint or check is started without
// the host's value of name.
func Stripped(name string) bool {
	return strippedBy(name, exactNames, prefixes)
}

func strippedBy(name string, exact, prefix []Entry) bool {
	name = canonical(name)
	for _, e := range exact {
		if name == e.Name {
			return true
		}
	}
	for _, e := range prefix {
		if strings.HasPrefix(name, e.Name) {
			return true
		}
	}
	return false
}

// Apply returns the environment of a tool of fix, lint or check. base is the
// environment datamitsu would start it with; every Stripped variable is
// dropped from it. inherited (Resolve) is added back, the layers — the app's
// env, then the operation's — are applied in order, a later value winning, and
// NO_COLOR=1 is set last.
func Apply(base, inherited []string, layers ...map[string]string) []string {
	extra := len(inherited) + 1
	for _, layer := range layers {
		extra += len(layer)
	}
	env := make([]string, 0, len(base)+extra)
	at := make(map[string]int, len(base)+extra)
	set := func(name, kv string) {
		key := canonical(name)
		if i, ok := at[key]; ok {
			env[i] = kv
			return
		}
		at[key] = len(env)
		env = append(env, kv)
	}

	for _, kv := range base {
		name, _, found := strings.Cut(kv, "=")
		if !found || name == "" {
			// Windows keeps per-drive working directories as "=C:=C:\dir".
			env = append(env, kv)
			continue
		}
		if !Stripped(name) {
			set(name, kv)
		}
	}
	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		set(name, kv)
	}
	for _, layer := range layers {
		for _, name := range slices.Sorted(maps.Keys(layer)) {
			set(name, name+"="+layer[name])
		}
	}
	name, _, _ := strings.Cut(NoColor, "=")
	set(name, NoColor)
	return env
}
