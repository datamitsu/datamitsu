package tools

import (
	"path/filepath"
	"testing"

	"github.com/dop251/goja"
)

func TestRegisterConfigLinksInVM(t *testing.T) {
	vm := goja.New()

	registry := map[string]string{
		"eslint-base.js": "eslint",
	}

	err := RegisterConfigLinksInVM(vm, registry, "/repo")
	if err != nil {
		t.Fatalf("RegisterConfigLinksInVM() error = %v", err)
	}

	toolsVal := vm.Get("tools")
	if toolsVal == nil || goja.IsUndefined(toolsVal) {
		t.Fatal("tools not registered in VM")
	}

	toolsObj := toolsVal.ToObject(vm)
	configVal := toolsObj.Get("Config")
	if configVal == nil || goja.IsUndefined(configVal) {
		t.Fatal("tools.Config not registered")
	}

	configObj := configVal.ToObject(vm)
	linkPathVal := configObj.Get("linkPath")
	if linkPathVal == nil || goja.IsUndefined(linkPathVal) {
		t.Fatal("tools.Config.linkPath not registered")
	}
}

func TestConfigLinkPathCorrectPath(t *testing.T) {
	vm := goja.New()

	gitRoot := "/repo"
	registry := map[string]string{
		"eslint-base.js": "eslint",
		"prettier.config": "prettier",
	}

	if err := RegisterConfigLinksInVM(vm, registry, gitRoot); err != nil {
		t.Fatalf("RegisterConfigLinksInVM() error = %v", err)
	}

	// From a subdirectory, get relative path to the .datamitsu/ link
	script := `tools.Config.linkPath("eslint", "eslint-base.js", "/repo/packages/my-app");`
	val, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}

	result := val.String()
	expected, _ := filepath.Rel("/repo/packages/my-app", filepath.Join(gitRoot, ".datamitsu", "eslint-base.js"))
	if result != expected {
		t.Errorf("linkPath() = %q, want %q", result, expected)
	}
}

func TestConfigLinkPathFromRoot(t *testing.T) {
	vm := goja.New()

	gitRoot := "/repo"
	registry := map[string]string{
		"eslint-base.js": "eslint",
	}

	if err := RegisterConfigLinksInVM(vm, registry, gitRoot); err != nil {
		t.Fatalf("RegisterConfigLinksInVM() error = %v", err)
	}

	script := `tools.Config.linkPath("eslint", "eslint-base.js", "/repo");`
	val, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}

	result := val.String()
	expected := filepath.Join(".datamitsu", "eslint-base.js")
	if result != expected {
		t.Errorf("linkPath() = %q, want %q", result, expected)
	}
}

func TestConfigLinkPathWrongAppName(t *testing.T) {
	vm := goja.New()

	registry := map[string]string{
		"eslint-base.js": "eslint",
	}

	if err := RegisterConfigLinksInVM(vm, registry, "/repo"); err != nil {
		t.Fatalf("RegisterConfigLinksInVM() error = %v", err)
	}

	script := `tools.Config.linkPath("prettier", "eslint-base.js", "/repo");`
	_, err := vm.RunString(script)
	if err == nil {
		t.Error("expected error when using wrong appName")
	}
}

func TestConfigLinkPathNonexistentKey(t *testing.T) {
	vm := goja.New()

	registry := map[string]string{
		"eslint-base.js": "eslint",
	}

	if err := RegisterConfigLinksInVM(vm, registry, "/repo"); err != nil {
		t.Fatalf("RegisterConfigLinksInVM() error = %v", err)
	}

	script := `tools.Config.linkPath("eslint", "nonexistent.js", "/repo");`
	_, err := vm.RunString(script)
	if err == nil {
		t.Error("expected error for nonexistent link name")
	}
}

func TestConfigLinkPathTooFewArguments(t *testing.T) {
	vm := goja.New()

	registry := map[string]string{
		"eslint-base.js": "eslint",
	}

	if err := RegisterConfigLinksInVM(vm, registry, "/repo"); err != nil {
		t.Fatalf("RegisterConfigLinksInVM() error = %v", err)
	}

	script := `tools.Config.linkPath("eslint", "eslint-base.js");`
	_, err := vm.RunString(script)
	if err == nil {
		t.Error("expected error when calling linkPath with too few arguments")
	}
}

func TestConfigLinkPathCoexistsWithOtherTools(t *testing.T) {
	vm := goja.New()

	if err := RegisterIgnoreToolsInVM(vm); err != nil {
		t.Fatalf("RegisterIgnoreToolsInVM() error = %v", err)
	}
	if err := RegisterPathToolsInVM(vm, "/repo"); err != nil {
		t.Fatalf("RegisterPathToolsInVM() error = %v", err)
	}

	registry := map[string]string{
		"eslint-base.js": "eslint",
	}
	if err := RegisterConfigLinksInVM(vm, registry, "/repo"); err != nil {
		t.Fatalf("RegisterConfigLinksInVM() error = %v", err)
	}

	// Verify all three tools coexist
	toolsObj := vm.Get("tools").ToObject(vm)

	ignoreVal := toolsObj.Get("Ignore")
	if ignoreVal == nil || goja.IsUndefined(ignoreVal) {
		t.Error("tools.Ignore was overwritten")
	}

	pathVal := toolsObj.Get("Path")
	if pathVal == nil || goja.IsUndefined(pathVal) {
		t.Error("tools.Path was overwritten")
	}

	configVal := toolsObj.Get("Config")
	if configVal == nil || goja.IsUndefined(configVal) {
		t.Error("tools.Config not found")
	}
}

func TestConfigLinkPathEmptyRegistry(t *testing.T) {
	vm := goja.New()

	registry := map[string]string{}

	if err := RegisterConfigLinksInVM(vm, registry, "/repo"); err != nil {
		t.Fatalf("RegisterConfigLinksInVM() error = %v", err)
	}

	script := `tools.Config.linkPath("eslint", "any.js", "/repo");`
	_, err := vm.RunString(script)
	if err == nil {
		t.Error("expected error when registry is empty")
	}
}
