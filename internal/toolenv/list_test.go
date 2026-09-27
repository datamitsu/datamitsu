package toolenv

import (
	"slices"
	"strings"
	"testing"
)

func envMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(env))
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if _, dup := out[name]; dup {
			t.Errorf("%s appears twice in %q", name, env)
		}
		out[name] = value
	}
	return out
}

func TestApply(t *testing.T) {
	host := []string{
		"PATH=/usr/bin", "CI=true", "GITHUB_ACTIONS=true", "GITHUB_TOKEN=t", "GITHUB_BASE_REF=main",
		"AI_AGENT=codex", "AGENT=amp", "CLAUDECODE=1", "CODEX_SANDBOX=seatbelt", "COPILOT_CLI=1",
		"JUNIE_DATA=/j", "REPL_ID=r", "CURSOR_TRACE_ID=c", "FORCE_COLOR=3", "CLICOLOR_FORCE=1",
		"NO_COLOR=", "TERM=xterm", "EMPTY=",
	}

	t.Run("strips the listed names and prefixes, and nothing else", func(t *testing.T) {
		got := envMap(t, Apply(host, nil))
		for _, name := range []string{"GITHUB_ACTIONS", "AI_AGENT", "AGENT", "CLAUDECODE", "CODEX_SANDBOX", "COPILOT_CLI", "JUNIE_DATA", "FORCE_COLOR", "CLICOLOR_FORCE"} {
			if v, ok := got[name]; ok {
				t.Errorf("%s=%s reached the tool", name, v)
			}
		}
		for _, kv := range []string{"PATH=/usr/bin", "CI=true", "GITHUB_TOKEN=t", "GITHUB_BASE_REF=main", "REPL_ID=r", "CURSOR_TRACE_ID=c", "TERM=xterm", "EMPTY="} {
			name, value, _ := strings.Cut(kv, "=")
			if got[name] != value {
				t.Errorf("%s = %q, want the host's %q", name, got[name], value)
			}
		}
		if got["NO_COLOR"] != "1" {
			t.Errorf("NO_COLOR = %q, want 1", got["NO_COLOR"])
		}
	})

	t.Run("NO_COLOR=1 wins over every layer", func(t *testing.T) {
		got := envMap(t, Apply(host, []string{"NO_COLOR="}, map[string]string{"NO_COLOR": ""}, map[string]string{"NO_COLOR": "0"}))
		if got["NO_COLOR"] != "1" {
			t.Errorf("NO_COLOR = %q, want 1", got["NO_COLOR"])
		}
	})

	t.Run("inherited pairs come back with the host's value", func(t *testing.T) {
		inherited := Resolve(host, []string{"GITHUB_ACTIONS", "FORCE_COLOR", "EMPTY", "ABSENT"})
		got := envMap(t, Apply(host, inherited))
		if got["GITHUB_ACTIONS"] != "true" || got["FORCE_COLOR"] != "3" {
			t.Errorf("inherited values = %q, %q, want the host's", got["GITHUB_ACTIONS"], got["FORCE_COLOR"])
		}
		if v, ok := got["EMPTY"]; !ok || v != "" {
			t.Errorf("a present empty value came back as %q (present %v)", v, ok)
		}
		if _, ok := got["ABSENT"]; ok {
			t.Error("an absent variable was added")
		}
		if _, ok := got["AI_AGENT"]; ok {
			t.Error("a stripped variable nobody inherited came back")
		}
	})

	t.Run("env wins over an inherited value, the operation over the app", func(t *testing.T) {
		app := map[string]string{"GITHUB_ACTIONS": "app", "APP_ONLY": "a", "CI": "app"}
		op := map[string]string{"GITHUB_ACTIONS": "false", "CI": "op"}
		got := envMap(t, Apply(host, []string{"GITHUB_ACTIONS=true"}, app, op))
		if got["GITHUB_ACTIONS"] != "false" || got["CI"] != "op" || got["APP_ONLY"] != "a" {
			t.Errorf("GITHUB_ACTIONS=%q CI=%q APP_ONLY=%q, want false, op, a", got["GITHUB_ACTIONS"], got["CI"], got["APP_ONLY"])
		}
	})

	t.Run("an env layer can set a stripped name", func(t *testing.T) {
		got := envMap(t, Apply(host, nil, nil, map[string]string{"FORCE_COLOR": "0"}))
		if got["FORCE_COLOR"] != "0" {
			t.Errorf("FORCE_COLOR = %q, want the operation's 0", got["FORCE_COLOR"])
		}
	})

	t.Run("base is not modified and entries without a name are kept", func(t *testing.T) {
		base := []string{"GITHUB_ACTIONS=true", "=C:=C:\\dir", "A=1"}
		raw := slices.Clone(base)
		got := Apply(base, nil, map[string]string{"A": "2"})
		if !slices.Equal(base, raw) {
			t.Errorf("Apply rewrote base to %q", base)
		}
		if !slices.Contains(got, "=C:=C:\\dir") {
			t.Errorf("Apply dropped a drive directory entry: %q", got)
		}
	})
}

func TestStripped(t *testing.T) {
	for _, name := range []string{"GITHUB_ACTIONS", "CODEX_THREAD_ID", "CODEX_", "COPILOT_MODEL", "JUNIE_SHIM_PATH", "FORCE_COLOR"} {
		if !Stripped(name) {
			t.Errorf("Stripped(%q) = false", name)
		}
	}
	for _, name := range []string{"CI", "GITHUB_TOKEN", "GITHUB_ACTIONS_X", "CODEX", "NO_COLOR", "REPL_ID", "CURSOR_TRACE_ID", "PATH"} {
		if Stripped(name) {
			t.Errorf("Stripped(%q) = true", name)
		}
	}
}

// Every kept entry is a promise that nothing strips it; a Name ending in "*"
// stands for every name it prefixes, other than the stripped ones it names.
func TestKeptIsNeverStripped(t *testing.T) {
	for _, e := range kept {
		name := e.Name
		if prefix, ok := strings.CutSuffix(name, "*"); ok {
			name = prefix + "EXAMPLE"
		}
		if Stripped(name) {
			t.Errorf("kept %s is stripped", e.Name)
		}
		if e.Why == "" {
			t.Errorf("kept %s has no reason", e.Name)
		}
	}
	for _, e := range append(slices.Clone(exactNames), prefixes...) {
		if e.Why == "" {
			t.Errorf("stripped %s has no reason", e.Name)
		}
	}
}

func TestEntriesAreCopies(t *testing.T) {
	exact, prefix := Entries()
	exact[0].Name, prefix[0].Name = "X", "Y"
	Kept()[0].Name = "Z"
	if exactNames[0].Name == "X" || prefixes[0].Name == "Y" || kept[0].Name == "Z" {
		t.Error("a caller rewrote the lists through their accessors")
	}
}
