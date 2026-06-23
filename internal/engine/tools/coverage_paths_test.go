package tools

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// When a "tools" object already exists in the VM, RegisterIgnoreToolsInVM must
// reuse it rather than overwrite it.
func TestRegisterIgnoreToolsInVM_ReusesExistingToolsObject(t *testing.T) {
	vm := goja.New()
	existing := vm.NewObject()
	_ = existing.Set("marker", "kept")
	_ = vm.Set("tools", existing)

	if err := RegisterIgnoreToolsInVM(vm); err != nil {
		t.Fatalf("RegisterIgnoreToolsInVM() error = %v", err)
	}

	v, err := vm.RunString(`tools.marker`)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}
	if v.String() != "kept" {
		t.Errorf("existing tools.marker = %q, want kept", v.String())
	}
	if iv, err := vm.RunString(`typeof tools.Ignore.parse`); err != nil || iv.String() != "function" {
		t.Errorf("tools.Ignore.parse not registered onto existing object: %v %q", err, iv)
	}
}

func TestIgnoreStringify_NullAndEmptyGroups(t *testing.T) {
	vm := goja.New()
	if err := RegisterIgnoreToolsInVM(vm); err != nil {
		t.Fatalf("RegisterIgnoreToolsInVM() error = %v", err)
	}

	// A null group value maps to an empty group (nil branch); an empty array
	// stays empty. Groups with no rules are omitted entirely, so the result is
	// an empty string.
	script := `
		const groups = { "Empty": null, "Other": [] };
		tools.Ignore.stringify(groups, ["Empty", "Other"]);
	`
	v, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}
	if got := v.String(); got != "" {
		t.Errorf("null/empty groups should stringify to empty, got %q", got)
	}
}

func TestIgnoreStringify_NonArrayGroupErrors(t *testing.T) {
	vm := goja.New()
	if err := RegisterIgnoreToolsInVM(vm); err != nil {
		t.Fatalf("RegisterIgnoreToolsInVM() error = %v", err)
	}

	// A group whose value is a plain number is neither an array nor an
	// array-like object → the default branch must panic with a type error.
	script := `tools.Ignore.stringify({ "Bad": 42 });`
	_, err := vm.RunString(script)
	if err == nil || !strings.Contains(err.Error(), "must be an array") {
		t.Errorf("expected 'must be an array' error, got %v", err)
	}
}
