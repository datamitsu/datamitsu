// Package datamitsuignore parses .datamitsuignore files and matches their
// per-directory rules against file paths to decide which tools are disabled.
package datamitsuignore

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/globmatch"
)

func toSlash(p string) string {
	return filepath.ToSlash(p)
}

type dirRules struct {
	dir   string // relative to root, "" for root
	rules []preparedRule
}

// preparedRule is a Rule with everything that does not depend on the path being
// tested resolved once: the glob scoped to the rule's directory, and that glob
// compiled.
//
// IsDisabled runs once per (file, tool) — tens of thousands of times when
// planning a large repository — and used to rebuild the scoped pattern string
// and re-parse it on every one of those calls.
type preparedRule struct {
	rule Rule
	set  globmatch.Set
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
	dir := toSlash(relDir)
	prepared := make([]preparedRule, 0, len(rules))
	for _, rule := range rules {
		glob := rule.Glob
		// A relative glob is scoped to the rule's directory. Resolved here, not
		// per query.
		if dir != "" && !strings.HasPrefix(glob, "**/") {
			glob = dir + "/" + glob
		}
		prepared = append(prepared, preparedRule{rule: rule, set: globmatch.New([]string{glob})})
	}
	m.entries = append(m.entries, dirRules{dir: dir, rules: prepared})

	// Kept sorted here rather than on every query. Rules are applied root-first
	// so a deeper directory can override a shallower one, and IsDisabled runs
	// once per (file, tool) — tens of thousands of times on a large repository —
	// so the ordering must not cost a slice allocation and a sort each time.
	// A stable sort of an already-ordered slice with one appended entry puts that
	// entry at the end of its depth group, which is the same order sorting once
	// at the end would produce.
	sort.SliceStable(m.entries, func(i, j int) bool {
		return depth(m.entries[i].dir) < depth(m.entries[j].dir)
	})
}

// DisabledEverywhere reports whether toolName is disabled for every path in the
// tree and no rule anywhere can re-enable it.
//
// It exists so a caller can drop a tool before doing any per-file work. The
// answer is deliberately conservative — it proves the "disabled" case and says
// false whenever it cannot — because the only sound use of a true is to skip
// work that would have produced nothing anyway.
//
// A proof needs both halves: some root-scoped rule that disables the tool (or
// every tool) across the whole tree, and no inversion anywhere naming the tool
// or "*". Only a `**/*` glob at the root covers the tree; a narrower glob such
// as `**/*.md` leaves paths it does not match enabled.
func (m *Matcher) DisabledEverywhere(toolName string) bool {
	disabled := false
	for _, e := range m.entries {
		for _, pr := range e.rules {
			rule := pr.rule
			for _, tool := range rule.Tools {
				if rule.Invert && (tool == toolName || tool == "*") {
					return false
				}
			}
			if rule.Invert || e.dir != "" || rule.Glob != "**/*" {
				continue
			}
			for _, tool := range rule.Tools {
				if tool == toolName || tool == "*" {
					disabled = true
				}
			}
		}
	}
	return disabled
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

	// wildcard tracks whether a "*" rule currently disables every tool.
	// override holds the explicit decision for toolName (true=disabled,
	// false=enabled), which takes precedence over wildcard — so a specific
	// re-enable like "!**/*.md: eslint" wins even after a blanket "**/*: *".
	//
	// Only toolName's own decision is tracked, not a map of every tool's: the
	// question is about one tool, and the blanket-rule reset that used to
	// clear(overrides) is exactly "forget what we knew about toolName". This
	// runs once per (file, tool), so the map and the per-call applicable slice
	// it replaced were the allocation cost of planning on a large repository.
	wildcard := false
	override, hasOverride := false, false

	// m.entries is kept sorted by directory depth, root first, so rules apply in
	// precedence order without a per-call sort.
	for _, e := range m.entries {
		if !isAncestorOrEqual(e.dir, fileDir) {
			continue
		}
		for _, pr := range e.rules {
			rule := pr.rule
			if !ruleMentions(rule, toolName) {
				continue
			}
			if !pr.set.Match(relFilePath) {
				continue
			}

			for _, tool := range rule.Tools {
				switch tool {
				case "*":
					// A blanket (re-)enable/disable resets prior per-tool
					// exceptions: the later, broader rule wins.
					wildcard = !rule.Invert
					hasOverride = false
				case toolName:
					override, hasOverride = !rule.Invert, true
				}
			}
		}
	}

	if hasOverride {
		return override
	}
	return wildcard
}

// ruleMentions reports whether a rule can affect toolName at all, so a rule
// naming only other tools is rejected before its glob is matched. A rule listing
// 20 tools for a file we are asking about one of them is the common shape, and
// the glob match is the expensive part.
func ruleMentions(rule Rule, toolName string) bool {
	for _, tool := range rule.Tools {
		if tool == "*" || tool == toolName {
			return true
		}
	}
	return false
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
