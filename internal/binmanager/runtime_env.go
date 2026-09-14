package binmanager

import (
	"fmt"
	"strings"
)

// ExpandAppBinaryBindings shares binding syntax between validation and read-only resolution.
func ExpandAppBinaryBindings(value string, resolve func(string) (string, error)) (string, error) {
	return expandAppBindings(value, resolve, nil)
}

func expandAppBindings(value string, resolve func(string) (string, error), paths *strings.Replacer) (string, error) {
	var out strings.Builder
	for {
		start := strings.Index(value, "${")
		if start < 0 {
			out.WriteString(value)
			return out.String(), nil
		}
		out.WriteString(value[:start])
		value = value[start:]
		end := strings.IndexByte(value, '}')
		if end < 0 {
			return "", fmt.Errorf("unterminated placeholder %q", value)
		}
		token := value[2:end]
		switch {
		case strings.Contains(token, "${"):
			return "", fmt.Errorf("malformed nested placeholder %q", value[:end+1])
		case strings.HasPrefix(token, "APP_BIN:"):
			name := strings.TrimPrefix(token, "APP_BIN:")
			if name == "" || strings.ContainsAny(name, "{}:$ \t\r\n") {
				return "", fmt.Errorf("malformed APP_BIN placeholder %q", value[:end+1])
			}
			path, err := resolve(name)
			if err != nil {
				return "", err
			}
			if path == "" {
				return "", fmt.Errorf("APP_BIN target %q resolved to an empty path", name)
			}
			out.WriteString(path)
		case token == "APP_BIN":
			return "", fmt.Errorf("malformed APP_BIN placeholder %q", value[:end+1])
		case strings.Contains(token, ":"):
			return "", fmt.Errorf("unknown placeholder %q", value[:end+1])
		default:
			literal := value[:end+1]
			if paths != nil {
				literal = paths.Replace(literal)
			}
			out.WriteString(literal)
		}
		value = value[end+1:]
	}
}
