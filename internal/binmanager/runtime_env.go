package binmanager

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const defaultToolCacheSlot = "default"

// ValidateCacheNames rejects invalid, reserved, or duplicate app cache slots.
func ValidateCacheNames(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if err := validateCacheName(name); err != nil {
			return err
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate cache name %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateCacheName(name string) error {
	if name == defaultToolCacheSlot {
		return fmt.Errorf("cache name %q is reserved", name)
	}
	if len(name) > 255 {
		return fmt.Errorf("cache name exceeds 255 bytes (got %d)", len(name))
	}
	if name == "" {
		return fmt.Errorf("invalid cache name %q", name)
	}
	for i := range len(name) {
		c := name[i]
		if (c < 'a' || c > 'z') && c != '_' {
			return fmt.Errorf("invalid cache name %q", name)
		}
	}
	if isWindowsDeviceName(name) {
		return fmt.Errorf("cache name %q is a reserved Windows device name", name)
	}
	return nil
}

func isWindowsDeviceName(name string) bool {
	return name == "con" || name == "prn" || name == "aux" || name == "nul"
}

// ValidateToolCacheTokens validates app cache placeholders against declarations.
func ValidateToolCacheTokens(value string, names []string) (bool, error) {
	used := false
	err := forEachToolCacheToken(value, func(_, _ int, name string) error {
		used = true
		if name != "" && !containsCacheName(names, name) {
			return fmt.Errorf("unknown tool cache %q", name)
		}
		return nil
	})
	return used, err
}

func containsCacheName(names []string, name string) bool {
	return slices.Contains(names, name)
}

const toolCachePrefix = "{toolCache"

func startsToolCacheToken(value string) bool {
	rest, ok := strings.CutPrefix(value, toolCachePrefix)
	return ok && (rest == "" || rest[0] == '}' || rest[0] == ':')
}

func containsToolCacheToken(value string) bool {
	for {
		i := strings.Index(value, toolCachePrefix)
		if i < 0 {
			return false
		}
		if startsToolCacheToken(value[i:]) {
			return true
		}
		value = value[i+len(toolCachePrefix):]
	}
}

// scanDollarPlaceholder reports the exclusive end and unquoted syntax in the span.
func scanDollarPlaceholder(value string) (end int, hasCache, hasNested, hasColon bool) {
	hasCache = startsToolCacheToken(value[1:])
	depth := 1
	var quote byte
	for i := 2; i < len(value); i++ {
		if quote != 0 {
			if value[i] == quote {
				quote = 0
			} else if quote == '"' && value[i] == '\\' {
				i++
			}
			continue
		}
		switch value[i] {
		case '\'', '"':
			quote = value[i]
		case '\\':
			i++
		case ':':
			hasColon = true
		case '$':
			hasNested = hasNested || strings.HasPrefix(value[i:], "${")
		case '{':
			hasCache = hasCache || startsToolCacheToken(value[i:])
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1, hasCache, hasNested, hasColon
			}
		}
	}
	return -1, hasCache, hasNested, hasColon
}

func forEachToolCacheToken(value string, visit func(int, int, string) error) error {
	searchFrom := 0
	for searchFrom < len(value) {
		if strings.HasPrefix(value[searchFrom:], "${") {
			width, hasCache, _, _ := scanDollarPlaceholder(value[searchFrom:])
			end := len(value)
			if width >= 0 {
				end = searchFrom + width
			}
			if hasCache {
				return fmt.Errorf("malformed nested placeholder %q", value[searchFrom:end])
			}
			searchFrom = end
			continue
		}
		if !startsToolCacheToken(value[searchFrom:]) {
			searchFrom++
			continue
		}
		start := searchFrom
		relativeEnd := strings.IndexByte(value[start:], '}')
		if relativeEnd < 0 {
			return fmt.Errorf("malformed tool cache placeholder %q", value[start:])
		}
		end := start + relativeEnd + 1
		token := value[start+1 : end-1]
		name := ""
		if token != "toolCache" {
			if !strings.HasPrefix(token, "toolCache:") {
				return fmt.Errorf("malformed tool cache placeholder %q", value[start:end])
			}
			name = token[len("toolCache:"):]
			if err := validateCacheName(name); err != nil {
				return fmt.Errorf("malformed tool cache placeholder %q: %w", value[start:end], err)
			}
		}
		if err := visit(start, end, name); err != nil {
			return err
		}
		searchFrom = end
	}
	return nil
}

func appendCacheSlot(slots []string, slot string) []string {
	if slices.Contains(slots, slot) {
		return slots
	}
	return append(slots, slot)
}

// ExpandToolCacheTokens maps app cache placeholders to sibling slot paths.
func ExpandToolCacheTokens(value, parent string, names []string) (string, []string, error) {
	if !containsToolCacheToken(value) {
		return value, nil, nil
	}
	var out strings.Builder
	out.Grow(len(value))
	var slots []string
	last := 0
	err := forEachToolCacheToken(value, func(start, end int, name string) error {
		if name != "" && !containsCacheName(names, name) {
			return fmt.Errorf("unknown tool cache %q", name)
		}
		slot := name
		if slot == "" {
			slot = defaultToolCacheSlot
		}
		out.WriteString(value[last:start])
		out.WriteString(filepath.Join(parent, slot))
		slots = appendCacheSlot(slots, slot)
		last = end
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	out.WriteString(value[last:])
	return out.String(), slots, nil
}

// ExpandAppBinaryBindings shares binding syntax between validation and read-only resolution.
func ExpandAppBinaryBindings(value string, resolve func(string) (string, error)) (string, error) {
	return expandAppBindings(value, resolve, nil)
}

func expandAppBindings(value string, resolve func(string) (string, error), paths *strings.Replacer) (string, error) {
	expanded, _, err := expandRuntimeTemplate(value, resolve, paths, "", nil)
	return expanded, err
}

func expandRuntimeEnvPlaceholders(value, cacheParent string, cacheNames []string, resolve func(string) (string, error), paths *strings.Replacer) (string, []string, error) {
	return expandRuntimeTemplate(value, resolve, paths, cacheParent, cacheNames)
}

func expandRuntimeTemplate(value string, resolve func(string) (string, error), paths *strings.Replacer, cacheParent string, cacheNames []string) (string, []string, error) {
	var out strings.Builder
	out.Grow(len(value))
	var slots []string
	for pos := 0; pos < len(value); {
		start := pos
		for start < len(value) && value[start] != '$' && (cacheParent == "" || value[start] != '{' || !startsToolCacheToken(value[start:])) {
			start++
		}
		literal := value[pos:start]
		if paths != nil {
			literal = paths.Replace(literal)
		}
		out.WriteString(literal)
		if start == len(value) {
			break
		}
		if startsToolCacheToken(value[start:]) {
			endRel := strings.IndexByte(value[start:], '}')
			if endRel < 0 {
				return "", nil, fmt.Errorf("malformed tool cache placeholder %q", value[start:])
			}
			end := start + endRel + 1
			token := value[start+1 : end-1]
			name := ""
			if token != "toolCache" {
				if !strings.HasPrefix(token, "toolCache:") {
					return "", nil, fmt.Errorf("malformed tool cache placeholder %q", value[start:end])
				}
				name = token[len("toolCache:"):]
				if err := validateCacheName(name); err != nil {
					return "", nil, fmt.Errorf("malformed tool cache placeholder %q: %w", value[start:end], err)
				}
				if !containsCacheName(cacheNames, name) {
					return "", nil, fmt.Errorf("unknown tool cache %q", name)
				}
			}
			slot := name
			if slot == "" {
				slot = defaultToolCacheSlot
			}
			out.WriteString(filepath.Join(cacheParent, slot))
			slots = appendCacheSlot(slots, slot)
			pos = end
			continue
		}
		if value[start] != '$' || !strings.HasPrefix(value[start:], "${") {
			out.WriteByte(value[start])
			pos = start + 1
			continue
		}
		width, hasCache, hasNested, hasColon := scanDollarPlaceholder(value[start:])
		if width < 0 {
			if hasCache {
				return "", nil, fmt.Errorf("malformed nested placeholder %q", value[start:])
			}
			return "", nil, fmt.Errorf("unterminated placeholder %q", value[start:])
		}
		end := start + width
		placeholder := value[start:end]
		token := value[start+2 : end-1]
		if hasNested || hasCache {
			return "", nil, fmt.Errorf("malformed nested placeholder %q", placeholder)
		}
		switch {
		case strings.HasPrefix(token, "APP_BIN:"):
			name := strings.TrimPrefix(token, "APP_BIN:")
			if name == "" || strings.ContainsAny(name, "{}:$ \t\r\n") {
				return "", nil, fmt.Errorf("malformed APP_BIN placeholder %q", placeholder)
			}
			path, err := resolve(name)
			if err != nil {
				return "", nil, err
			}
			if path == "" {
				return "", nil, fmt.Errorf("APP_BIN target %q resolved to an empty path", name)
			}
			out.WriteString(path)
		case token == "APP_BIN":
			return "", nil, fmt.Errorf("malformed APP_BIN placeholder %q", placeholder)
		case hasColon:
			return "", nil, fmt.Errorf("unknown placeholder %q", placeholder)
		case paths != nil && (token == "STORE" || token == "APP_DIR" || token == "APP_PREPARED"):
			out.WriteString(paths.Replace(placeholder))
		default:
			out.WriteString(placeholder)
		}
		pos = end
	}
	return out.String(), slots, nil
}
