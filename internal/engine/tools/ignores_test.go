package tools

import (
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"fmt"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestParseIgnoreFile(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		expectedGroups map[string][]string
		expectedOrder  []string
	}{
		{
			name: "simple groups",
			content: `# Dependencies
node_modules/
vendor/

# Build
dist/
build/`,
			expectedGroups: map[string][]string{
				"Dependencies": {"node_modules/", "vendor/"},
				"Build":        {"dist/", "build/"},
			},
			expectedOrder: []string{"Dependencies", "Build"},
		},
		{
			name: "ungrouped patterns",
			content: `*.log
*.tmp`,
			expectedGroups: map[string][]string{
				"Other": {"*.log", "*.tmp"},
			},
			expectedOrder: []string{"Other"},
		},
		{
			name: "mixed grouped and ungrouped",
			content: `*.log

# Cache
.cache/

*.tmp`,
			expectedGroups: map[string][]string{
				"Other": {"*.log", "*.tmp"},
				"Cache": {".cache/"},
			},
			expectedOrder: []string{"Other", "Cache"},
		},
		{
			name: "managed section is ignored",
			content: fmt.Sprintf(`# Custom
custom/

# %s >>>
managed/
# %s <<<

# Other
other/`, ldflags.PackageName, ldflags.PackageName),
			expectedGroups: map[string][]string{
				"Custom": {"custom/"},
				"Other":  {"other/"},
			},
			expectedOrder: []string{"Custom", "Other"},
		},
		{
			name:           "empty content",
			content:        "",
			expectedGroups: map[string][]string{},
			expectedOrder:  []string{},
		},
		{
			name: "comment without patterns",
			content: `# Empty Group

# Another Group
pattern.txt`,
			expectedGroups: map[string][]string{
				"Empty Group":   {},
				"Another Group": {"pattern.txt"},
			},
			expectedOrder: []string{"Empty Group", "Another Group"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseIgnoreFile(tt.content)

			if len(result.Groups) != len(tt.expectedGroups) {
				t.Errorf("Groups count = %d, want %d", len(result.Groups), len(tt.expectedGroups))
			}

			for groupName, expectedPatterns := range tt.expectedGroups {
				patterns, exists := result.Groups[groupName]
				if !exists {
					t.Errorf("Group %q not found", groupName)
					continue
				}

				if len(patterns) != len(expectedPatterns) {
					t.Errorf("Group %q: patterns count = %d, want %d", groupName, len(patterns), len(expectedPatterns))
					continue
				}

				for i, pattern := range patterns {
					if pattern != expectedPatterns[i] {
						t.Errorf("Group %q: pattern[%d] = %q, want %q", groupName, i, pattern, expectedPatterns[i])
					}
				}
			}

			if len(result.GroupOrder) != len(tt.expectedOrder) {
				t.Errorf("GroupOrder length = %d, want %d", len(result.GroupOrder), len(tt.expectedOrder))
			}

			for i, order := range result.GroupOrder {
				if i < len(tt.expectedOrder) && order != tt.expectedOrder[i] {
					t.Errorf("GroupOrder[%d] = %q, want %q", i, order, tt.expectedOrder[i])
				}
			}
		})
	}
}

func TestFormatIgnoreFile(t *testing.T) {
	tests := []struct {
		name       string
		groups     map[string][]string
		groupOrder []string
		expected   string
	}{
		{
			name: "simple format",
			groups: map[string][]string{
				"Dependencies": {"node_modules/", "vendor/"},
				"Build":        {"dist/", "build/"},
			},
			groupOrder: []string{"Dependencies", "Build"},
			expected: `# Dependencies
node_modules/
vendor/

# Build
dist/
build/`,
		},
		{
			name: "deduplicate patterns",
			groups: map[string][]string{
				"Cache": {".cache/", ".cache/", "tmp/"},
			},
			groupOrder: []string{"Cache"},
			expected: `# Cache
.cache/
tmp/`,
		},
		{
			name: "groups not in order",
			groups: map[string][]string{
				"A": {"a.txt"},
				"B": {"b.txt"},
				"C": {"c.txt"},
			},
			groupOrder: []string{"B", "A"},
			expected: `# B
b.txt

# A
a.txt

# C
c.txt`,
		},
		{
			name:       "empty groups",
			groups:     map[string][]string{},
			groupOrder: []string{},
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatIgnoreFile(tt.groups, tt.groupOrder)

			if result != tt.expected {
				t.Errorf("FormatIgnoreFile() =\n%s\n\nwant\n%s", result, tt.expected)
			}
		})
	}
}

func TestDeduplicateSlice(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "with duplicates",
			input:    []string{"a", "b", "a", "c", "b"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "no duplicates",
			input:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "empty slice",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "all same",
			input:    []string{"a", "a", "a"},
			expected: []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deduplicateSlice(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("len(result) = %d, want %d", len(result), len(tt.expected))
				return
			}

			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("result[%d] = %q, want %q", i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestRegisterIgnoreToolsInVM(t *testing.T) {
	vm := goja.New()

	err := RegisterIgnoreToolsInVM(vm)
	if err != nil {
		t.Fatalf("RegisterIgnoreToolsInVM() error = %v", err)
	}

	toolsVal := vm.Get("tools")
	if toolsVal == nil || goja.IsUndefined(toolsVal) {
		t.Fatal("tools not registered in VM")
	}

	toolsObj := toolsVal.ToObject(vm)
	ignoreVal := toolsObj.Get("Ignore")
	if ignoreVal == nil || goja.IsUndefined(ignoreVal) {
		t.Fatal("tools.Ignore not registered")
	}
}

func TestIgnoreParseInVM(t *testing.T) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	if err := RegisterIgnoreToolsInVM(vm); err != nil {
		t.Fatalf("RegisterIgnoreToolsInVM() error = %v", err)
	}

	script := `
		const content = "# Test\npattern.txt\n";
		const result = tools.Ignore.parse(content);
		result;
	`

	val, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}

	obj := val.ToObject(vm)
	if obj == nil {
		t.Fatal("result is not an object")
	}

	groupsVal := obj.Get("groups")
	if groupsVal == nil || goja.IsUndefined(groupsVal) {
		t.Error("result.groups is undefined")
	}

	groupOrderVal := obj.Get("groupOrder")
	if groupOrderVal == nil || goja.IsUndefined(groupOrderVal) {
		t.Error("result.groupOrder is undefined")
	}
}

func TestIgnoreStringifyInVM(t *testing.T) {
	vm := goja.New()
	if err := RegisterIgnoreToolsInVM(vm); err != nil {
		t.Fatalf("RegisterIgnoreToolsInVM() error = %v", err)
	}

	script := `
		const groups = {
			"Test": ["pattern1.txt", "pattern2.txt"],
			"Other": ["pattern3.txt"]
		};
		const groupOrder = ["Test", "Other"];
		const result = tools.Ignore.stringify(groups, groupOrder);
		result;
	`

	val, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}

	result := val.String()
	if !strings.Contains(result, "# Test") {
		t.Error("result does not contain '# Test'")
	}

	if !strings.Contains(result, "pattern1.txt") {
		t.Error("result does not contain 'pattern1.txt'")
	}
}

func TestIgnoreParseNoArguments(t *testing.T) {
	vm := goja.New()
	if err := RegisterIgnoreToolsInVM(vm); err != nil {
		t.Fatalf("RegisterIgnoreToolsInVM() error = %v", err)
	}

	script := `tools.Ignore.parse();`

	_, err := vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling parse without arguments")
	}
}

func TestIgnoreStringifyNoArguments(t *testing.T) {
	vm := goja.New()
	if err := RegisterIgnoreToolsInVM(vm); err != nil {
		t.Fatalf("RegisterIgnoreToolsInVM() error = %v", err)
	}

	script := `tools.Ignore.stringify();`

	_, err := vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling stringify without arguments")
	}
}

func TestIgnoreStringifyInvalidArgument(t *testing.T) {
	vm := goja.New()
	if err := RegisterIgnoreToolsInVM(vm); err != nil {
		t.Fatalf("RegisterIgnoreToolsInVM() error = %v", err)
	}

	script := `tools.Ignore.stringify("not an object");`

	_, err := vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling stringify with invalid argument")
	}
}

func TestIgnoreStringifyWithoutGroupOrder(t *testing.T) {
	vm := goja.New()
	if err := RegisterIgnoreToolsInVM(vm); err != nil {
		t.Fatalf("RegisterIgnoreToolsInVM() error = %v", err)
	}

	script := `
		const groups = {
			"Test": ["pattern.txt"]
		};
		const result = tools.Ignore.stringify(groups);
		result;
	`

	val, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}

	result := val.String()
	if !strings.Contains(result, "# Test") {
		t.Error("result does not contain '# Test'")
	}
}

func TestParseIgnoreResult(t *testing.T) {
	result := ParseIgnoreResult{
		Groups: map[string][]string{
			"Test": {"pattern.txt"},
		},
		GroupOrder: []string{"Test"},
	}

	if len(result.Groups) != 1 {
		t.Errorf("len(Groups) = %d, want 1", len(result.Groups))
	}

	if len(result.GroupOrder) != 1 {
		t.Errorf("len(GroupOrder) = %d, want 1", len(result.GroupOrder))
	}
}
