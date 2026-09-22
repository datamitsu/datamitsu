// Package inspectortheme merges a user's color overrides onto the inspector's
// built-in theme and turns the result into the CSS custom properties the
// inspector reads.
package inspectortheme

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Groups lists every token the inspector styles read, by group, in the order a
// generated file presents them. It is the whole surface a theme file may set.
var Groups = map[string][]string{
	"accent":  {"base", "display", "glow"},
	"focus":   {"ring"},
	"line":    {"base", "orbit"},
	"runtime": {"binary", "bun", "go", "jvm", "node", "python", "shell", "unknown"},
	"status":  {"enabled", "skipped"},
	"surface": {"base", "panel", "raised", "scrim", "shadow"},
	"text":    {"base", "muted"},
}

// Modes are the two color schemes a theme file may address.
var Modes = []string{"dark", "light"}

// Theme is a fully resolved pair of palettes: mode → group → token → color.
type Theme map[string]map[string]map[string]string

var colorPattern = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// Parse reads a theme file, rejecting anything the inspector could not apply.
// Every error names the full key path, so a typo fails loudly rather than
// silently doing nothing.
func Parse(data []byte) (Theme, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse theme: %w", err)
	}
	theme := Theme{}
	for _, mode := range sortedKeys(document) {
		if mode == "$schema" {
			continue
		}
		if !contains(Modes, mode) {
			return nil, fmt.Errorf("%s: unknown key (want %s)", mode, strings.Join(Modes, " or "))
		}
		var palette map[string]json.RawMessage
		if err := json.Unmarshal(document[mode], &palette); err != nil {
			return nil, fmt.Errorf("%s: expected an object of groups: %w", mode, err)
		}
		theme[mode] = map[string]map[string]string{}
		for _, group := range sortedKeys(palette) {
			tokens, known := Groups[group]
			if !known {
				return nil, fmt.Errorf("%s.%s: unknown key (want %s)", mode, group, strings.Join(groupNames(), ", "))
			}
			var values map[string]string
			if err := json.Unmarshal(palette[group], &values); err != nil {
				return nil, fmt.Errorf("%s.%s: expected an object of colors: %w", mode, group, err)
			}
			theme[mode][group] = map[string]string{}
			for _, token := range sortedValues(values) {
				if !contains(tokens, token) {
					return nil, fmt.Errorf("%s.%s.%s: unknown key (want %s)", mode, group, token, strings.Join(tokens, ", "))
				}
				if !colorPattern.MatchString(values[token]) {
					return nil, fmt.Errorf("%s.%s.%s: %q is not a color (want #rgb, #rrggbb or #rrggbbaa)", mode, group, token, values[token])
				}
				theme[mode][group][token] = values[token]
			}
		}
	}
	return theme, nil
}

// Merge overlays a theme file on the built-in theme, token by token. A mode
// override never reaches the other mode, and a group keeps the tokens it does
// not mention.
func Merge(base, override Theme) Theme {
	merged := Theme{}
	for mode, groups := range base {
		merged[mode] = map[string]map[string]string{}
		for group, tokens := range groups {
			merged[mode][group] = map[string]string{}
			maps.Copy(merged[mode][group], tokens)
		}
	}
	for mode, groups := range override {
		if merged[mode] == nil {
			merged[mode] = map[string]map[string]string{}
		}
		for group, tokens := range groups {
			if merged[mode][group] == nil {
				merged[mode][group] = map[string]string{}
			}
			maps.Copy(merged[mode][group], tokens)
		}
	}
	return merged
}

// CSS renders the theme as the custom properties the inspector styles read.
// Light is the document default; dark applies under the theme attribute the app
// sets, so the two never depend on the operating system here.
func CSS(theme Theme) string {
	var out strings.Builder
	out.WriteString(":root{")
	writeTokens(&out, theme["light"])
	out.WriteString("}\n:root[data-theme=\"dark\"]{")
	writeTokens(&out, theme["dark"])
	out.WriteString("}\n")
	return out.String()
}

// JSON renders the theme in the format a theme file uses, so the default is a
// starting point someone can edit.
func JSON(theme Theme) ([]byte, error) {
	data, err := json.MarshalIndent(theme, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode theme: %w", err)
	}
	return append(data, '\n'), nil
}

// Schema describes a theme file for editors. It is generated from the same token
// table the parser enforces, so the two cannot drift.
func Schema(id string) ([]byte, error) {
	groups := map[string]any{}
	for group, tokens := range Groups {
		properties := map[string]any{}
		for _, token := range tokens {
			properties[token] = map[string]any{
				"description": fmt.Sprintf("Color of the %s %s token.", group, token),
				"pattern":     colorPattern.String(),
				"type":        "string",
			}
		}
		groups[group] = map[string]any{
			"additionalProperties": false,
			"properties":           properties,
			"type":                 "object",
		}
	}
	palette := map[string]any{
		"additionalProperties": false,
		"properties":           groups,
		"type":                 "object",
	}
	schema := map[string]any{
		"$id":                  id,
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"additionalProperties": false,
		"description":          "Color overrides for the datamitsu config inspector. Every mode, group and token is optional; what a file omits keeps its built-in value.",
		"properties": map[string]any{
			"$schema": map[string]any{"type": "string"},
			"dark":    palette,
			"light":   palette,
		},
		"title": "datamitsu inspector theme",
		"type":  "object",
	}
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode theme schema: %w", err)
	}
	return append(data, '\n'), nil
}

// ContrastWarnings reports text and accent pairs that fall below WCAG AA. Body
// text needs 4.5:1; accent.display is only ever set in headings, which WCAG
// counts as large text at 3:1. They are warnings: a theme is the user's
// decision, not ours.
func ContrastWarnings(theme Theme) []string {
	var warnings []string
	for _, mode := range Modes {
		palette := theme[mode]
		background := palette["surface"]["base"]
		for _, pair := range []struct {
			label, color, text string
			minimum            float64
		}{
			{"text.base on surface.base", palette["text"]["base"], "body text", 4.5},
			{"text.muted on surface.base", palette["text"]["muted"], "body text", 4.5},
			{"accent.base on surface.base", palette["accent"]["base"], "body text", 4.5},
			{"accent.display on surface.base", palette["accent"]["display"], "large text", 3},
		} {
			ratio, ok := contrast(pair.color, background)
			if !ok {
				continue
			}
			if ratio < pair.minimum {
				warnings = append(warnings, fmt.Sprintf(
					"%s: %s has contrast %s:1, below the %s:1 WCAG AA minimum for %s",
					mode, pair.label, strconv.FormatFloat(ratio, 'f', 2, 64),
					strconv.FormatFloat(pair.minimum, 'f', -1, 64), pair.text))
			}
		}
	}
	return warnings
}

func contrast(foreground, background string) (float64, bool) {
	front, ok := luminance(foreground)
	if !ok {
		return 0, false
	}
	back, ok := luminance(background)
	if !ok {
		return 0, false
	}
	if front < back {
		front, back = back, front
	}
	return (front + 0.05) / (back + 0.05), true
}

func luminance(color string) (float64, bool) {
	channels, ok := channels(color)
	if !ok {
		return 0, false
	}
	var linear [3]float64
	for index, value := range channels {
		part := float64(value) / 255
		if part <= 0.03928 {
			linear[index] = part / 12.92
		} else {
			linear[index] = math.Pow((part+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*linear[0] + 0.7152*linear[1] + 0.0722*linear[2], true
}

func channels(color string) ([3]int, bool) {
	digits := strings.TrimPrefix(color, "#")
	if len(digits) == 3 {
		digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
	}
	if len(digits) < 6 {
		return [3]int{}, false
	}
	var out [3]int
	for index := range out {
		value, err := strconv.ParseInt(digits[index*2:index*2+2], 16, 0)
		if err != nil {
			return [3]int{}, false
		}
		out[index] = int(value)
	}
	return out, true
}

func writeTokens(out *strings.Builder, palette map[string]map[string]string) {
	for _, group := range groupNames() {
		for _, token := range Groups[group] {
			value, ok := palette[group][token]
			if !ok {
				continue
			}
			fmt.Fprintf(out, "--%s-%s:%s;", group, token, value)
		}
	}
}

func groupNames() []string {
	names := make([]string, 0, len(Groups))
	for group := range Groups {
		names = append(names, group)
	}
	sort.Strings(names)
	return names
}

func contains(values []string, value string) bool {
	return slices.Contains(values, value)
}

func sortedKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedValues(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
