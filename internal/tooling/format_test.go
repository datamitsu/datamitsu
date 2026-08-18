package tooling

import (
	"context"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/textdiff"
)

// formatTask builds a per-file stdin->stdout fix task for app appName.
func formatTask(appName, projectPath string) Task {
	return Task{
		ToolName:  appName,
		Operation: config.OpFix,
		OpConfig: config.ToolOperation{
			App:    appName,
			Scope:  config.ToolScopePerFile,
			Input:  config.ToolInputStdin,
			Output: config.ToolOutputStdout,
		},
		ProjectPath: projectPath,
	}
}

func TestFormatContent_NoChangeYieldsNilEdits(t *testing.T) {
	dir := t.TempDir()
	appMgr := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		// `cat` echoes stdin unchanged → candidate == content → no edits.
		"noop": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", "cat"}},
	}}
	e := NewExecutor(dir, false, false, appMgr, nil)

	content := []byte("a\nb\nc\n")
	candidate, edits, err := e.FormatContent(context.Background(), formatTask("noop", dir), dir+"/f.txt", content)
	if err != nil {
		t.Fatalf("FormatContent: %v", err)
	}
	if string(candidate) != string(content) {
		t.Errorf("candidate = %q, want unchanged %q", candidate, content)
	}
	if edits != nil {
		t.Errorf("expected nil edits for unchanged content, got %v", edits)
	}
}

func TestFormatContent_TransformProducesApplicableEdits(t *testing.T) {
	dir := t.TempDir()
	appMgr := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		// upper-case stdin; diagnostics to stderr must NOT leak into the candidate.
		"upper": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", "tr a-z A-Z; echo note >&2"}},
	}}
	e := NewExecutor(dir, false, false, appMgr, nil)

	content := []byte("hello\nworld\n")
	candidate, edits, err := e.FormatContent(context.Background(), formatTask("upper", dir), dir+"/f.txt", content)
	if err != nil {
		t.Fatalf("FormatContent: %v", err)
	}
	if string(candidate) != "HELLO\nWORLD\n" {
		t.Errorf("candidate = %q, want %q", candidate, "HELLO\nWORLD\n")
	}
	if len(edits) == 0 {
		t.Fatal("expected non-empty edits for a transform")
	}
	// The edits applied to the original must reproduce the candidate exactly.
	if got := textdiff.Apply(string(content), edits); got != string(candidate) {
		t.Errorf("Apply(content, edits) = %q, want candidate %q", got, candidate)
	}
}

func TestFormatContent_RejectsNonStdoutOp(t *testing.T) {
	dir := t.TempDir()
	e := NewExecutor(dir, false, false, &mockAppManager{}, nil)

	task := formatTask("x", dir)
	task.OpConfig.Output = "" // not stdout
	if _, _, err := e.FormatContent(context.Background(), task, dir+"/f.txt", []byte("x")); err == nil {
		t.Error("expected error for non-stdout op, got nil")
	}
}

func TestFormatContent_EmptyStdoutForNonEmptyContentIsError(t *testing.T) {
	dir := t.TempDir()
	appMgr := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		// exits 0 but writes nothing → would truncate the buffer; must be rejected.
		"sink": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", "true"}},
	}}
	e := NewExecutor(dir, false, false, appMgr, nil)

	if _, _, err := e.FormatContent(context.Background(), formatTask("sink", dir), dir+"/f.txt", []byte("data\n")); err == nil {
		t.Error("expected error when formatter produces empty stdout for non-empty content")
	}
}
