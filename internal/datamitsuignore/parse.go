package datamitsuignore

import (
	"fmt"
	"strings"
)

// Rule represents a single .datamitsuignore rule.
type Rule struct {
	Glob   string
	Tools  []string
	Invert bool
}

// UnknownTools returns the tool names referenced by rules that are absent from
// known, skipping the "*" wildcard. Results preserve first-seen order and are
// de-duplicated so callers can warn once per unknown name. It is the single
// source of truth for "which tool names in a rule set are not configured".
func UnknownTools(rules []Rule, known map[string]bool) []string {
	var unknown []string
	seen := make(map[string]bool)
	for _, r := range rules {
		for _, t := range r.Tools {
			if t == "*" || known[t] || seen[t] {
				continue
			}
			seen[t] = true
			unknown = append(unknown, t)
		}
	}
	return unknown
}

// Parse parses .datamitsuignore content into a list of rules.
// Blank lines and lines starting with # are skipped.
// Format: "glob: tool1, tool2" or "!glob: tool1, tool2" for inversion.
func Parse(content string) ([]Rule, error) {
	return ParseRules(strings.Split(content, "\n"))
}

// ParseRules parses a slice of rule lines into rules.
// Each line follows the same format as .datamitsuignore:
// "glob: tool1, tool2" or "!glob: tool1, tool2" for inversion.
// Blank lines and lines starting with # are skipped.
func ParseRules(lines []string) ([]Rule, error) {
	var rules []Rule

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		before, after, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("line %d: missing colon separator: %q", i+1, line)
		}

		globPart := strings.TrimSpace(before)
		toolsPart := strings.TrimSpace(after)

		if globPart == "" || globPart == "!" {
			return nil, fmt.Errorf("line %d: empty glob pattern", i+1)
		}
		if toolsPart == "" {
			return nil, fmt.Errorf("line %d: empty tool list", i+1)
		}

		invert := false
		if strings.HasPrefix(globPart, "!") {
			invert = true
			globPart = strings.TrimSpace(globPart[1:])
			if globPart == "" {
				return nil, fmt.Errorf("line %d: empty glob pattern after negation", i+1)
			}
		}

		var tools []string
		for t := range strings.SplitSeq(toolsPart, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tools = append(tools, t)
			}
		}
		if len(tools) == 0 {
			return nil, fmt.Errorf("line %d: empty tool list", i+1)
		}

		rules = append(rules, Rule{
			Glob:   globPart,
			Tools:  tools,
			Invert: invert,
		})
	}

	return rules, nil
}
