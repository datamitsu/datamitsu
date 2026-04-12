package tools

import (
	"fmt"
	"path/filepath"

	"github.com/dop251/goja"
)

func RegisterConfigLinksInVM(vm *goja.Runtime, registry map[string]string, gitRoot string) error {
	toolsObj := vm.Get("tools")
	if toolsObj == nil || goja.IsUndefined(toolsObj) || goja.IsNull(toolsObj) {
		toolsObj = vm.NewObject()
		if err := vm.Set("tools", toolsObj); err != nil {
			return fmt.Errorf("failed to set tools global: %w", err)
		}
	}

	configObj := vm.NewObject()

	if err := configObj.Set("linkPath", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			panic(vm.NewTypeError("linkPath requires 3 arguments: appName, linkName, fromPath"))
		}

		appName := call.Argument(0).String()
		linkName := call.Argument(1).String()
		fromPath := call.Argument(2).String()

		owner, exists := registry[linkName]
		if !exists {
			panic(vm.NewTypeError(fmt.Sprintf("link %q is not registered in any app or bundle", linkName)))
		}
		if owner != appName {
			panic(vm.NewTypeError(fmt.Sprintf("link %q belongs to %q, not %q", linkName, owner, appName)))
		}

		linkAbsPath := filepath.Join(gitRoot, ".datamitsu", linkName)
		relPath, err := filepath.Rel(fromPath, linkAbsPath)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("failed to compute relative path from %q to %q: %w", fromPath, linkAbsPath, err)))
		}

		return vm.ToValue(relPath)
	}); err != nil {
		return fmt.Errorf("failed to set linkPath function: %w", err)
	}

	toolsGoja, ok := toolsObj.(*goja.Object)
	if !ok {
		return fmt.Errorf("tools global is not an object")
	}
	if err := toolsGoja.Set("Config", configObj); err != nil {
		return fmt.Errorf("failed to set tools.Config: %w", err)
	}

	return nil
}
