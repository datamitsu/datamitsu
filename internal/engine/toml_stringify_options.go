package engine

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/dop251/goja"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// tomlOptions builds a TOML marshaler configured from TOML.stringify options.
func (e *Engine) tomlOptions(opts goja.Value) (func(any) ([]byte, error), error) {
	indent := "  "
	indentTables := false
	multilineArrays := false
	inlineTables := false
	quoteStyle := ""

	if opts != nil && !goja.IsUndefined(opts) && !goja.IsNull(opts) {
		obj, ok := opts.(*goja.Object)
		if !ok || obj.ClassName() != "Object" {
			return nil, errors.New("options must be an object")
		}
		if len(obj.Keys()) == 0 {
			return toml.Marshal, nil
		}
		for _, key := range obj.Keys() {
			value := obj.Get(key)
			if goja.IsUndefined(value) {
				continue
			}
			switch key {
			case "indent":
				var ok bool
				indent, ok = value.Export().(string)
				if !ok || strings.Trim(indent, " \t") != "" {
					return nil, errors.New("indent must contain only spaces or tabs")
				}
			case "indentTables":
				v, ok := value.Export().(bool)
				if !ok {
					return nil, errors.New("indentTables must be a boolean")
				}
				indentTables = v
			case "multilineArrays":
				v, ok := value.Export().(bool)
				if !ok {
					return nil, errors.New("multilineArrays must be a boolean")
				}
				multilineArrays = v
			case "inlineTables":
				v, ok := value.Export().(bool)
				if !ok {
					return nil, errors.New("inlineTables must be a boolean")
				}
				inlineTables = v
			case "quoteStyle":
				if goja.IsUndefined(value) || goja.IsNull(value) {
					return nil, errors.New("quoteStyle must be 'single' or 'double'")
				}
				quoteStyle = value.String()
				if quoteStyle != "single" && quoteStyle != "double" {
					return nil, errors.New("quoteStyle must be 'single' or 'double'")
				}
			default:
				return nil, fmt.Errorf("unknown TOML.stringify option %q", key)
			}
		}
	}
	if opts == nil || goja.IsUndefined(opts) || goja.IsNull(opts) {
		return toml.Marshal, nil
	}

	return func(value any) ([]byte, error) {
		var buf bytes.Buffer
		enc := toml.NewEncoder(&buf).SetIndentSymbol(indent).SetIndentTables(indentTables).SetArraysMultiline(multilineArrays).SetTablesInline(inlineTables)
		if err := enc.Encode(value); err != nil {
			return nil, fmt.Errorf("encode TOML: %w", err)
		}
		out := buf.Bytes()
		if quoteStyle == "double" {
			return tomlDoubleQuoteStrings(out)
		}
		return append([]byte(nil), out...), nil
	}, nil
}

func tomlDoubleQuoteStrings(src []byte) ([]byte, error) {
	var parser unstable.Parser
	parser.Reset(src)
	var ranges []unstable.Range
	for parser.NextExpression() {
		n := parser.Expression()
		var visit func(*unstable.Node)
		visit = func(node *unstable.Node) {
			if node == nil {
				return
			}
			if node.Kind == unstable.String {
				raw := parser.Raw(node.Raw)
				if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
					ranges = append(ranges, node.Raw)
				}
			}
			it := node.Children()
			for it.Next() {
				visit(it.Node())
			}
		}
		visit(n)
	}
	if err := parser.Error(); err != nil {
		return nil, fmt.Errorf("parse encoded TOML: %w", err)
	}
	if len(ranges) == 0 {
		return append([]byte(nil), src...), nil
	}
	var out bytes.Buffer
	last := 0
	for _, r := range ranges {
		start, end := r.Offset, r.Offset+r.Length
		out.Write(src[last:start])
		out.WriteByte('"')
		for _, c := range src[start+1 : end-1] {
			if c == '"' || c == '\\' {
				out.WriteByte('\\')
			}
			out.WriteByte(c)
		}
		out.WriteByte('"')
		last = int(end)
	}
	out.Write(src[last:])
	return out.Bytes(), nil
}
