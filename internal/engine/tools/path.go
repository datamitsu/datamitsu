package tools

import (
	"path/filepath"

	"github.com/dop251/goja"
)

func RegisterPathToolsInVM(vm *goja.Runtime, rootPath string) error {
	toolsObj := vm.Get("tools")
	if toolsObj == nil || goja.IsUndefined(toolsObj) || goja.IsNull(toolsObj) {
		toolsObj = vm.NewObject()
		_ = vm.Set("tools", toolsObj)
	}

	pathObj := vm.NewObject()

	// Join paths
	_ = pathObj.Set("join", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}

		paths := make([]string, len(call.Arguments))
		for i, arg := range call.Arguments {
			paths[i] = arg.String()
		}

		result := filepath.Join(paths...)
		return vm.ToValue(result)
	})

	// Get absolute path
	_ = pathObj.Set("abs", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.NewTypeError("abs requires 1 argument: path"))
		}

		path := call.Argument(0).String()
		absPath, err := filepath.Abs(path)
		if err != nil {
			panic(vm.NewGoError(err))
		}

		return vm.ToValue(absPath)
	})

	// Get relative path (relative to rootPath)
	_ = pathObj.Set("rel", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.NewTypeError("rel requires 1 argument: path"))
		}

		targetPath := call.Argument(0).String()

		// If a base path is provided as second argument, use it; otherwise use rootPath
		basePath := rootPath
		if len(call.Arguments) >= 2 && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
			basePath = call.Argument(1).String()
		}

		relPath, err := filepath.Rel(basePath, targetPath)
		if err != nil {
			panic(vm.NewGoError(err))
		}

		return vm.ToValue(relPath)
	})

	// Convert relative path to import-compatible format (ensures ./ or ../ prefix)
	_ = pathObj.Set("forImport", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.NewTypeError("forImport requires 1 argument: path"))
		}

		path := call.Argument(0).String()
		cleaned := filepath.ToSlash(filepath.Clean(path))

		if cleaned == "." {
			return vm.ToValue("./")
		}

		// filepath.IsAbs misses rooted-but-volumeless paths on Windows (e.g. "/foo"),
		// so also reject any path starting with "/" after ToSlash normalization.
		if filepath.IsAbs(cleaned) || cleaned[0] == '/' {
			panic(vm.NewTypeError("forImport requires a relative path, got absolute path: " + cleaned))
		}

		if (len(cleaned) >= 3 && cleaned[0:3] == "../") || cleaned == ".." {
			return vm.ToValue(cleaned)
		}

		return vm.ToValue("./" + cleaned)
	})

	_ = toolsObj.(*goja.Object).Set("Path", pathObj)

	return nil
}
