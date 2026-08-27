package tooling

import (
	"context"
	"fmt"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/textdiff"
)

// FormatContent runs a single stdin->stdout formatter task against in-memory
// content and returns the candidate formatted bytes plus the minimal line-based
// edits that turn content into candidate.
//
// It is the in-memory twin of executePerFile's format path (executor.go), built
// for the LSP textDocument/formatting handler: unlike the disk path it NEVER
// reads the file, NEVER writes it (no applyStdoutFormat/writeFileAtomic), and
// NEVER parses. The task must be a fix operation with Output==stdout; absPath
// drives {file} placeholder substitution and the working directory, but the
// bytes fed to the tool's stdin are `content`, not the file's on-disk contents
// (so an unsaved editor buffer formats correctly).
//
// The returned candidate is the tool's raw stdout, suitable for chaining into the
// next formatter's input. edits is content->candidate (nil when unchanged); a
// caller composing several formatters should diff original->final itself rather
// than concatenate per-step edits.
func (e *Executor) FormatContent(ctx context.Context, task Task, absPath string, content []byte) (candidate []byte, edits []textdiff.Edit, err error) {
	if task.OpConfig.Output != config.ToolOutputStdout {
		return nil, nil, fmt.Errorf("tool %s: FormatContent requires output:stdout", task.ToolName)
	}

	cmdInfo, err := e.commandInfo(ctx, task.OpConfig.App)
	if err != nil {
		return nil, nil, fmt.Errorf("get command info for %s: %w", task.OpConfig.App, err)
	}

	workingDir := e.getWorkingDir(task)
	args := e.replacePlaceholders(task.OpConfig.Args, absPath, []string{absPath}, task.ProjectPath, task.ToolName)
	opEnv := e.replaceEnvPlaceholders(task.OpConfig.Env, task.ProjectPath, task.ToolName)
	cmd := e.buildCommand(ctx, cmdInfo, args, workingDir, opEnv)

	// separate=true keeps the candidate (stdout) apart from diagnostics (stderr),
	// exactly as the format path does; stderr is discarded here.
	stdoutBytes, _, runErr := e.runCommandIO(cmd, content, true)
	if runErr != nil {
		return nil, nil, fmt.Errorf("run %s (exit %d): %w", task.ToolName, getExitCode(runErr), runErr)
	}

	// Anti-truncation guard (mirrors executePerFile): a stdout-mode formatter that
	// exits 0 but emits nothing for non-empty input is misbehaving; refuse it
	// rather than report an edit that wipes the buffer to zero bytes.
	if len(stdoutBytes) == 0 && len(content) > 0 {
		return nil, nil, fmt.Errorf("formatter %s produced empty stdout for non-empty content", task.ToolName)
	}

	return stdoutBytes, textdiff.ComputeEdits(string(content), string(stdoutBytes)), nil
}
