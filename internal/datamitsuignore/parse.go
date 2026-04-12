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

		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			return nil, fmt.Errorf("line %d: missing colon separator: %q", i+1, line)
		}

		globPart := strings.TrimSpace(line[:colonIdx])
		toolsPart := strings.TrimSpace(line[colonIdx+1:])

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
		for _, t := range strings.Split(toolsPart, ",") {
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
