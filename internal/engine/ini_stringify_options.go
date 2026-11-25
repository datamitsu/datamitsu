package engine

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/dop251/goja"
	"gopkg.in/ini.v1"
)

// stringifyINIOptions applies per-call formatting options without changing the
// ini package's process-wide writer settings.
func (e *Engine) stringifyINIOptions(cfg *ini.File, value goja.Value) (string, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return writeINI(cfg)
	}
	obj, ok := value.(*goja.Object)
	if !ok || obj.ClassName() != "Object" {
		return "", errors.New("INI.stringify options must be an object")
	}

	allowed := map[string]bool{
		"indent": true, "delimiter": true, "spaceAroundDelimiter": true,
		"align": true, "blankLinesBetweenSections": true,
		"defaultHeader": true, "lineEnding": true,
	}
	keys := obj.Keys()
	if len(keys) == 0 {
		return writeINI(cfg)
	}
	for _, key := range keys {
		if !allowed[key] {
			return "", fmt.Errorf("unknown INI.stringify option %q", key)
		}
	}

	indent := ""
	delimiter := "="
	spaceAround, align, blankLines, defaultHeader := true, true, true, false
	lineEnding := "\n"
	if v := obj.Get("indent"); v != nil && !goja.IsUndefined(v) {
		if v.ExportType() != nil && v.ExportType().Kind().String() != "string" {
			return "", errors.New("INI.stringify option indent must be a string")
		}
		indent = v.String()
	}
	if v := obj.Get("delimiter"); v != nil && !goja.IsUndefined(v) {
		if v.ExportType() != nil && v.ExportType().Kind().String() != "string" {
			return "", errors.New("INI.stringify option delimiter must be a string")
		}
		delimiter = v.String()
		if delimiter != "=" && delimiter != ":" {
			return "", errors.New("INI.stringify option delimiter must be '=' or ':'")
		}
	}
	boolOptions := []struct {
		name string
		dst  *bool
	}{
		{"spaceAroundDelimiter", &spaceAround},
		{"align", &align},
		{"blankLinesBetweenSections", &blankLines},
		{"defaultHeader", &defaultHeader},
	}
	for _, option := range boolOptions {
		v := obj.Get(option.name)
		if v == nil || goja.IsUndefined(v) {
			continue
		}
		b, ok := v.Export().(bool)
		if !ok {
			return "", fmt.Errorf("INI.stringify option %s must be a boolean", option.name)
		}
		*option.dst = b
	}
	if v := obj.Get("lineEnding"); v != nil && !goja.IsUndefined(v) {
		if v.ExportType() != nil && v.ExportType().Kind().String() != "string" {
			return "", errors.New("INI.stringify option lineEnding must be a string")
		}
		switch v.String() {
		case "lf":
			lineEnding = "\n"
		case "crlf":
			lineEnding = "\r\n"
		default:
			return "", errors.New("INI.stringify option lineEnding must be lf or crlf")
		}
	}

	if strings.Trim(indent, " \t") != "" {
		return "", errors.New("INI.stringify indent must contain only spaces or tabs")
	}
	var out strings.Builder
	for _, section := range cfg.Sections() {
		keys := section.Keys()
		isDefault := section.Name() == ini.DefaultSection
		if isDefault && !defaultHeader && len(keys) == 0 {
			continue
		}
		if out.Len() > 0 && blankLines {
			out.WriteString(lineEnding)
		}
		if !isDefault || defaultHeader {
			out.WriteString("[" + section.Name() + "]" + lineEnding)
		}
		names := make([]string, len(keys))
		width := 0
		for i, key := range keys {
			name := key.Name()
			switch {
			case strings.Contains(name, "`"):
				name = "\"\"\"" + name + "\"\"\""
			case strings.ContainsAny(name, "=:\""):
				name = "`" + name + "`"
			}
			names[i] = name
			width = max(width, len(name))
		}
		for i, key := range keys {
			if !isDefault {
				out.WriteString(indent)
			}
			out.WriteString(names[i])
			if align {
				out.WriteString(strings.Repeat(" ", width-len(names[i])))
			}
			if spaceAround {
				out.WriteByte(' ')
			}
			out.WriteString(delimiter)
			if spaceAround {
				out.WriteByte(' ')
			}
			v := key.Value()
			switch {
			case strings.ContainsAny(v, "\n`"):
				v = "\"\"\"" + v + "\"\"\""
			case strings.HasPrefix(v, `"""`), strings.ContainsAny(v, "#;"), len(v) >= 2 && v[0] == v[len(v)-1] && strings.ContainsRune("\"'", rune(v[0])):
				v = "`" + v + "`"
			case strings.TrimSpace(v) != v:
				if strings.Contains(v, "\"") {
					v = "`" + v + "`"
				} else {
					v = "\"" + v + "\""
				}
			}
			out.WriteString(v)
			out.WriteString(lineEnding)
		}
	}
	return out.String(), nil
}

func writeINI(cfg *ini.File) (string, error) {
	var buf bytes.Buffer
	if _, err := cfg.WriteTo(&buf); err != nil {
		return "", fmt.Errorf("write INI: %w", err)
	}
	return buf.String(), nil
}
