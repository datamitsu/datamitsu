package engine

import (
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
)

// DeterministicValue converts v into a goja value whose property enumeration
// order does not depend on Go map iteration order.
//
// goja wraps a Go map lazily and enumerates it by ranging over the map, so
// Object.keys(), a spread and JSON.stringify() all observe Go's randomized
// iteration order. Config JS is expected to be a pure function of the config
// cache key (internal/configcache): a config that turns enumeration order into
// an ordered output — an array built from Object.keys(), a joined string —
// would otherwise produce a different result per evaluation, and the first one
// to be stored would then be served forever.
//
// Only map-bearing subtrees are materialized: a value whose type cannot reach a
// map is handed to goja untouched, so it keeps the live Go-backed object with
// its methods and write-through semantics. Shapes are mirrored exactly as goja
// produces them — a nil map is an empty object, a nil slice an empty array, a
// nil pointer null, and a field whose `json` tag is absent or not an identifier
// stays hidden — so JS sees the same value it always did, in a fixed order.
func DeterministicValue(vm *goja.Runtime, v any) goja.Value {
	if v == nil {
		return goja.Null()
	}
	return deterministicValue(vm, reflect.ValueOf(v))
}

func deterministicValue(vm *goja.Runtime, rv reflect.Value) goja.Value {
	if !rv.IsValid() {
		return goja.Null()
	}
	t := rv.Type()
	if !typeContainsMap(t) {
		return vm.ToValue(rv.Interface())
	}

	// Only four kinds can carry a map; everything else is one fallback, so this
	// is a chain rather than a switch over reflect.Kind.
	kind := t.Kind()

	if kind == reflect.Interface || kind == reflect.Pointer {
		if rv.IsNil() {
			return goja.Null()
		}
		return deterministicValue(vm, rv.Elem())
	}

	if kind == reflect.Map {
		return mapToObject(vm, rv)
	}

	if kind == reflect.Slice || kind == reflect.Array {
		items := make([]any, rv.Len())
		for i := range items {
			items[i] = deterministicValue(vm, rv.Index(i))
		}
		return vm.NewArray(items...)
	}

	if kind == reflect.Struct {
		obj := vm.NewObject()
		for f := range t.Fields() {
			if !f.IsExported() {
				continue
			}
			name := jsFieldName(f)
			if name == "" {
				continue
			}
			_ = obj.Set(name, deterministicValue(vm, rv.FieldByIndex(f.Index)))
		}
		return obj
	}

	return vm.ToValue(rv.Interface())
}

// mapToObject materializes a Go map as a JS object with its keys inserted in
// sorted order. A map whose key is not a string falls back to goja's own
// conversion: no such map is reachable from the config or from facts, and
// guessing at a canonical order for another key type would be worse than
// leaving it as it was.
func mapToObject(vm *goja.Runtime, rv reflect.Value) goja.Value {
	if rv.Type().Key().Kind() != reflect.String {
		return vm.ToValue(rv.Interface())
	}

	keys := make([]string, 0, rv.Len())
	values := make(map[string]reflect.Value, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		key := iter.Key().String()
		keys = append(keys, key)
		values[key] = iter.Value()
	}
	sort.Strings(keys)

	obj := vm.NewObject()
	for _, key := range keys {
		_ = obj.Set(key, deterministicValue(vm, values[key]))
	}
	return obj
}

// jsFieldName mirrors goja.TagFieldNameMapper("json", true), the mapper every
// config VM is built with: the tag up to the first comma when it is a valid
// identifier, and otherwise no name at all, which hides the field.
func jsFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	if parser.IsIdentifier(tag) {
		return tag
	}
	return ""
}

// containsMapCache memoizes typeContainsMap. The answer is a property of the
// type, and the same handful of config types are converted on every layer of
// every load.
var containsMapCache sync.Map // reflect.Type -> bool

// typeContainsMap reports whether a value of type t can reach a Go map through
// the fields JS can see. It is the gate that keeps materialization off every
// map-free subtree.
func typeContainsMap(t reflect.Type) bool {
	if cached, ok := containsMapCache.Load(t); ok {
		if result, isBool := cached.(bool); isBool {
			return result
		}
	}
	result := containsMapWalk(t, map[reflect.Type]bool{})
	containsMapCache.Store(t, result)
	return result
}

func containsMapWalk(t reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true

	kind := t.Kind()

	// An interface says nothing about its dynamic type, so a map cannot be ruled
	// out; the value walk recurses into the concrete value and gates again there.
	if kind == reflect.Map || kind == reflect.Interface {
		return true
	}

	if kind == reflect.Pointer || kind == reflect.Slice || kind == reflect.Array {
		return containsMapWalk(t.Elem(), seen)
	}

	if kind == reflect.Struct {
		for f := range t.Fields() {
			if !f.IsExported() || jsFieldName(f) == "" {
				continue
			}
			if containsMapWalk(f.Type, seen) {
				return true
			}
		}
	}

	return false
}
