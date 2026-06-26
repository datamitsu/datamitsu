package tooling

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/textdiff"
)

const (
	// formatTempSubdir is the system-temp child that holds the per-format scratch
	// directories. It lives OUTSIDE the repository so an in-place formatter never
	// touches the working tree (no git noise, no watcher churn).
	formatTempSubdir = "datamitsu-lsp-fmt"
	// formatTempTTL bounds how long a scratch dir may linger before the next
	// server start sweeps it. Formats finish in seconds, so an hour is generous
	// and safely past any realistic in-flight run.
	formatTempTTL = time.Hour
)

func formatTempBase() string { return filepath.Join(os.TempDir(), formatTempSubdir) }

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

	cmdInfo, err := e.appManager.GetCommandInfo(ctx, task.OpConfig.App)
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

// FormatFileInPlace runs a single in-place (non-stdout) fix formatter against
// in-memory content by materializing it into a private temp file OUTSIDE the
// repository, running the tool on that copy, and reading the result back. The
// real file on disk is never read or written, so an unsaved editor buffer formats
// correctly and a format request never writes the user's file as a side effect.
//
// The temp file keeps absPath's FULL base name (e.g. index.d.ts -> index.d.ts) so
// extension- and filename-sensitive tools dispatch exactly as on the real file;
// its unique parent dir isolates concurrent formats and is GC'd by
// CleanStaleFormatTempDirs. The tool runs with the task's real working directory,
// so {root}/{cwd} placeholders and cwd-relative config discovery still resolve to
// the repo — only the {file}/{files} placeholder is redirected to the temp copy.
// The caller must ensure the task references {file}/{files} (so it CAN be
// redirected off the real tree); FormatFile enforces this.
//
// A non-zero exit is not treated as fatal: a fixer can exit non-zero while still
// applying partial fixes, so whatever the tool wrote to the temp file is read
// back. Only an emptied non-empty file (truncation) or an unreadable result is an
// error.
func (e *Executor) FormatFileInPlace(ctx context.Context, task Task, absPath string, content []byte) ([]byte, error) {
	cmdInfo, err := e.appManager.GetCommandInfo(ctx, task.OpConfig.App)
	if err != nil {
		return nil, fmt.Errorf("get command info for %s: %w", task.OpConfig.App, err)
	}

	base := formatTempBase()
	if mkErr := os.MkdirAll(base, 0o700); mkErr != nil {
		return nil, fmt.Errorf("create format temp base: %w", mkErr)
	}
	tmpDir, err := os.MkdirTemp(base, "fmt-")
	if err != nil {
		return nil, fmt.Errorf("create format temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tmpFile := filepath.Join(tmpDir, filepath.Base(absPath))
	if writeErr := os.WriteFile(tmpFile, content, 0o600); writeErr != nil {
		return nil, fmt.Errorf("write format temp file: %w", writeErr)
	}

	// Redirect only the file placeholder to the temp copy; the working directory
	// and {root}/{cwd} stay at the real project so config discovery is unaffected.
	workingDir := e.getWorkingDir(task)
	args := e.replacePlaceholders(task.OpConfig.Args, tmpFile, []string{tmpFile}, task.ProjectPath, task.ToolName)
	opEnv := e.replaceEnvPlaceholders(task.OpConfig.Env, task.ProjectPath, task.ToolName)
	cmd := e.buildCommand(ctx, cmdInfo, args, workingDir, opEnv)

	// In-place tools write the temp file; their stdout/stderr are diagnostics we
	// discard. A non-zero exit is intentionally ignored (see doc comment) — the
	// result is whatever the tool wrote, read back below.
	_, _, _ = e.runCommandIO(cmd, nil, false) //nolint:dogsled // all three returns are deliberately discarded; correctness comes from reading the temp file back

	formatted, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, fmt.Errorf("read formatted temp file for %s: %w", task.ToolName, err)
	}
	if len(formatted) == 0 && len(content) > 0 {
		return nil, fmt.Errorf("formatter %s emptied %s", task.ToolName, filepath.Base(absPath))
	}
	return formatted, nil
}

// CleanStaleFormatTempDirs removes leftover in-place-format scratch directories
// from crashed sessions: every child of the format temp base whose mtime is older
// than formatTempTTL. It is best-effort and intended to run once at server start;
// errors are joined for logging, never fatal. A missing base is not an error.
func CleanStaleFormatTempDirs() error {
	base := formatTempBase()
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read format temp base: %w", err)
	}

	cutoff := time.Now().Add(-formatTempTTL)
	var errs []error
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			errs = append(errs, infoErr)
			continue
		}
		if info.ModTime().Before(cutoff) {
			if rmErr := os.RemoveAll(filepath.Join(base, entry.Name())); rmErr != nil {
				errs = append(errs, rmErr)
			}
		}
	}
	return errors.Join(errs...)
}
