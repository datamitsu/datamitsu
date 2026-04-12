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
	if len(rules) == 0 {
		return nil
	}
	m.entries = append(m.entries, dirRules{dir: toSlash(relDir), rules: rules})
	return nil
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

	disabled := make(map[string]bool)

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
				if rule.Invert {
					if tool == "*" {
						for k := range disabled {
							delete(disabled, k)
						}
					} else {
						delete(disabled, tool)
					}
				} else {
					disabled[tool] = true
				}
			}
		}
	}

	return disabled[toolName] || disabled["*"]
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
