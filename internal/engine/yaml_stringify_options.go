package engine

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/dop251/goja"
	yaml "github.com/goccy/go-yaml"
)

type yamlStringifyOptions struct {
	indent                  int
	indentSequence          bool
	flow                    bool
	literalStyleIfMultiline bool
	quoteStyle              string
}

func (e *Engine) stringifyYAMLOptions(input any, opts goja.Value) ([]byte, error) {
	settings := yamlStringifyOptions{indent: 2, quoteStyle: "double", indentSequence: false, flow: false, literalStyleIfMultiline: false}
	if opts != nil && !goja.IsUndefined(opts) && !goja.IsNull(opts) {
		obj, ok := opts.(*goja.Object)
		if !ok || obj.ClassName() != "Object" {
			return nil, errors.New("options must be an object")
		}
		for _, key := range obj.Keys() {
			value := obj.Get(key)
			if goja.IsUndefined(value) {
				continue
			}
			switch key {
			case "indent":
				n, ok := value.Export().(int64)
				if !ok || n < 1 || n > 16 {
					return nil, errors.New("YAML.stringify option indent must be an integer from 1 to 16")
				}
				settings.indent = int(n)
			case "indentSequence", "flow", "literalStyleIfMultiline":
				b, ok := value.Export().(bool)
				if !ok {
					return nil, fmt.Errorf("YAML.stringify option %s must be a boolean", key)
				}
				switch key {
				case "indentSequence":
					settings.indentSequence = b
				case "flow":
					settings.flow = b
				case "literalStyleIfMultiline":
					settings.literalStyleIfMultiline = b
				}
			case "quoteStyle":
				s, ok := value.Export().(string)
				if !ok || s != "single" && s != "double" {
					return nil, errors.New("YAML.stringify option quoteStyle must be 'single' or 'double'")
				}
				settings.quoteStyle = s
			default:
				return nil, fmt.Errorf("YAML.stringify unknown option %q", key)
			}
		}
	}
	if settings.flow && settings.literalStyleIfMultiline {
		return nil, errors.New("YAML.stringify options flow and literalStyleIfMultiline cannot both be true")
	}

	options := []yaml.EncodeOption{
		yaml.Indent(settings.indent),
		yaml.IndentSequence(settings.indentSequence),
		yaml.Flow(settings.flow),
		yaml.UseLiteralStyleIfMultiline(settings.literalStyleIfMultiline && !settings.flow),
		yaml.UseSingleQuote(settings.quoteStyle == "single"),
	}
	baseline, err := yaml.MarshalWithOptions(input, options[:len(options)-1]...)
	if err != nil {
		return nil, fmt.Errorf("encode YAML: %w", err)
	}
	if settings.quoteStyle != "single" {
		return baseline, nil
	}
	preferred, err := yaml.MarshalWithOptions(input, options...)
	if err != nil {
		return nil, fmt.Errorf("encode YAML with single quotes: %w", err)
	}
	var a, b any
	if yaml.UnmarshalWithOptions(baseline, &a, yaml.UseOrderedMap()) == nil &&
		yaml.UnmarshalWithOptions(preferred, &b, yaml.UseOrderedMap()) == nil && reflect.DeepEqual(a, b) {
		return preferred, nil
	}
	return baseline, nil
}
