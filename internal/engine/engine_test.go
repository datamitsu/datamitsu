package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func TestNew(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if engine == nil {
		t.Fatal("New() returned nil")
	}

	if engine.vm == nil {
		t.Error("engine.vm is nil")
	}

	if engine.facts == nil {
		t.Error("engine.facts is nil")
	}
}

func TestVM(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	vm := engine.VM()
	if vm == nil {
		t.Error("VM() returned nil")
	}
}

func TestFacts(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	facts := engine.Facts()
	if facts == nil {
		t.Error("Facts() returned nil")
	}
}

func TestConsoleLog(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `console.log("test");`
	_, err = engine.vm.RunString(script)
	if err != nil {
		t.Errorf("console.log() error = %v", err)
	}
}

func TestConsoleError(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `console.error("test error");`
	_, err = engine.vm.RunString(script)
	if err != nil {
		t.Errorf("console.error() error = %v", err)
	}
}

func TestConsoleWarn(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `console.warn("test warning");`
	_, err = engine.vm.RunString(script)
	if err != nil {
		t.Errorf("console.warn() error = %v", err)
	}
}

func TestConsoleDebug(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `console.debug("test debug");`
	_, err = engine.vm.RunString(script)
	if err != nil {
		t.Errorf("console.debug() error = %v", err)
	}
}

func TestConsoleInfo(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `console.info("test info");`
	_, err = engine.vm.RunString(script)
	if err != nil {
		t.Errorf("console.info() error = %v", err)
	}
}

func TestYAMLParse(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `
		const yaml = "key: value\nnum: 42";
		const result = YAML.parse(yaml);
		result;
	`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("YAML.parse() error = %v", err)
	}

	obj := val.ToObject(engine.vm)
	if obj == nil {
		t.Fatal("YAML.parse() did not return an object")
	}

	keyVal := obj.Get("key")
	if keyVal == nil || keyVal.String() != "value" {
		t.Errorf("key = %v, want 'value'", keyVal)
	}
}

func TestYAMLStringify(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `
		const obj = {key: "value", num: 42};
		const result = YAML.stringify(obj);
		result;
	`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("YAML.stringify() error = %v", err)
	}

	result := val.String()
	if !strings.Contains(result, "key:") {
		t.Error("YAML output does not contain 'key:'")
	}
}

func TestYAMLParseNoArguments(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `YAML.parse();`
	_, err = engine.vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling YAML.parse without arguments")
	}
}

func TestYAMLStringifyNoArguments(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `YAML.stringify();`
	_, err = engine.vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling YAML.stringify without arguments")
	}
}

func TestTOMLParse(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `
		const toml = "key = 'value'\nnum = 42";
		const result = TOML.parse(toml);
		result;
	`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("TOML.parse() error = %v", err)
	}

	obj := val.ToObject(engine.vm)
	if obj == nil {
		t.Fatal("TOML.parse() did not return an object")
	}
}

func TestTOMLStringify(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `
		const obj = {key: "value", num: 42};
		const result = TOML.stringify(obj);
		result;
	`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("TOML.stringify() error = %v", err)
	}

	result := val.String()
	if !strings.Contains(result, "key") {
		t.Error("TOML output does not contain 'key'")
	}
}

func TestTOMLParseNoArguments(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `TOML.parse();`
	_, err = engine.vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling TOML.parse without arguments")
	}
}

func TestTOMLStringifyNoArguments(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `TOML.stringify();`
	_, err = engine.vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling TOML.stringify without arguments")
	}
}

// TestYAMLStringifyFieldOrder verifies that YAML.stringify preserves field order
func TestYAMLStringifyFieldOrder(t *testing.T) {
	script := `
		const obj = {
			zebra: 1,
			alpha: 2,
			mike: 3,
			bravo: 4,
			delta: 5
		};
		YAML.stringify(obj);
	`

	// Run multiple times to ensure deterministic order
	var firstResult string
	for i := 0; i < 10; i++ {
		engine, err := New("")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		val, err := engine.vm.RunString(script)
		if err != nil {
			t.Fatalf("Run %d: YAML.stringify() error = %v", i, err)
		}

		result := val.String()
		if i == 0 {
			firstResult = result
		} else if result != firstResult {
			t.Errorf("Run %d: Field order changed!\nFirst:\n%s\nCurrent:\n%s", i, firstResult, result)
		}
	}

	// Also verify the order matches insertion order (zebra, alpha, mike, bravo, delta)
	lines := strings.Split(strings.TrimSpace(firstResult), "\n")
	expectedOrder := []string{"zebra:", "alpha:", "mike:", "bravo:", "delta:"}

	for i, expected := range expectedOrder {
		if i >= len(lines) {
			t.Errorf("Not enough lines in output. Expected at least %d lines", len(expectedOrder))
			break
		}
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), expected) {
			t.Errorf("Line %d: expected field %s, got: %s", i, expected, lines[i])
		}
	}
}

// TestTOMLStringifyFieldOrder verifies that TOML.stringify preserves field order
func TestTOMLStringifyFieldOrder(t *testing.T) {
	script := `
		const obj = {
			zebra: 1,
			alpha: 2,
			mike: 3,
			bravo: 4,
			delta: 5
		};
		TOML.stringify(obj);
	`

	// Run multiple times to ensure deterministic order
	var firstResult string
	for i := 0; i < 10; i++ {
		engine, err := New("")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		val, err := engine.vm.RunString(script)
		if err != nil {
			t.Fatalf("Run %d: TOML.stringify() error = %v", i, err)
		}

		result := val.String()
		if i == 0 {
			firstResult = result
			t.Logf("TOML output:\n%s", result)
		} else if result != firstResult {
			t.Errorf("Run %d: Field order changed!\nFirst:\n%s\nCurrent:\n%s", i, firstResult, result)
		}
	}

	// Verify the order matches insertion order (zebra, alpha, mike, bravo, delta)
	lines := strings.Split(strings.TrimSpace(firstResult), "\n")
	expectedOrder := []string{"zebra =", "alpha =", "mike =", "bravo =", "delta ="}

	for i, expected := range expectedOrder {
		if i >= len(lines) {
			t.Errorf("Not enough lines in output. Expected at least %d lines", len(expectedOrder))
			break
		}
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), expected) {
			t.Errorf("Line %d: expected field %s, got: %s", i, expected, lines[i])
		}
	}
}

func TestINIParse(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `
		const ini = "[section1]\nkey1 = value1\n[section2]\nkey2 = value2";
		const result = INI.parse(ini);
		result;
	`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("INI.parse() error = %v", err)
	}

	obj := val.ToObject(engine.vm)
	if obj == nil {
		t.Fatal("INI.parse() did not return an array")
	}

	lengthVal := obj.Get("length")
	if lengthVal == nil || goja.IsUndefined(lengthVal) {
		t.Error("result does not have length property")
	}
}

func TestINIToRecord(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `
		const sections = [
			{name: "section1", properties: {key1: "value1"}},
			{name: "section2", properties: {key2: "value2"}}
		];
		const result = INI.toRecord(sections);
		result;
	`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("INI.toRecord() error = %v", err)
	}

	obj := val.ToObject(engine.vm)
	if obj == nil {
		t.Fatal("INI.toRecord() did not return an object")
	}

	section1 := obj.Get("section1")
	if section1 == nil || goja.IsUndefined(section1) {
		t.Error("result does not have section1")
	}
}

func TestINIStringify(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `
		const sections = [
			{name: "section1", properties: {key1: "value1"}},
			{name: "section2", properties: {key2: "value2"}}
		];
		const result = INI.stringify(sections);
		result;
	`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("INI.stringify() error = %v", err)
	}

	result := val.String()
	if !strings.Contains(result, "[section1]") {
		t.Error("INI output does not contain '[section1]'")
	}
}

func TestINIParseNoArguments(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `INI.parse();`
	_, err = engine.vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling INI.parse without arguments")
	}
}

func TestINIToRecordNoArguments(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `INI.toRecord();`
	_, err = engine.vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling INI.toRecord without arguments")
	}
}

func TestINIStringifyNoArguments(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `INI.stringify();`
	_, err = engine.vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling INI.stringify without arguments")
	}
}

func TestConvertGojaValueToOrderedStructure(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		name     string
		script   string
		validate func(t *testing.T, result interface{})
	}{
		{
			name:   "primitive string",
			script: `"hello"`,
			validate: func(t *testing.T, result interface{}) {
				if str, ok := result.(string); !ok || str != "hello" {
					t.Errorf("expected string 'hello', got %v", result)
				}
			},
		},
		{
			name:   "primitive number",
			script: `42`,
			validate: func(t *testing.T, result interface{}) {
				if _, ok := result.(int64); !ok {
					t.Errorf("expected int64, got %T", result)
				}
			},
		},
		{
			name:   "primitive boolean",
			script: `true`,
			validate: func(t *testing.T, result interface{}) {
				if b, ok := result.(bool); !ok || !b {
					t.Errorf("expected bool true, got %v", result)
				}
			},
		},
		{
			name:   "null",
			script: `null`,
			validate: func(t *testing.T, result interface{}) {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
			},
		},
		{
			name:   "undefined",
			script: `undefined`,
			validate: func(t *testing.T, result interface{}) {
				if result != nil {
					t.Errorf("expected nil for undefined, got %v", result)
				}
			},
		},
		{
			name:   "array",
			script: `[1, 2, 3]`,
			validate: func(t *testing.T, result interface{}) {
				arr, ok := result.([]interface{})
				if !ok {
					t.Errorf("expected []interface{}, got %T", result)
					return
				}
				if len(arr) != 3 {
					t.Errorf("expected length 3, got %d", len(arr))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := engine.vm.RunString(tt.script)
			if err != nil {
				t.Fatalf("RunString() error = %v", err)
			}

			result := engine.convertGojaValueToOrderedStructure(val)
			tt.validate(t, result)
		})
	}
}

func TestFactsFunction(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `
		const f = facts();
		f;
	`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("facts() error = %v", err)
	}

	if val == nil || goja.IsUndefined(val) {
		t.Error("facts() returned undefined")
	}
}

func TestFactsLibcExposed(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `
		const f = facts();
		f.libc;
	`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("facts().libc error = %v", err)
	}

	libc := val.String()
	validValues := map[string]bool{"glibc": true, "musl": true, "unknown": true}
	if !validValues[libc] {
		t.Errorf("facts().libc = %q, want one of glibc, musl, unknown", libc)
	}
}

func TestToolsRegistered(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `typeof tools !== 'undefined'`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}

	if !val.ToBoolean() {
		t.Error("tools not registered in VM")
	}
}

func TestINIToRecordInvalidArgument(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `INI.toRecord({});`
	_, err = engine.vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling INI.toRecord with invalid argument")
	}
}

func TestINIStringifyInvalidArgument(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `INI.stringify({});`
	_, err = engine.vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling INI.stringify with invalid argument")
	}
}

func TestINIToRecordMergesSameSections(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `
		const sections = [
			{name: "section1", properties: {key1: "value1"}},
			{name: "section1", properties: {key2: "value2"}}
		];
		const result = INI.toRecord(sections);
		result.section1.key1 + "," + result.section1.key2;
	`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("INI.toRecord() error = %v", err)
	}

	result := val.String()
	if result != "value1,value2" {
		t.Errorf("result = %q, want 'value1,value2'", result)
	}
}

func TestRunWithTimeout_InfiniteLoop(t *testing.T) {
	e, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	start := time.Now()
	_, err = e.RunWithTimeout("while(true){}", 200*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected error from infinite loop, got nil")
	}
	if elapsed > 3*time.Second {
		t.Errorf("timeout took too long: %v (expected ~200ms)", elapsed)
	}
}

func TestRunWithTimeout_CompletesBeforeDeadline(t *testing.T) {
	e, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	val, err := e.RunWithTimeout("1 + 1", 5*time.Second)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if val.ToInteger() != 2 {
		t.Errorf("expected 2, got %v", val)
	}
}

func TestNewPanicRecovery(t *testing.T) {
	testInitHook = func(*Engine) { panic("injected test panic") }
	defer func() { testInitHook = nil }()

	_, err := New("")
	if err == nil {
		t.Fatal("expected error from panic recovery, got nil")
	}
	if !strings.Contains(err.Error(), "engine initialization panic") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestConvertGojaValueCircularReference(t *testing.T) {
	engine, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := `
		const obj = {};
		obj.self = obj;
		obj;
	`

	val, err := engine.vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}

	result := engine.convertGojaValueToOrderedStructure(val)
	if result == nil {
		t.Error("expected non-nil result for circular reference")
	}
}
