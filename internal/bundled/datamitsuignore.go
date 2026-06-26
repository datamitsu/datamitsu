// Package bundled implements discovery, linting and normalization of
// .datamitsuignore files across a project tree.
package bundled

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/datamitsuignore"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/utils"
)

// FindIgnoreFiles returns the paths of every .datamitsuignore file that the
// planner would actually apply. It uses the same gitignore-aware traversal
// (traverser.FindFilesFromPath) as internal/tooling's matcher, so the set of
// files linted/fixed here is identical to the set that takes effect at runtime —
// no "green lint on a file that never applies" divergence.
func FindIgnoreFiles(ctx context.Context, rootPath string) ([]string, error) {
	all, err := traverser.FindFilesFromPath(ctx, rootPath, rootPath)
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", rootPath, err)
	}
	var files []string
	for _, f := range all {
		if filepath.Base(f) == ".datamitsuignore" {
			files = append(files, f)
		}
	}
	return files, nil
}

// normalizeRule formats a parsed rule into canonical form: "{!}{glob}: {tool1}, {tool2}"
func normalizeRule(r datamitsuignore.Rule) string {
	prefix := ""
	if r.Invert {
		prefix = "!"
	}
	return prefix + strings.TrimSpace(r.Glob) + ": " + strings.Join(r.Tools, ", ")
}

// normalizeLine normalizes a single line of .datamitsuignore content.
// Whitespace-only lines are normalized to empty. Comments are preserved.
// Rule lines are normalized to canonical form.
func normalizeLine(line string) (string, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "#") {
		return line, nil
	}

	rules, err := datamitsuignore.ParseRules([]string{line})
	if err != nil {
		return "", err
	}
	if len(rules) == 0 {
		return line, nil
	}
	return normalizeRule(rules[0]), nil
}

// normalizeFileStructure removes leading empty lines and collapses consecutive
// empty lines. The trailing "" element (representing a final newline) is preserved.
func normalizeFileStructure(lines []string) []string {
	hasTrailingNewline := len(lines) > 0 && lines[len(lines)-1] == ""
	work := lines
	if hasTrailingNewline {
		work = lines[:len(lines)-1]
	}

	for len(work) > 0 && work[0] == "" {
		work = work[1:]
	}

	result := make([]string, 0, len(work))
	prevEmpty := false
	for _, l := range work {
		isEmpty := l == ""
		if isEmpty && prevEmpty {
			continue
		}
		result = append(result, l)
		prevEmpty = isEmpty
	}

	if hasTrailingNewline {
		result = append(result, "")
	}
	return result
}

func toRelPath(rootPath, absPath string) string {
	rel, err := filepath.Rel(rootPath, absPath)
	if err != nil {
		return absPath
	}
	return rel
}

// RunFix normalizes formatting of all .datamitsuignore files under rootPath.
// Parse errors cause an immediate error return.
// Files are written atomically (temp file + rename) only if content changed.
func RunFix(ctx context.Context, rootPath string) error {
	files, err := FindIgnoreFiles(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("finding .datamitsuignore files: %w", err)
	}

	for _, path := range files {
		if err := fixFile(path, rootPath); err != nil {
			return err
		}
	}
	return nil
}

func fixFile(path, rootPath string) error {
	relPath := toRelPath(rootPath, path)
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", relPath, err)
	}

	original := string(content)
	lines := strings.Split(original, "\n")
	normalized := make([]string, len(lines))

	for i, line := range lines {
		n, err := normalizeLine(line)
		if err != nil {
			errMsg := err.Error()
			if after, ok := strings.CutPrefix(errMsg, "line 1: "); ok {
				return fmt.Errorf("datamitsuignore: %s:%d: %s", relPath, i+1, after)
			}
			return fmt.Errorf("datamitsuignore: %s:%d: %w", relPath, i+1, err)
		}
		normalized[i] = n
	}

	normalized = normalizeFileStructure(normalized)
	result := strings.Join(normalized, "\n")
	if result == original {
		return nil
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".datamitsuignore.tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(result); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing temp file %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing temp file %s: %w", tmpPath, err)
	}

	info, err := os.Stat(path)
	if err == nil {
		_ = os.Chmod(tmpPath, info.Mode())
	}

	if err := utils.RenameReplace(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file to %s: %w", path, err)
	}

	return nil
}

// RunLint validates all .datamitsuignore files under rootPath.
// Parse errors cause an immediate error return.
// Formatting issues and unknown tool names produce yellow warnings on stderr.
func RunLint(ctx context.Context, rootPath string, tools config.MapOfTools) error {
	files, err := FindIgnoreFiles(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("finding .datamitsuignore files: %w", err)
	}

	for _, path := range files {
		if err := lintFile(path, tools, rootPath); err != nil {
			return err
		}
	}
	return nil
}

func lintFile(path string, tools config.MapOfTools, rootPath string) error {
	relPath := toRelPath(rootPath, path)
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", relPath, err)
	}

	allLines := strings.Split(string(content), "\n")
	// Exclude the trailing "" artifact from a final newline.
	lines := allLines
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// warn emits a human lint warning to stderr, suppressed in JSON-L (quiet)
	// mode so it never injects a non-JSON line into the typed event stream.
	warn := func(format string, a ...any) {
		if ui.Quiet() {
			return
		}
		fmt.Fprintf(os.Stderr, format, a...)
	}

	prevEmpty := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isEmpty := trimmed == ""

		if isEmpty && line != "" {
			warn("%s\n", color.Yellow(fmt.Sprintf("warning: datamitsuignore: %s:%d: whitespace-only line", relPath, i+1)))
		}
		if isEmpty && i == 0 {
			warn("%s\n", color.Yellow(fmt.Sprintf("warning: datamitsuignore: %s:%d: leading empty line", relPath, i+1)))
		}
		if isEmpty && prevEmpty {
			warn("%s\n", color.Yellow(fmt.Sprintf("warning: datamitsuignore: %s:%d: consecutive empty line", relPath, i+1)))
		}
		prevEmpty = isEmpty

		if isEmpty || strings.HasPrefix(trimmed, "#") {
			continue
		}

		rules, err := datamitsuignore.ParseRules([]string{line})
		if err != nil {
			errMsg := err.Error()
			if after, ok := strings.CutPrefix(errMsg, "line 1: "); ok {
				return fmt.Errorf("datamitsuignore: %s:%d: %s", relPath, i+1, after)
			}
			return fmt.Errorf("datamitsuignore: %s:%d: %w", relPath, i+1, err)
		}

		if len(rules) == 0 {
			continue
		}

		rule := rules[0]
		canonical := normalizeRule(rule)
		if line != canonical {
			warn("%s\n", color.Yellow(fmt.Sprintf("warning: datamitsuignore: %s:%d: formatting differs from canonical form", relPath, i+1)))
			warn("  have: %s\n", line)
			warn("  want: %s\n", canonical)
		}

		for _, toolName := range rule.Tools {
			if toolName == "*" {
				continue
			}
			if _, ok := tools[toolName]; !ok {
				warn("%s\n", color.Yellow(fmt.Sprintf("warning: datamitsuignore: %s:%d: unknown tool %q", relPath, i+1, toolName)))
			}
		}
	}

	return nil
}
