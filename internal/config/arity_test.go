package config

import (
	"strings"
	"testing"
)

func TestInferArity(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want ToolArity
	}{
		{"standalone files", []string{"--write", "{files}"}, ArityMany},
		{"standalone file", []string{"-w", "{file}"}, ArityOne},
		{"standalone target", []string{"dir", "{target}"}, ArityDir},
		{"embedded target", []string{"dir:{target}"}, ArityDir},
		{"embedded files", []string{"--paths={files}"}, ArityMany},
		{"no path token", []string{"run", "--fix"}, ArityNone},
		{"empty args", nil, ArityNone},

		// {root}/{cwd} are ambiguous by design — they name a config path as often
		// as a scan target, which is why {target} exists. They never infer a shape.
		{"root is a config path, not a target", []string{"-c", "{root}/.gitleaks.toml"}, ArityNone},
		{"cwd is a config path, not a target", []string{"-c", "{cwd}/eslint.config.mjs"}, ArityNone},
		{"root config plus file list", []string{"-c", "{root}/x.toml", "{files}"}, ArityMany},

		// {file} is not a prefix match on {files}.
		{"files does not read as file", []string{"{files}"}, ArityMany},

		// target wins: the validator rejects this combination, but inference must
		// still be total rather than depending on argument order.
		{"target beats files", []string{"{files}", "{target}"}, ArityDir},
		{"target beats files reversed", []string{"{target}", "{files}"}, ArityDir},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InferArity(tt.args); got != tt.want {
				t.Errorf("InferArity(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestArgsReferenceFilesExcludesTarget(t *testing.T) {
	// A {target} directory is not the file list: counting it would make the
	// executor chunk a command that takes no file list.
	if ArgsReferenceFiles([]string{"dir", "{target}"}) {
		t.Error("ArgsReferenceFiles({target}) = true, want false")
	}
	if !ArgsReferenceFiles([]string{"{files}"}) || !ArgsReferenceFiles([]string{"{file}"}) {
		t.Error("ArgsReferenceFiles should be true for {file} and {files}")
	}
}

func TestRunsPerFile(t *testing.T) {
	tests := []struct {
		name  string
		op    ToolOperation
		files int
		want  bool
	}{
		{"per-file with {file}", ToolOperation{Scope: ToolScopePerFile, Args: []string{"{file}"}}, 1, true},
		// sort-package-json: per-file, reads the file from cwd, no path in argv.
		{"per-file with no path", ToolOperation{Scope: ToolScopePerFile, Args: []string{"--quiet"}}, 1, true},
		// dotenv-linter: per-file scope but an explicit list shape.
		{"per-file with {files}", ToolOperation{Scope: ToolScopePerFile, Args: []string{"{files}"}}, 1, false},
		{"repository with {files}", ToolOperation{Scope: ToolScopeRepository, Args: []string{"{files}"}}, 9, false},
		{"repository with no path", ToolOperation{Scope: ToolScopeRepository, Args: []string{"run"}}, 9, false},
		{"repository with {target}", ToolOperation{Scope: ToolScopeRepository, Args: []string{"{target}"}}, 9, false},
		{"no files never runs per file", ToolOperation{Scope: ToolScopePerFile, Args: []string{"{file}"}}, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RunsPerFile(tt.op, tt.files); got != tt.want {
				t.Errorf("RunsPerFile = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateToolsArity(t *testing.T) {
	validate := func(op ToolOperation) error {
		return ValidateTools(MapOfTools{"t": {Operations: map[OperationType]ToolOperation{OpFix: op}}}, nil)
	}

	t.Run("declared arity matching the inference passes", func(t *testing.T) {
		if err := validate(ToolOperation{App: "a", Scope: ToolScopeRepository, Arity: ArityDir, Args: []string{"dir", "{target}"}}); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("declared arity disagreeing with the inference fails", func(t *testing.T) {
		err := validate(ToolOperation{App: "a", Scope: ToolScopeRepository, Arity: ArityDir, Args: []string{"{files}"}})
		if err == nil || !strings.Contains(err.Error(), "cannot override it") {
			t.Errorf("expected an assertion failure, got: %v", err)
		}
	})

	t.Run("target and a file list together fail", func(t *testing.T) {
		err := validate(ToolOperation{App: "a", Scope: ToolScopeRepository, Args: []string{"{target}", "{files}"}})
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("expected a mutual-exclusion error, got: %v", err)
		}
	})

	t.Run("omitted arity never fails", func(t *testing.T) {
		for _, args := range [][]string{{"{files}"}, {"{file}"}, {"{target}"}, {"run"}} {
			if err := validate(ToolOperation{App: "a", Scope: ToolScopeRepository, Args: args}); err != nil {
				t.Errorf("args %q: expected no error, got: %v", args, err)
			}
		}
	})
}
