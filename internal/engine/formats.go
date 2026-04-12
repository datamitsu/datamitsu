package engine

import (
	"bytes"
	"fmt"

	"github.com/dop251/goja"
	"github.com/goccy/go-yaml"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/ini.v1"
)

// convertMapSliceToMap recursively converts yaml.MapSlice to map[string]interface{}
// This is used for TOML encoding, which doesn't preserve order from maps,
// but we convert it anyway and then encode field by field to maintain order
func convertMapSliceToMap(val interface{}) interface{} {
	switch v := val.(type) {
	case yaml.MapSlice:
		result := make(map[string]interface{})
		for _, item := range v {
			key, ok := item.Key.(string)
			if !ok {
				continue
			}
			result[key] = convertMapSliceToMap(item.Value)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = convertMapSliceToMap(item)
		}
		return result
	default:
		return v
	}
}

// tomlStringifyOrdered encodes a yaml.MapSlice to TOML preserving key order
// It manually writes primitives first, then tables, to maintain proper TOML structure
func tomlStringifyOrdered(slice yaml.MapSlice) (string, error) {
	var buf bytes.Buffer
	var tables []yaml.MapItem

	// First pass: write all primitive values (non-table entries)
	for _, item := range slice {
		key, ok := item.Key.(string)
		if !ok {
			continue
		}

		value := convertMapSliceToMap(item.Value)

		// Check if this is a table (map) or a primitive value
		if _, isMap := value.(map[string]interface{}); isMap {
			// Defer tables to second pass
			tables = append(tables, item)
			continue
		}

		// Encode primitive value
		singleItem := map[string]interface{}{key: value}
		itemBytes, err := toml.Marshal(singleItem)
		if err != nil {
			return "", fmt.Errorf("failed to marshal TOML item %s: %w", key, err)
		}

		buf.Write(itemBytes)
	}

	// Second pass: write all tables in order
	for _, item := range tables {
		key := item.Key.(string)
		value := convertMapSliceToMap(item.Value)

		singleItem := map[string]interface{}{key: value}
		itemBytes, err := toml.Marshal(singleItem)
		if err != nil {
			return "", fmt.Errorf("failed to marshal TOML table %s: %w", key, err)
		}

		buf.Write(itemBytes)
	}

	return buf.String(), nil
}

// convertGojaValueToOrderedStructure converts a goja.Value to Go structures while preserving key order
// Uses yaml.MapSlice for objects to maintain insertion order
func (e *Engine) convertGojaValueToOrderedStructure(val goja.Value) interface{} {
	return e.convertGojaValueToOrderedStructureWithVisited(val, make(map[*goja.Object]bool))
}

func (e *Engine) convertGojaValueToOrderedStructureWithVisited(val goja.Value, visited map[*goja.Object]bool) interface{} {
	if goja.IsNull(val) || goja.IsUndefined(val) {
		return nil
	}

	exported := val.Export()

	// Handle primitive types
	switch exported.(type) {
	case string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool:
		return exported
	case nil:
		return nil
	}

	// Try to get as object
	obj := val.ToObject(e.vm)
	if obj == nil {
		return exported
	}

	// Check for circular references
	if visited[obj] {
		return nil
	}
	visited[obj] = true

	// Check if it's an array
	if className := obj.ClassName(); className == "Array" {
		lengthVal := obj.Get("length")
		if lengthVal != nil && !goja.IsUndefined(lengthVal) && !goja.IsNull(lengthVal) {
			length := int(lengthVal.ToInteger())
			result := make([]interface{}, length)
			for i := 0; i < length; i++ {
				itemVal := obj.Get(fmt.Sprint(i))
				result[i] = e.convertGojaValueToOrderedStructureWithVisited(itemVal, visited)
			}
			return result
		}
	}

	// It's an object - convert to MapSlice to preserve order
	keys := obj.Keys()
	result := make(yaml.MapSlice, 0, len(keys))
	for _, key := range keys {
		itemVal := obj.Get(key)
		result = append(result, yaml.MapItem{
			Key:   key,
			Value: e.convertGojaValueToOrderedStructureWithVisited(itemVal, visited),
		})
	}
	return result
}

func (e *Engine) initFormats() {
	// YAML
	_ = e.vm.Set("YAML", map[string]interface{}{
		"parse": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(e.vm.NewTypeError("YAML.parse requires at least 1 argument"))
			}

			yamlStr := call.Argument(0).String()
			var result interface{}

			if err := yaml.Unmarshal([]byte(yamlStr), &result); err != nil {
				panic(e.vm.NewGoError(fmt.Errorf("YAML.parse error: %w", err)))
			}

			return e.vm.ToValue(result)
		},
		"stringify": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(e.vm.NewTypeError("YAML.stringify requires at least 1 argument"))
			}

			// Convert goja value to ordered structure preserving key order
			orderedValue := e.convertGojaValueToOrderedStructure(call.Argument(0))
			yamlBytes, err := yaml.Marshal(orderedValue)

			if err != nil {
				panic(e.vm.NewGoError(fmt.Errorf("YAML.stringify error: %w", err)))
			}

			return e.vm.ToValue(string(yamlBytes))
		},
	})

	// TOML
	_ = e.vm.Set("TOML", map[string]interface{}{
		"parse": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(e.vm.NewTypeError("TOML.parse requires at least 1 argument"))
			}

			tomlStr := call.Argument(0).String()
			var result interface{}

			if err := toml.Unmarshal([]byte(tomlStr), &result); err != nil {
				panic(e.vm.NewGoError(fmt.Errorf("TOML.parse error: %w", err)))
			}

			return e.vm.ToValue(result)
		},
		"stringify": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(e.vm.NewTypeError("TOML.stringify requires at least 1 argument"))
			}

			// Convert goja value to ordered structure preserving key order
			orderedValue := e.convertGojaValueToOrderedStructure(call.Argument(0))

			var tomlStr string
			var err error

			// Check if it's a MapSlice (ordered object) and use ordered encoding
			if slice, ok := orderedValue.(yaml.MapSlice); ok {
				tomlStr, err = tomlStringifyOrdered(slice)
			} else {
				// For other types, use regular TOML marshaling
				tomlBytes, marshalErr := toml.Marshal(orderedValue)
				if marshalErr != nil {
					err = marshalErr
				} else {
					tomlStr = string(tomlBytes)
				}
			}

			if err != nil {
				panic(e.vm.NewGoError(fmt.Errorf("TOML.stringify error: %w", err)))
			}

			return e.vm.ToValue(tomlStr)
		},
	})

	// INI
	iniNamespace := e.vm.NewObject()

	_ = iniNamespace.Set("parse", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(e.vm.NewTypeError("INI.parse requires at least 1 argument"))
		}

		iniStr := call.Argument(0).String()
		cfg, err := ini.Load([]byte(iniStr))

		if err != nil {
			panic(e.vm.NewGoError(fmt.Errorf("INI.parse error: %w", err)))
		}

		// Convert INI to array of section entries preserving order
		sections := make([]interface{}, 0)
		for _, section := range cfg.Sections() {
			sectionObj := e.vm.NewObject()
			propsObj := e.vm.NewObject()

			// Add properties in order
			for _, key := range section.Keys() {
				_ = propsObj.Set(key.Name(), key.Value())
			}

			_ = sectionObj.Set("name", section.Name())
			_ = sectionObj.Set("properties", propsObj)
			sections = append(sections, sectionObj)
		}

		return e.vm.ToValue(sections)
	})

	// Convert array of sections to a record, merging sections with the same name
	_ = iniNamespace.Set("toRecord", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(e.vm.NewTypeError("INI.toRecord requires at least 1 argument"))
		}

		sectionsVal := call.Argument(0)
		sectionsObj := sectionsVal.ToObject(e.vm)
		if sectionsObj == nil {
			panic(e.vm.NewTypeError("INI.toRecord argument must be an array"))
		}

		lengthVal := sectionsObj.Get("length")
		if lengthVal == nil || goja.IsUndefined(lengthVal) {
			panic(e.vm.NewTypeError("INI.toRecord argument must be an array"))
		}

		length := int(lengthVal.ToInteger())
		result := e.vm.NewObject()

		// Process each section entry, merging properties for sections with the same name
		for i := 0; i < length; i++ {
			sectionEntryVal := sectionsObj.Get(fmt.Sprint(i))
			sectionEntryObj := sectionEntryVal.ToObject(e.vm)
			if sectionEntryObj == nil {
				continue
			}

			nameVal := sectionEntryObj.Get("name")
			propsVal := sectionEntryObj.Get("properties")

			if nameVal == nil || goja.IsUndefined(nameVal) || propsVal == nil || goja.IsUndefined(propsVal) {
				continue
			}

			sectionName := nameVal.String()
			propsObj := propsVal.ToObject(e.vm)

			// Get or create section object
			existingSection := result.Get(sectionName)
			var sectionObj *goja.Object
			if existingSection != nil && !goja.IsUndefined(existingSection) {
				sectionObj = existingSection.ToObject(e.vm)
			} else {
				sectionObj = e.vm.NewObject()
				_ = result.Set(sectionName, sectionObj)
			}

			// Merge properties (later values override earlier ones)
			if propsObj != nil {
				keys := propsObj.Keys()
				for _, key := range keys {
					val := propsObj.Get(key)
					if val != nil && !goja.IsUndefined(val) {
						_ = sectionObj.Set(key, val)
					}
				}
			}
		}

		return result
	})

	_ = iniNamespace.Set("stringify", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(e.vm.NewTypeError("INI.stringify requires at least 1 argument"))
		}

		sectionsVal := call.Argument(0)
		sectionsObj := sectionsVal.ToObject(e.vm)
		if sectionsObj == nil {
			panic(e.vm.NewTypeError("INI.stringify argument must be an array"))
		}

		lengthVal := sectionsObj.Get("length")
		if lengthVal == nil || goja.IsUndefined(lengthVal) {
			panic(e.vm.NewTypeError("INI.stringify argument must be an array"))
		}

		length := int(lengthVal.ToInteger())
		cfg := ini.Empty()

		// Process each section entry
		for i := 0; i < length; i++ {
			sectionEntryVal := sectionsObj.Get(fmt.Sprint(i))
			sectionEntryObj := sectionEntryVal.ToObject(e.vm)
			if sectionEntryObj == nil {
				continue
			}

			nameVal := sectionEntryObj.Get("name")
			propsVal := sectionEntryObj.Get("properties")

			if nameVal == nil || goja.IsUndefined(nameVal) || propsVal == nil || goja.IsUndefined(propsVal) {
				continue
			}

			sectionName := nameVal.String()
			section, err := cfg.NewSection(sectionName)
			if err != nil {
				panic(e.vm.NewGoError(fmt.Errorf("INI.stringify error: %w", err)))
			}

			// Add properties preserving order
			propsObj := propsVal.ToObject(e.vm)
			if propsObj != nil {
				keys := propsObj.Keys()
				for _, key := range keys {
					val := propsObj.Get(key)
					if val != nil && !goja.IsUndefined(val) {
						_, err := section.NewKey(key, val.String())
						if err != nil {
							panic(e.vm.NewGoError(fmt.Errorf("INI.stringify error: %w", err)))
						}
					}
				}
			}
		}

		var buf bytes.Buffer
		_, err := cfg.WriteTo(&buf)
		if err != nil {
			panic(e.vm.NewGoError(fmt.Errorf("INI.stringify error: %w", err)))
		}

		return e.vm.ToValue(buf.String())
	})

	_ = e.vm.Set("INI", iniNamespace)
}
