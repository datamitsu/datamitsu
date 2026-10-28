// Package datamitsuignore parses the .datamitsuignore file at the git root and
// matches its rules against file paths to decide which tools are disabled.
//
// The file lives only at the repository git root. The former per-directory
// cascading form was removed: a rule that used to live in a nested
// .datamitsuignore migrates to the root file with a directory prefix
// ("sub/.datamitsuignore: *.go" becomes "sub/*.go: tool").
package datamitsuignore

import (
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/globmatch"
)

func toSlash(p string) string {
	return filepath.ToSlash(p)
}

// preparedRule is a Rule with its glob compiled once.
//
// IsDisabled runs once per (file, tool) — tens of thousands of times when
// planning a large repository — and used to rebuild the pattern string and
// re-parse it on every one of those calls.
type preparedRule struct {
	rule Rule
	set  globmatch.Set
}

// Matcher collects .datamitsuignore rules and determines whether a tool is
// disabled for a given file path. Every rule applies from the repository root.
type Matcher struct {
	rules []preparedRule
}

// NewMatcher creates an empty Matcher.
func NewMatcher() *Matcher {
	return &Matcher{}
}

// AddFile parses .datamitsuignore content and appends the resulting rules.
func (m *Matcher) AddFile(content string) error {
	rules, err := Parse(content)
	if err != nil {
		return err
	}
	m.AddRules(rules)
	return nil
}

// AddRules appends already-parsed rules. Empty rule sets are ignored. Use this
// when the caller has already parsed the content (e.g. to validate tool names
// first) to avoid a redundant re-parse.
func (m *Matcher) AddRules(rules []Rule) {
	if len(rules) == 0 {
		return
	}
	for _, rule := range rules {
		m.rules = append(m.rules, preparedRule{rule: rule, set: globmatch.New([]string{rule.Glob})})
	}
}

// DisabledEverywhere reports whether toolName is disabled for every path in
// the tree and no rule anywhere can re-enable it.
//
// It exists so a caller can drop a tool before doing any per-file work. The
// answer is deliberately conservative — it proves the "disabled" case and says
// false whenever it cannot — because the only sound use of a true is to skip
// work that would have produced nothing anyway.
//
// A proof needs both halves: some rule that disables the tool (or every tool)
// across the whole tree, and no inversion anywhere naming the tool or "*".
// Only a `**/*` glob covers the tree; a narrower glob such as `**/*.md` leaves
// paths it does not match enabled.
func (m *Matcher) DisabledEverywhere(toolName string) bool {
	disabled := false
	for _, pr := range m.rules {
		rule := pr.rule
		for _, tool := range rule.Tools {
			if rule.Invert && (tool == toolName || tool == "*") {
				return false
			}
		}
		if rule.Invert || rule.Glob != "**/*" {
			continue
		}
		for _, tool := range rule.Tools {
			if tool == toolName || tool == "*" {
				disabled = true
			}
		}
	}
	return disabled
}

// IsDisabled reports whether toolName should be skipped for relFilePath.
// relFilePath must be relative to the repository root.
// Positive rules add to the disabled set, inversion rules remove from it; a
// later rule wins over an earlier one.
func (m *Matcher) IsDisabled(toolName string, relFilePath string) bool {
	if len(m.rules) == 0 {
		return false
	}

	relFilePath = toSlash(relFilePath)

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

	for _, pr := range m.rules {
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
