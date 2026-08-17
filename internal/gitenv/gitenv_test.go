package gitenv

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestSanitizeRemovesEveryHookVar(t *testing.T) {
	for _, name := range hookVars {
		t.Run(name, func(t *testing.T) {
			env := []string{"PATH=/usr/bin", name + "=/somewhere/.git", "HOME=/home/user"}

			got := Sanitize(env)

			for _, entry := range got {
				if strings.HasPrefix(entry, name+"=") {
					t.Errorf("Sanitize() kept %s: %v", name, got)
				}
			}
			if !slices.Equal(got, []string{"PATH=/usr/bin", "HOME=/home/user"}) {
				t.Errorf("Sanitize() = %v, want the unrelated vars kept in order", got)
			}
		})
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{
			name: "unrelated vars survive",
			env:  []string{"PATH=/usr/bin", "GITHUB_TOKEN=secret", "HOME=/home/user"},
			want: []string{"PATH=/usr/bin", "GITHUB_TOKEN=secret", "HOME=/home/user"},
		},
		{
			// Author/committer identity is not repository-binding, so it stays:
			// stripping it would break commits made by tooling under a hook.
			name: "git identity survives",
			env:  []string{"GIT_AUTHOR_NAME=A", "GIT_DIR=/repo/.git", "GIT_COMMITTER_NAME=B"},
			want: []string{"GIT_AUTHOR_NAME=A", "GIT_COMMITTER_NAME=B"},
		},
		{
			name: "similarly named vars survive",
			env:  []string{"GIT_DIRECTORY=/repo", "GIT_DIR=/repo/.git"},
			want: []string{"GIT_DIRECTORY=/repo"},
		},
		{
			name: "entries without = pass through",
			env:  []string{"MALFORMED", "GIT_DIR=/repo/.git"},
			want: []string{"MALFORMED"},
		},
		{
			name: "the whole hook set at once",
			env:  append(hookEnv(), "PATH=/usr/bin"),
			want: []string{"PATH=/usr/bin"},
		},
		{
			name: "empty env",
			env:  []string{},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Sanitize(tt.env)

			if !slices.Equal(got, tt.want) {
				t.Errorf("Sanitize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnvironDropsHookVarsFromProcess(t *testing.T) {
	t.Setenv("GIT_DIR", "/somewhere/.git")
	t.Setenv("GIT_WORK_TREE", "/somewhere")
	t.Setenv("GIT_INDEX_FILE", "/somewhere/.git/index")
	t.Setenv("DATAMITSU_GITENV_MARKER", "kept")

	got := Environ()

	for _, entry := range got {
		for _, name := range hookVars {
			if strings.HasPrefix(entry, name+"=") {
				t.Errorf("Environ() leaked %s", entry)
			}
		}
	}
	if !slices.Contains(got, "DATAMITSU_GITENV_MARKER=kept") {
		t.Error("Environ() dropped an unrelated variable")
	}
}

func TestUnsetClearsProcessEnv(t *testing.T) {
	// t.Setenv registers the restore, so Unset cannot leak into other tests.
	for _, name := range hookVars {
		t.Setenv(name, "/somewhere")
	}
	t.Setenv("DATAMITSU_GITENV_MARKER", "kept")

	Unset()

	for _, name := range hookVars {
		if value, ok := os.LookupEnv(name); ok {
			t.Errorf("Unset() left %s=%q", name, value)
		}
	}
	if os.Getenv("DATAMITSU_GITENV_MARKER") != "kept" {
		t.Error("Unset() cleared an unrelated variable")
	}
}

// hookEnv renders every hook variable as an environment entry.
func hookEnv() []string {
	env := make([]string, 0, len(hookVars))
	for _, name := range hookVars {
		env = append(env, name+"=/somewhere")
	}

	return env
}
