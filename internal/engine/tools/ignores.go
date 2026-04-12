package tools

import (
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"fmt"
	"sort"
	"strings"

	"github.com/dop251/goja"
)

type ParseIgnoreResult struct {
	Groups     map[string][]string `json:"groups"`
	GroupOrder []string            `json:"groupOrder"`
}

func ParseIgnoreFile(content string) ParseIgnoreResult {
	groups := make(map[string][]string)
	groupOrder := make([]string, 0)
	seenGroups := make(map[string]bool)

	lines := strings.Split(content, "\n")

	currentGroup := ""
	inManagedSection := false
	groupJustSet := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.Contains(trimmed, fmt.Sprintf("# %s >>>", ldflags.PackageName)) {
			inManagedSection = true
			currentGroup = ""
			groupJustSet = false
			continue
		}
		if strings.Contains(trimmed, fmt.Sprintf("# %s <<<", ldflags.PackageName)) {
			inManagedSection = false
			currentGroup = ""
			groupJustSet = false
			continue
		}
		if inManagedSection {
			continue
		}

		if trimmed == "" {
			if !groupJustSet {
				currentGroup = ""
			}
			groupJustSet = false
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			groupName := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			if groupName != "" {
				currentGroup = groupName
				groupJustSet = true
				if _, exists := groups[currentGroup]; !exists {
					groups[currentGroup] = []string{}
					if !seenGroups[currentGroup] {
						groupOrder = append(groupOrder, currentGroup)
						seenGroups[currentGroup] = true
					}
				}
			}
			continue
		}

		groupJustSet = false

		if currentGroup == "" {
			currentGroup = "Other"
			if _, exists := groups[currentGroup]; !exists {
				groups[currentGroup] = []string{}
				if !seenGroups[currentGroup] {
					groupOrder = append(groupOrder, currentGroup)
					seenGroups[currentGroup] = true
				}
			}
		}

		groups[currentGroup] = append(groups[currentGroup], line)
	}

	return ParseIgnoreResult{
		Groups:     groups,
		GroupOrder: groupOrder,
	}
}

func FormatIgnoreFile(groups map[string][]string, groupOrder []string) string {
	var result strings.Builder

	processedGroups := make(map[string]bool)

	for _, groupName := range groupOrder {
		rules, exists := groups[groupName]
		if !exists || len(rules) == 0 {
			continue
		}

		// Deduplicate rules while preserving order
		uniqueRules := deduplicateSlice(rules)

		fmt.Fprintf(&result, "# %s\n", groupName)
		for _, rule := range uniqueRules {
			result.WriteString(rule + "\n")
		}
		result.WriteString("\n")
		processedGroups[groupName] = true
	}

	remainingGroups := make([]string, 0)
	for groupName := range groups {
		if !processedGroups[groupName] && len(groups[groupName]) > 0 {
			remainingGroups = append(remainingGroups, groupName)
		}
	}
	sort.Strings(remainingGroups)

	for _, groupName := range remainingGroups {
		rules := groups[groupName]

		// Deduplicate rules while preserving order
		uniqueRules := deduplicateSlice(rules)

		fmt.Fprintf(&result, "# %s\n", groupName)
		for _, rule := range uniqueRules {
			result.WriteString(rule + "\n")
		}
		result.WriteString("\n")
	}

	return strings.TrimSpace(result.String())
}

// deduplicateSlice removes duplicate strings while preserving order (keeps first occurrence)
func deduplicateSlice(items []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(items))

	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}

	return result
}

func RegisterIgnoreToolsInVM(vm *goja.Runtime) error {
	toolsObj := vm.Get("tools")
	if toolsObj == nil || goja.IsUndefined(toolsObj) || goja.IsNull(toolsObj) {
		toolsObj = vm.NewObject()
		_ = vm.Set("tools", toolsObj)
	}

	ignoreObj := vm.NewObject()

	_ = ignoreObj.Set("parse", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.NewTypeError("parse requires 1 argument: content"))
		}

		content := call.Argument(0).String()
		result := ParseIgnoreFile(content)

		return vm.ToValue(result)
	})

	_ = ignoreObj.Set("stringify", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.NewTypeError("stringify requires at least 1 argument: groups"))
		}

		groupsObj, ok := call.Argument(0).(*goja.Object)
		if !ok {
			panic(vm.NewTypeError("first argument must be an object"))
		}

		convertedGroups := make(map[string][]string)

		for _, key := range groupsObj.Keys() {
			val := groupsObj.Get(key)
			exported := val.Export()

			switch v := exported.(type) {
			case []interface{}:
				strArr := make([]string, len(v))
				for i, item := range v {
					strArr[i] = fmt.Sprint(item)
				}
				convertedGroups[key] = strArr
			case []string:
				convertedGroups[key] = v
			case nil:
				convertedGroups[key] = []string{}
			default:
				if arr, ok := val.(*goja.Object); ok {
					lengthVal := arr.Get("length")
					if lengthVal != nil && !goja.IsUndefined(lengthVal) && !goja.IsNull(lengthVal) {
						length := int(lengthVal.ToInteger())
						strArr := make([]string, length)
						for i := 0; i < length; i++ {
							itemVal := arr.Get(fmt.Sprint(i))
							if itemVal != nil && !goja.IsUndefined(itemVal) && !goja.IsNull(itemVal) {
								strArr[i] = itemVal.String()
							}
						}
						convertedGroups[key] = strArr
						continue
					}
				}

				panic(vm.NewTypeError(fmt.Sprintf("group '%s' must be an array, got %T", key, exported)))
			}
		}

		var groupOrder []string
		if len(call.Arguments) >= 2 && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
			orderArg := call.Argument(1)
			orderExported := orderArg.Export()

			switch v := orderExported.(type) {
			case []interface{}:
				groupOrder = make([]string, len(v))
				for i, item := range v {
					groupOrder[i] = fmt.Sprint(item)
				}
			case []string:
				groupOrder = v
			default:
				if arr, ok := orderArg.(*goja.Object); ok {
					lengthVal := arr.Get("length")
					if lengthVal != nil && !goja.IsUndefined(lengthVal) && !goja.IsNull(lengthVal) {
						length := int(lengthVal.ToInteger())
						groupOrder = make([]string, length)
						for i := 0; i < length; i++ {
							itemVal := arr.Get(fmt.Sprint(i))
							if itemVal != nil && !goja.IsUndefined(itemVal) && !goja.IsNull(itemVal) {
								groupOrder[i] = itemVal.String()
							}
						}
					} else {
						panic(vm.NewTypeError(fmt.Sprintf("second argument must be an array, got %T", orderExported)))
					}
				} else {
					panic(vm.NewTypeError(fmt.Sprintf("second argument must be an array, got %T", orderExported)))
				}
			}
		}

		result := FormatIgnoreFile(convertedGroups, groupOrder)
		return vm.ToValue(result)
	})

	_ = toolsObj.(*goja.Object).Set("Ignore", ignoreObj)

	return nil
}
