package engine

import (
	"errors"
	"fmt"

	"github.com/dop251/goja"
)

// initJSONStringifyOptions extends JSON.stringify with a named options form:
// JSON.stringify(value, {indent: number|string}). Standard replacer/space calls
// continue through goja's built-in implementation unchanged.
func (e *Engine) initJSONStringifyOptions() error {
	jsonValue := e.vm.Get("JSON")
	jsonObject, ok := jsonValue.(*goja.Object)
	if !ok {
		return errors.New("JSON global is not an object")
	}
	stringifyValue := jsonObject.Get("stringify")
	stringify, ok := goja.AssertFunction(stringifyValue)
	if !ok {
		return errors.New("JSON.stringify is not callable")
	}

	if err := jsonObject.Set("stringify", func(call goja.FunctionCall) goja.Value {
		args := call.Arguments
		if options, ok := call.Argument(1).(*goja.Object); ok && options.ClassName() == "Object" && len(args) == 2 {
			space := goja.Undefined()
			for _, key := range options.Keys() {
				if key != "indent" {
					panic(e.vm.NewTypeError("unknown JSON.stringify option %q", key))
				}
				space = options.Get(key)
				if goja.IsUndefined(space) {
					continue
				}
				switch v := space.Export().(type) {
				case int64, float64:
				case string:
					for _, r := range v {
						if r != ' ' && r != '\t' {
							panic(e.vm.NewTypeError("JSON.stringify indent must contain only spaces or tabs"))
						}
					}
				default:
					panic(e.vm.NewTypeError("JSON.stringify indent must be a number or string"))
				}
			}
			args = []goja.Value{args[0], goja.Null(), space}
		}
		result, err := stringify(call.This, args...)
		if err != nil {
			panic(err)
		}
		return result
	}); err != nil {
		return fmt.Errorf("register JSON.stringify: %w", err)
	}
	return nil
}
