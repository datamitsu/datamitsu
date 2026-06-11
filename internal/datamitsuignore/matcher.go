// Package datamitsuignore parses .datamitsuignore files and matches their
// per-directory rules against file paths to decide which tools are disabled.
package datamitsuignore

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

func toSlash(p string) string {
	return filepath.ToSlash(p)
}

type dirRules struct {
	dir   string // relative to root, "" for root
	rules []Rule
}

// Matcher collects .datamitsuignore rules per directory and determines
// whether a tool is disabled for a given file path.
type Matcher struct {
	entries []dirRules
}

// NewMatcher creates an empty Matcher.
func NewMatcher() *Matcher {
	return &Matcher{}
}

// AddFile parses .datamitsuignore content and associates the resulting rules
// with the given directory (relative to root, use "" for root).
func (m *Matcher) AddFile(relDir string, content string) error {
	rules, err := Parse(content)
	if err != nil {
		return err
	}
	m.AddRules(relDir, rules)
	return nil
}

// AddRules associates already-parsed rules with the given directory (relative to
// root, use "" for root). Empty rule sets are ignored. Use this when the caller
// has already parsed the content (e.g. to validate tool names first) to avoid a
// redundant re-parse.
func (m *Matcher) AddRules(relDir string, rules []Rule) {
	if len(rules) == 0 {
		return
	}
	m.entries = append(m.entries, dirRules{dir: toSlash(relDir), rules: rules})
}

// IsDisabled reports whether toolName should be skipped for relFilePath.
// relFilePath must be relative to the repository root.
// Rules are applied from root directory to the file's directory; positive rules
// add to the disabled set, inversion rules remove from it.
func (m *Matcher) IsDisabled(toolName string, relFilePath string) bool {
	if len(m.entries) == 0 {
		return false
	}

	relFilePath = toSlash(relFilePath)
	fileDir := toSlash(filepath.Dir(relFilePath))

	// Collect applicable entries: those whose directory is an ancestor of (or
	// equal to) the file's directory.
	var applicable []dirRules
	for _, e := range m.entries {
		if isAncestorOrEqual(e.dir, fileDir) {
			applicable = append(applicable, e)
		}
	}

	if len(applicable) == 0 {
		return false
	}

	// Sort by directory depth (root first, deeper dirs later).
	// Stable sort preserves insertion order for same-depth entries,
	// ensuring deterministic precedence between config-defined rules
	// and file-based rules at the same directory level.
	sort.SliceStable(applicable, func(i, j int) bool {
		return depth(applicable[i].dir) < depth(applicable[j].dir)
	})

	// wildcard tracks whether a "*" rule currently disables every tool.
	// overrides holds explicit per-tool decisions (true=disabled, false=enabled)
	// that take precedence over wildcard, so a specific re-enable like
	// "!**/*.md: eslint" wins even after a blanket "**/*: *" disable.
	wildcard := false
	overrides := make(map[string]bool)

	for _, e := range applicable {
		for _, rule := range e.rules {
			glob := rule.Glob
			// If the rule's glob is relative, scope it to the rule's directory.
			if e.dir != "" && !strings.HasPrefix(glob, "**/") {
				glob = e.dir + "/" + glob
			}

			matched, err := doublestar.Match(glob, relFilePath)
			if err != nil || !matched {
				continue
			}

			for _, tool := range rule.Tools {
				switch {
				case tool == "*":
					// A blanket (re-)enable/disable resets prior per-tool
					// exceptions: the later, broader rule wins.
					wildcard = !rule.Invert
					clear(overrides)
				case rule.Invert:
					overrides[tool] = false
				default:
					overrides[tool] = true
				}
			}
		}
	}

	if v, ok := overrides[toolName]; ok {
		return v
	}
	return wildcard
}

// IsProjectDisabled reports whether toolName should be skipped for an entire
// project rooted at relProjectDir (relative to the repository root).
// It uses a synthetic file path inside the project directory to test whether
// catch-all glob rules (e.g. "**/*") would disable the tool.
// Extension-specific rules (e.g. "**/*.md") will not trigger a project-level disable.
func (m *Matcher) IsProjectDisabled(toolName string, relProjectDir string) bool {
	relProjectDir = toSlash(relProjectDir)
	synthetic := relProjectDir + "/x"
	if relProjectDir == "" || relProjectDir == "." {
		synthetic = "x"
	}
	return m.IsDisabled(toolName, synthetic)
}

func isAncestorOrEqual(dir, target string) bool {
	if dir == "" || dir == "." {
		return true
	}
	if dir == target {
		return true
	}
	prefix := dir + "/"
	return strings.HasPrefix(target, prefix)
}

func depth(dir string) int {
	if dir == "" || dir == "." {
		return 0
	}
	return strings.Count(dir, "/") + 1
}
