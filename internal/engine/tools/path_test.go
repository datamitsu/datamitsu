package tools

import (
	"path/filepath"
	"testing"

	"github.com/dop251/goja"
)

func TestPathRel_SingleArgUsesRootPath(t *testing.T) {
	vm := goja.New()
	rootPath := "/repo/root"

	if err := RegisterPathToolsInVM(vm, rootPath); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	val, err := vm.RunString(`tools.Path.rel("/repo/root/src/file.go")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected, _ := filepath.Rel(rootPath, "/repo/root/src/file.go")
	if val.String() != expected {
		t.Errorf("got %q, want %q", val.String(), expected)
	}
}

func TestPathRel_TwoArgsUsesProvidedBase(t *testing.T) {
	vm := goja.New()
	rootPath := "/repo/root"

	if err := RegisterPathToolsInVM(vm, rootPath); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	val, err := vm.RunString(`tools.Path.rel("/custom/base/sub/file.go", "/custom/base")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected, _ := filepath.Rel("/custom/base", "/custom/base/sub/file.go")
	if val.String() != expected {
		t.Errorf("got %q, want %q", val.String(), expected)
	}
}

func TestPathRel_SecondArgUndefinedUsesDefault(t *testing.T) {
	vm := goja.New()
	rootPath := "/repo/root"

	if err := RegisterPathToolsInVM(vm, rootPath); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	val, err := vm.RunString(`tools.Path.rel("/repo/root/pkg/main.go", undefined)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected, _ := filepath.Rel(rootPath, "/repo/root/pkg/main.go")
	if val.String() != expected {
		t.Errorf("got %q, want %q", val.String(), expected)
	}
}

func TestPathRel_SecondArgNullUsesDefault(t *testing.T) {
	vm := goja.New()
	rootPath := "/repo/root"

	if err := RegisterPathToolsInVM(vm, rootPath); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	val, err := vm.RunString(`tools.Path.rel("/repo/root/pkg/main.go", null)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected, _ := filepath.Rel(rootPath, "/repo/root/pkg/main.go")
	if val.String() != expected {
		t.Errorf("got %q, want %q", val.String(), expected)
	}
}

func TestPathRel_NoArgsPanics(t *testing.T) {
	vm := goja.New()

	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	_, err := vm.RunString(`tools.Path.rel()`)
	if err == nil {
		t.Error("expected error when calling rel with no arguments")
	}
}

func TestPathJoin(t *testing.T) {
	vm := goja.New()

	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	val, err := vm.RunString(`tools.Path.join("/repo", "src", "file.go")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join("/repo", "src", "file.go")
	if val.String() != expected {
		t.Errorf("got %q, want %q", val.String(), expected)
	}
}

func TestPathJoin_NoArgs(t *testing.T) {
	vm := goja.New()

	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	val, err := vm.RunString(`tools.Path.join()`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if val.String() != "" {
		t.Errorf("got %q, want empty string", val.String())
	}
}

func TestPathAbs(t *testing.T) {
	vm := goja.New()

	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	val, err := vm.RunString(`tools.Path.abs("/repo/src/file.go")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected, _ := filepath.Abs("/repo/src/file.go")
	if val.String() != expected {
		t.Errorf("got %q, want %q", val.String(), expected)
	}
}

func TestPathAbs_NoArgsPanics(t *testing.T) {
	vm := goja.New()

	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	_, err := vm.RunString(`tools.Path.abs()`)
	if err == nil {
		t.Error("expected error when calling abs with no arguments")
	}
}

func TestPathForImport_SameDirectoryPaths(t *testing.T) {
	vm := goja.New()
	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	tests := []struct {
		input    string
		expected string
	}{
		{`.datamitsu/file.js`, `./.datamitsu/file.js`},
		{`file.js`, `./file.js`},
		{`src/utils/helper.ts`, `./src/utils/helper.ts`},
	}

	for _, tt := range tests {
		val, err := vm.RunString(`tools.Path.forImport("` + tt.input + `")`)
		if err != nil {
			t.Fatalf("forImport(%q) error: %v", tt.input, err)
		}
		if val.String() != tt.expected {
			t.Errorf("forImport(%q) = %q, want %q", tt.input, val.String(), tt.expected)
		}
	}
}

func TestPathForImport_ParentDirectoryPaths(t *testing.T) {
	vm := goja.New()
	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	tests := []struct {
		input    string
		expected string
	}{
		{`../.datamitsu/file.js`, `../.datamitsu/file.js`},
		{`../../.datamitsu/file.js`, `../../.datamitsu/file.js`},
		{`../config.js`, `../config.js`},
	}

	for _, tt := range tests {
		val, err := vm.RunString(`tools.Path.forImport("` + tt.input + `")`)
		if err != nil {
			t.Fatalf("forImport(%q) error: %v", tt.input, err)
		}
		if val.String() != tt.expected {
			t.Errorf("forImport(%q) = %q, want %q", tt.input, val.String(), tt.expected)
		}
	}
}

func TestPathForImport_Idempotence(t *testing.T) {
	vm := goja.New()
	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	tests := []struct {
		input    string
		expected string
	}{
		{`./.datamitsu/file.js`, `./.datamitsu/file.js`},
		{`./file.js`, `./file.js`},
		{`../.datamitsu/file.js`, `../.datamitsu/file.js`},
	}

	for _, tt := range tests {
		val, err := vm.RunString(`tools.Path.forImport("` + tt.input + `")`)
		if err != nil {
			t.Fatalf("forImport(%q) error: %v", tt.input, err)
		}
		if val.String() != tt.expected {
			t.Errorf("forImport(%q) = %q, want %q (idempotence)", tt.input, val.String(), tt.expected)
		}
	}
}

func TestPathForImport_EdgeCases(t *testing.T) {
	vm := goja.New()
	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	tests := []struct {
		input    string
		expected string
	}{
		{``, `./`},
		{`.`, `./`},
		{`./`, `./`},
		{`..`, `..`},
	}

	for _, tt := range tests {
		val, err := vm.RunString(`tools.Path.forImport("` + tt.input + `")`)
		if err != nil {
			t.Fatalf("forImport(%q) error: %v", tt.input, err)
		}
		if val.String() != tt.expected {
			t.Errorf("forImport(%q) = %q, want %q", tt.input, val.String(), tt.expected)
		}
	}
}

func TestPathForImport_AbsolutePathPanics(t *testing.T) {
	vm := goja.New()
	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	_, err := vm.RunString(`tools.Path.forImport("/absolute/path")`)
	if err == nil {
		t.Error("expected error for absolute path")
	}
}

func TestPathForImport_NoArgsPanics(t *testing.T) {
	vm := goja.New()
	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	_, err := vm.RunString(`tools.Path.forImport()`)
	if err == nil {
		t.Error("expected error when calling forImport with no arguments")
	}
}

func TestPathForImport_IntegrationWithJoin(t *testing.T) {
	vm := goja.New()
	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	val, err := vm.RunString(`tools.Path.forImport(tools.Path.join(".datamitsu", "eslint.config.js"))`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "./.datamitsu/eslint.config.js"
	if val.String() != expected {
		t.Errorf("forImport(join(...)) = %q, want %q", val.String(), expected)
	}
}

func TestPathForImport_IntegrationWithRel(t *testing.T) {
	vm := goja.New()
	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	val, err := vm.RunString(`tools.Path.forImport(tools.Path.rel("/repo/.datamitsu/eslint.config.js"))`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "./.datamitsu/eslint.config.js"
	if val.String() != expected {
		t.Errorf("forImport(rel(...)) = %q, want %q", val.String(), expected)
	}
}
