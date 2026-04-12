package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dop251/goja"
)

// ========================================
// getPriorLayerContent tests
// ========================================

func TestGetPriorLayerContent(t *testing.T) {
	t.Run("empty priorLayers returns nil", func(t *testing.T) {
		layerMap := make(InitLayerMap)
		result := getPriorLayerContent(layerMap, ".editorconfig")
		if result != nil {
			t.Errorf("expected nil, got %q", *result)
		}
	})

	t.Run("single layer with content", func(t *testing.T) {
		content := "root = true"
		layerMap := InitLayerMap{
			".editorconfig": &InitLayerHistory{
				FileName: ".editorconfig",
				Layers: []InitLayerEntry{
					{LayerName: "default", GeneratedContent: &content},
				},
			},
		}
		result := getPriorLayerContent(layerMap, ".editorconfig")
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if *result != "root = true" {
			t.Errorf("expected 'root = true', got %q", *result)
		}
	})

	t.Run("walks backward through multiple layers", func(t *testing.T) {
		content1 := "first"
		content2 := "second"
		layerMap := InitLayerMap{
			".editorconfig": &InitLayerHistory{
				FileName: ".editorconfig",
				Layers: []InitLayerEntry{
					{LayerName: "default", GeneratedContent: &content1},
					{LayerName: "auto", GeneratedContent: &content2},
				},
			},
		}
		result := getPriorLayerContent(layerMap, ".editorconfig")
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if *result != "second" {
			t.Errorf("expected 'second', got %q", *result)
		}
	})

	t.Run("returns nil for unknown filename", func(t *testing.T) {
		content := "some content"
		layerMap := InitLayerMap{
			".editorconfig": &InitLayerHistory{
				FileName: ".editorconfig",
				Layers: []InitLayerEntry{
					{LayerName: "default", GeneratedContent: &content},
				},
			},
		}
		result := getPriorLayerContent(layerMap, "lefthook.yml")
		if result != nil {
			t.Errorf("expected nil for unknown filename, got %q", *result)
		}
	})

	t.Run("returns nil when layers have no content", func(t *testing.T) {
		layerMap := InitLayerMap{
			".editorconfig": &InitLayerHistory{
				FileName: ".editorconfig",
				Layers: []InitLayerEntry{
					{LayerName: "default", GeneratedContent: nil},
				},
			},
		}
		result := getPriorLayerContent(layerMap, ".editorconfig")
		if result != nil {
			t.Errorf("expected nil, got %q", *result)
		}
	})
}

// ========================================
// mergeInitLayers tests
// ========================================

func TestMergeInitLayers(t *testing.T) {
	t.Run("creates new history entry", func(t *testing.T) {
		layerMap := make(InitLayerMap)
		evaluatedContent := map[string]string{
			".editorconfig": "root = true",
		}
		initConfigs := MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope: ScopeGitRoot,
			},
		}

		MergeInitLayers(layerMap, "default", evaluatedContent, initConfigs)

		history, ok := layerMap[".editorconfig"]
		if !ok {
			t.Fatal("expected .editorconfig in layerMap")
		}
		if history.FileName != ".editorconfig" {
			t.Errorf("expected FileName '.editorconfig', got %q", history.FileName)
		}
		if len(history.Layers) != 1 {
			t.Fatalf("expected 1 layer, got %d", len(history.Layers))
		}
		layer := history.Layers[0]
		if layer.LayerName != "default" {
			t.Errorf("expected LayerName 'default', got %q", layer.LayerName)
		}
		if layer.GeneratedContent == nil || *layer.GeneratedContent != "root = true" {
			t.Errorf("expected content 'root = true', got %v", layer.GeneratedContent)
		}
	})

	t.Run("appends to existing history", func(t *testing.T) {
		content1 := "first"
		layerMap := InitLayerMap{
			".editorconfig": &InitLayerHistory{
				FileName: ".editorconfig",
				Layers: []InitLayerEntry{
					{LayerName: "default", GeneratedContent: &content1},
				},
				FinalConfig: ConfigInit{Scope: ScopeGitRoot},
			},
		}
		evaluatedContent := map[string]string{
			".editorconfig": "second",
		}
		initConfigs := MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:        ScopeGitRoot,
				ProjectTypes: []string{"go"},
			},
		}

		MergeInitLayers(layerMap, "auto", evaluatedContent, initConfigs)

		history := layerMap[".editorconfig"]
		if len(history.Layers) != 2 {
			t.Fatalf("expected 2 layers, got %d", len(history.Layers))
		}
		if history.Layers[1].LayerName != "auto" {
			t.Errorf("expected second layer 'auto', got %q", history.Layers[1].LayerName)
		}
		if *history.Layers[1].GeneratedContent != "second" {
			t.Errorf("expected content 'second', got %q", *history.Layers[1].GeneratedContent)
		}
	})

	t.Run("updates FinalConfig metadata", func(t *testing.T) {
		layerMap := make(InitLayerMap)
		evaluatedContent := map[string]string{
			".editorconfig": "content",
		}
		initConfigs := MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:        ScopeGitRoot,
				ProjectTypes: []string{"go", "typescript"},
			},
		}

		MergeInitLayers(layerMap, "default", evaluatedContent, initConfigs)

		history := layerMap[".editorconfig"]
		if history.FinalConfig.Scope != ScopeGitRoot {
			t.Errorf("expected Scope 'git-root', got %q", history.FinalConfig.Scope)
		}
		if len(history.FinalConfig.ProjectTypes) != 2 {
			t.Errorf("expected 2 projectTypes, got %d", len(history.FinalConfig.ProjectTypes))
		}
	})

	t.Run("preserves OriginalContent when merging layers", func(t *testing.T) {
		originalContent := "original disk content"
		layerMap := InitLayerMap{
			".editorconfig": &InitLayerHistory{
				FileName:        ".editorconfig",
				OriginalContent: &originalContent,
			},
		}
		evaluatedContent := map[string]string{
			".editorconfig": "generated content",
		}
		initConfigs := MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope: ScopeGitRoot,
			},
		}

		MergeInitLayers(layerMap, "default", evaluatedContent, initConfigs)

		history := layerMap[".editorconfig"]
		if history.OriginalContent == nil {
			t.Fatal("expected OriginalContent to be preserved after merge")
		}
		if *history.OriginalContent != "original disk content" {
			t.Errorf("expected 'original disk content', got %q", *history.OriginalContent)
		}
	})

	t.Run("later layers do not overwrite OriginalContent from first layer", func(t *testing.T) {
		originalContent := "first layer original"
		content1 := "layer1 generated"
		layerMap := InitLayerMap{
			".editorconfig": &InitLayerHistory{
				FileName:        ".editorconfig",
				OriginalContent: &originalContent,
				Layers: []InitLayerEntry{
					{LayerName: "default", GeneratedContent: &content1},
				},
				FinalConfig: ConfigInit{Scope: ScopeGitRoot},
			},
		}

		// Second layer merges into existing history
		evaluatedContent := map[string]string{
			".editorconfig": "layer2 generated",
		}
		initConfigs := MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope: ScopeGitRoot,
			},
		}

		MergeInitLayers(layerMap, "auto", evaluatedContent, initConfigs)

		history := layerMap[".editorconfig"]
		if history.OriginalContent == nil {
			t.Fatal("expected OriginalContent to be preserved after second merge")
		}
		if *history.OriginalContent != "first layer original" {
			t.Errorf("expected 'first layer original', got %q", *history.OriginalContent)
		}
		if len(history.Layers) != 2 {
			t.Fatalf("expected 2 layers, got %d", len(history.Layers))
		}
	})

	t.Run("new entry in MergeInitLayers without prior OriginalContent gets nil", func(t *testing.T) {
		layerMap := make(InitLayerMap)
		evaluatedContent := map[string]string{
			"new-file.txt": "new content",
		}
		initConfigs := MapOfConfigInit{
			"new-file.txt": ConfigInit{
				Scope: ScopeGitRoot,
			},
		}

		MergeInitLayers(layerMap, "default", evaluatedContent, initConfigs)

		history := layerMap["new-file.txt"]
		if history == nil {
			t.Fatal("expected new-file.txt in layerMap")
		}
		if history.OriginalContent != nil {
			t.Errorf("expected nil OriginalContent for entry created by MergeInitLayers, got %q", *history.OriginalContent)
		}
	})

	t.Run("adds non-content layer for init entries without evaluated content", func(t *testing.T) {
		layerMap := make(InitLayerMap)
		evaluatedContent := map[string]string{} // no content evaluated
		initConfigs := MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:      ScopeGitRoot,
				LinkTarget: "../some/path",
			},
		}

		MergeInitLayers(layerMap, "default", evaluatedContent, initConfigs)

		history, ok := layerMap[".editorconfig"]
		if !ok {
			t.Fatal("expected .editorconfig in layerMap")
		}
		if len(history.Layers) != 1 {
			t.Fatalf("expected 1 layer, got %d", len(history.Layers))
		}
		if history.Layers[0].GeneratedContent != nil {
			t.Error("expected GeneratedContent to be nil for non-content entry")
		}
	})
}

// ========================================
// evaluateInitContent tests
// ========================================

func TestEvaluateInitContent(t *testing.T) {
	t.Run("no content functions returns empty map", func(t *testing.T) {
		cfg := &Config{
			Init: MapOfConfigInit{
				".editorconfig": ConfigInit{
					Scope:      ScopeGitRoot,
					LinkTarget: "../target",
				},
			},
		}

		vm := goja.New()
		result := EvaluateInitContent(cfg, vm, "/root", "/root", make(InitLayerMap))
		if len(result) != 0 {
			t.Errorf("expected empty map, got %d entries", len(result))
		}
	})

	t.Run("single content function", func(t *testing.T) {
		vm := goja.New()

		// Create a JS function that returns content
		fnVal, err := vm.RunString(`(function(context) { return "root = true"; })`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				".editorconfig": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: fnVal,
				},
			},
		}

		result := EvaluateInitContent(cfg, vm, "/root", "/root", make(InitLayerMap))
		if len(result) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
		if result[".editorconfig"] != "root = true" {
			t.Errorf("expected 'root = true', got %q", result[".editorconfig"])
		}
	})

	t.Run("uses prior layer content as existingContent", func(t *testing.T) {
		vm := goja.New()

		// Create a JS function that reads existingContent and appends to it
		fnVal, err := vm.RunString(`(function(context) {
			if (context.existingContent) {
				return context.existingContent + "\nindent_size = 2";
			}
			return "root = true";
		})`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		priorContent := "root = true"
		priorLayers := InitLayerMap{
			".editorconfig": &InitLayerHistory{
				FileName: ".editorconfig",
				Layers: []InitLayerEntry{
					{LayerName: "default", GeneratedContent: &priorContent},
				},
			},
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				".editorconfig": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: fnVal,
				},
			},
		}

		result := EvaluateInitContent(cfg, vm, "/root", "/root", priorLayers)
		expected := "root = true\nindent_size = 2"
		if result[".editorconfig"] != expected {
			t.Errorf("expected %q, got %q", expected, result[".editorconfig"])
		}
	})

	t.Run("skips entry when content throws", func(t *testing.T) {
		vm := goja.New()

		fnVal, err := vm.RunString(`(function(context) { throw new Error("content generation failed"); })`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				".editorconfig": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: fnVal,
				},
			},
		}

		result := EvaluateInitContent(cfg, vm, "/root", "/root", make(InitLayerMap))
		if len(result) != 0 {
			t.Errorf("expected empty map when content() throws, got %d entries", len(result))
		}
	})

	t.Run("skips deleteOnly entries", func(t *testing.T) {
		vm := goja.New()

		cfg := &Config{
			Init: MapOfConfigInit{
				".editorconfig": ConfigInit{
					Scope:      ScopeGitRoot,
					DeleteOnly: true,
				},
			},
		}

		result := EvaluateInitContent(cfg, vm, "/root", "/root", make(InitLayerMap))
		if len(result) != 0 {
			t.Errorf("expected empty map for deleteOnly, got %d entries", len(result))
		}
	})

	t.Run("skips linkTarget entries", func(t *testing.T) {
		vm := goja.New()

		cfg := &Config{
			Init: MapOfConfigInit{
				".editorconfig": ConfigInit{
					Scope:      ScopeGitRoot,
					LinkTarget: "../target",
				},
			},
		}

		result := EvaluateInitContent(cfg, vm, "/root", "/root", make(InitLayerMap))
		if len(result) != 0 {
			t.Errorf("expected empty map for linkTarget, got %d entries", len(result))
		}
	})

	t.Run("context has correct rootPath and cwdPath", func(t *testing.T) {
		vm := goja.New()

		fnVal, err := vm.RunString(`(function(context) {
			return context.rootPath + ":" + context.cwdPath;
		})`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				"test.txt": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: fnVal,
				},
			},
		}

		result := EvaluateInitContent(cfg, vm, "/project", "/project/sub", make(InitLayerMap))
		if result["test.txt"] != "/project:/project/sub" {
			t.Errorf("expected '/project:/project/sub', got %q", result["test.txt"])
		}
	})

	t.Run("multiple init entries evaluated independently", func(t *testing.T) {
		vm := goja.New()

		fn1, err := vm.RunString(`(function(context) { return "editor content"; })`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}
		fn2, err := vm.RunString(`(function(context) { return "hook content"; })`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				".editorconfig": ConfigInit{Scope: ScopeGitRoot, Content: fn1},
				"lefthook.yml":  ConfigInit{Scope: ScopeGitRoot, Content: fn2},
			},
		}

		result := EvaluateInitContent(cfg, vm, "/root", "/root", make(InitLayerMap))
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
		if result[".editorconfig"] != "editor content" {
			t.Errorf("expected 'editor content', got %q", result[".editorconfig"])
		}
		if result["lefthook.yml"] != "hook content" {
			t.Errorf("expected 'hook content', got %q", result["lefthook.yml"])
		}
	})
}

// ========================================
// InitLayerHistory OriginalContent tests
// ========================================

func TestInitLayerHistoryOriginalContent(t *testing.T) {
	t.Run("can store OriginalContent", func(t *testing.T) {
		content := "existing file content"
		history := &InitLayerHistory{
			FileName:        ".editorconfig",
			OriginalContent: &content,
			Layers:          []InitLayerEntry{},
		}
		if history.OriginalContent == nil {
			t.Fatal("expected OriginalContent to be non-nil")
		}
		if *history.OriginalContent != "existing file content" {
			t.Errorf("expected 'existing file content', got %q", *history.OriginalContent)
		}
	})

	t.Run("nil OriginalContent when file does not exist", func(t *testing.T) {
		history := &InitLayerHistory{
			FileName:        "nonexistent.txt",
			OriginalContent: nil,
			Layers:          []InitLayerEntry{},
		}
		if history.OriginalContent != nil {
			t.Error("expected OriginalContent to be nil for nonexistent file")
		}
	})

	t.Run("OriginalContent does not affect GetLastGeneratedContent", func(t *testing.T) {
		original := "original disk content"
		generated := "generated content"
		history := &InitLayerHistory{
			FileName:        ".editorconfig",
			OriginalContent: &original,
			Layers: []InitLayerEntry{
				{LayerName: "default", GeneratedContent: &generated},
			},
		}
		result := GetLastGeneratedContent(history)
		if result == nil || *result != "generated content" {
			t.Errorf("expected 'generated content', got %v", result)
		}
	})
}

// ========================================
// readFileContent tests
// ========================================

func TestReadFileContent(t *testing.T) {
	t.Run("returns content for existing file", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "test.txt")
		if err := os.WriteFile(filePath, []byte("hello world"), 0o644); err != nil {
			t.Fatal(err)
		}

		result := readFileContent(filePath)
		if result == nil {
			t.Fatal("expected non-nil result for existing file")
		}
		if *result != "hello world" {
			t.Errorf("expected 'hello world', got %q", *result)
		}
	})

	t.Run("returns nil for missing file", func(t *testing.T) {
		result := readFileContent("/nonexistent/path/file.txt")
		if result != nil {
			t.Errorf("expected nil for missing file, got %q", *result)
		}
	})

	t.Run("returns empty string pointer for empty file", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "empty.txt")
		if err := os.WriteFile(filePath, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}

		result := readFileContent(filePath)
		if result == nil {
			t.Fatal("expected non-nil result for empty file")
		}
		if *result != "" {
			t.Errorf("expected empty string, got %q", *result)
		}
	})

	t.Run("returns nil for unreadable file", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "restricted.txt")
		if err := os.WriteFile(filePath, []byte("secret"), 0o000); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(filePath, 0o644) }()

		result := readFileContent(filePath)
		if result != nil {
			t.Errorf("expected nil for unreadable file, got %q", *result)
		}
	})
}

// ========================================
// EvaluateInitContent originalContent storage tests (Task 3)
// ========================================

func TestEvaluateInitContentReadsOriginalContent(t *testing.T) {
	t.Run("reads file from disk and stores in InitLayerMap", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "package.json")
		if err := os.WriteFile(filePath, []byte(`{"name": "my-project"}`), 0o644); err != nil {
			t.Fatal(err)
		}

		vm := goja.New()
		fnVal, err := vm.RunString(`(function(context) { return "generated"; })`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				"package.json": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: fnVal,
				},
			},
		}

		layerMap := make(InitLayerMap)
		EvaluateInitContent(cfg, vm, dir, dir, layerMap)

		history, ok := layerMap["package.json"]
		if !ok {
			t.Fatal("expected package.json in layerMap after EvaluateInitContent")
		}
		if history.OriginalContent == nil {
			t.Fatal("expected OriginalContent to be set")
		}
		if *history.OriginalContent != `{"name": "my-project"}` {
			t.Errorf("expected original content, got %q", *history.OriginalContent)
		}
	})

	t.Run("constructs file path from cwdPath + filename", func(t *testing.T) {
		// Create a nested directory structure
		rootDir := t.TempDir()
		subDir := filepath.Join(rootDir, "packages", "web")
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatal(err)
		}

		// File exists in cwdPath (subDir), not rootPath
		filePath := filepath.Join(subDir, "tsconfig.json")
		if err := os.WriteFile(filePath, []byte(`{"compilerOptions": {}}`), 0o644); err != nil {
			t.Fatal(err)
		}

		vm := goja.New()
		fnVal, err := vm.RunString(`(function(context) { return "generated"; })`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				"tsconfig.json": ConfigInit{
					Scope:   ScopeProject,
					Content: fnVal,
				},
			},
		}

		layerMap := make(InitLayerMap)
		EvaluateInitContent(cfg, vm, rootDir, subDir, layerMap)

		history := layerMap["tsconfig.json"]
		if history == nil {
			t.Fatal("expected tsconfig.json in layerMap")
		}
		if history.OriginalContent == nil {
			t.Fatal("expected OriginalContent to be set from cwdPath")
		}
		if *history.OriginalContent != `{"compilerOptions": {}}` {
			t.Errorf("expected tsconfig content from cwdPath, got %q", *history.OriginalContent)
		}
	})

	t.Run("nil OriginalContent when file does not exist on disk", func(t *testing.T) {
		dir := t.TempDir()

		vm := goja.New()
		fnVal, err := vm.RunString(`(function(context) { return "generated"; })`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				"nonexistent.json": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: fnVal,
				},
			},
		}

		layerMap := make(InitLayerMap)
		EvaluateInitContent(cfg, vm, dir, dir, layerMap)

		history := layerMap["nonexistent.json"]
		if history == nil {
			t.Fatal("expected nonexistent.json in layerMap")
		}
		if history.OriginalContent != nil {
			t.Errorf("expected nil OriginalContent for missing file, got %q", *history.OriginalContent)
		}
	})

	t.Run("does not overwrite OriginalContent on second call", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "config.json")
		if err := os.WriteFile(filePath, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}

		vm := goja.New()
		fnVal, err := vm.RunString(`(function(context) { return "generated"; })`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				"config.json": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: fnVal,
				},
			},
		}

		layerMap := make(InitLayerMap)

		// First call reads original content
		EvaluateInitContent(cfg, vm, dir, dir, layerMap)
		MergeInitLayers(layerMap, "default", map[string]string{"config.json": "generated"}, cfg.Init)

		// Modify the file on disk
		if err := os.WriteFile(filePath, []byte("modified"), 0o644); err != nil {
			t.Fatal(err)
		}

		// Second call should NOT overwrite OriginalContent
		EvaluateInitContent(cfg, vm, dir, dir, layerMap)

		history := layerMap["config.json"]
		if history.OriginalContent == nil {
			t.Fatal("expected OriginalContent to be set")
		}
		if *history.OriginalContent != "original" {
			t.Errorf("expected original content preserved as 'original', got %q", *history.OriginalContent)
		}
	})
}

// ========================================
// EvaluateInitContent originalContent in JS context tests (Task 4)
// ========================================

func TestEvaluateInitContentPassesOriginalContentToJS(t *testing.T) {
	t.Run("originalContent available in JS context object", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "package.json")
		if err := os.WriteFile(filePath, []byte(`{"name": "test"}`), 0o644); err != nil {
			t.Fatal(err)
		}

		vm := goja.New()
		fnVal, err := vm.RunString(`(function(context) {
			if (context.originalContent === undefined) {
				return "MISSING";
			}
			return "HAS_ORIGINAL:" + context.originalContent;
		})`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				"package.json": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: fnVal,
				},
			},
		}

		layerMap := make(InitLayerMap)
		result := EvaluateInitContent(cfg, vm, dir, dir, layerMap)

		expected := `HAS_ORIGINAL:{"name": "test"}`
		if result["package.json"] != expected {
			t.Errorf("expected %q, got %q", expected, result["package.json"])
		}
	})

	t.Run("originalContent contains actual file content", func(t *testing.T) {
		dir := t.TempDir()
		fileContent := `{"dependencies": {"express": "^4.0.0"}, "scripts": {"start": "node index.js"}}`
		filePath := filepath.Join(dir, "package.json")
		if err := os.WriteFile(filePath, []byte(fileContent), 0o644); err != nil {
			t.Fatal(err)
		}

		vm := goja.New()
		fnVal, err := vm.RunString(`(function(context) {
			// Parse original, merge new fields, return combined
			var original = JSON.parse(context.originalContent);
			original.devDependencies = {"eslint": "^8.0.0"};
			return JSON.stringify(original);
		})`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				"package.json": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: fnVal,
				},
			},
		}

		layerMap := make(InitLayerMap)
		result := EvaluateInitContent(cfg, vm, dir, dir, layerMap)

		// Verify the merged result contains both original and new fields
		if result["package.json"] == "" {
			t.Fatal("expected non-empty result")
		}
		// The JS function parses original and adds devDependencies
		// Result should be valid JSON with both original and new fields
		vm2 := goja.New()
		checkVal, err := vm2.RunString(`(function(json) {
			var obj = JSON.parse(json);
			if (!obj.dependencies || !obj.dependencies.express) return "missing original deps";
			if (!obj.scripts || !obj.scripts.start) return "missing original scripts";
			if (!obj.devDependencies || !obj.devDependencies.eslint) return "missing new devDeps";
			return "OK";
		})`)
		if err != nil {
			t.Fatalf("failed to create check function: %v", err)
		}
		checkFn, _ := goja.AssertFunction(checkVal)
		checkResult, err := checkFn(goja.Undefined(), vm2.ToValue(result["package.json"]))
		if err != nil {
			t.Fatalf("check function failed: %v", err)
		}
		if checkResult.String() != "OK" {
			t.Errorf("content merge check failed: %s", checkResult.String())
		}
	})

	t.Run("originalContent is undefined when file does not exist", func(t *testing.T) {
		dir := t.TempDir()

		vm := goja.New()
		fnVal, err := vm.RunString(`(function(context) {
			if (context.originalContent === undefined) {
				return "UNDEFINED";
			}
			return "DEFINED:" + context.originalContent;
		})`)
		if err != nil {
			t.Fatalf("failed to create JS function: %v", err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				"nonexistent.json": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: fnVal,
				},
			},
		}

		layerMap := make(InitLayerMap)
		result := EvaluateInitContent(cfg, vm, dir, dir, layerMap)

		if result["nonexistent.json"] != "UNDEFINED" {
			t.Errorf("expected 'UNDEFINED' when file missing, got %q", result["nonexistent.json"])
		}
	})
}

// ========================================
// Edge case tests (Task 7)
// ========================================

func TestEdgeCaseRemoteConfigOverridesDefault(t *testing.T) {
	vm := goja.New()

	defaultFn, err := vm.RunString(`(function(context) { return "default content"; })`)
	if err != nil {
		t.Fatalf("failed to create JS function: %v", err)
	}

	remoteFn, err := vm.RunString(`(function(context) {
		if (context.existingContent) {
			return "overridden: " + context.existingContent;
		}
		return "remote only";
	})`)
	if err != nil {
		t.Fatalf("failed to create JS function: %v", err)
	}

	// Layer 1: default config provides .editorconfig
	defaultCfg := &Config{
		Init: MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: defaultFn,
			},
		},
	}
	layerMap := make(InitLayerMap)

	evaluated1 := EvaluateInitContent(defaultCfg, vm, "/root", "/root", layerMap)
	MergeInitLayers(layerMap, "default", evaluated1, defaultCfg.Init)

	if evaluated1[".editorconfig"] != "default content" {
		t.Errorf("default layer: expected 'default content', got %q", evaluated1[".editorconfig"])
	}

	// Layer 2: remote config overrides .editorconfig, receives default's content
	remoteCfg := &Config{
		Init: MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: remoteFn,
			},
		},
	}

	evaluated2 := EvaluateInitContent(remoteCfg, vm, "/root", "/root", layerMap)
	MergeInitLayers(layerMap, "remote", evaluated2, remoteCfg.Init)

	expected := "overridden: default content"
	if evaluated2[".editorconfig"] != expected {
		t.Errorf("remote layer: expected %q, got %q", expected, evaluated2[".editorconfig"])
	}

	// Verify layer history
	history := layerMap[".editorconfig"]
	if len(history.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(history.Layers))
	}
	if history.Layers[0].LayerName != "default" {
		t.Errorf("first layer name: expected 'default', got %q", history.Layers[0].LayerName)
	}
	if history.Layers[1].LayerName != "remote" {
		t.Errorf("second layer name: expected 'remote', got %q", history.Layers[1].LayerName)
	}
	lastContent := GetLastGeneratedContent(history)
	if lastContent == nil || *lastContent != expected {
		t.Errorf("GetLastGeneratedContent: expected %q, got %v", expected, lastContent)
	}
}

func TestEdgeCaseInitEntryRemovedInNextLayer(t *testing.T) {
	vm := goja.New()

	fn, err := vm.RunString(`(function(context) { return "generated"; })`)
	if err != nil {
		t.Fatalf("failed to create JS function: %v", err)
	}

	// Layer 1: defines .editorconfig with content
	layer1Cfg := &Config{
		Init: MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: fn,
			},
		},
	}
	layerMap := make(InitLayerMap)

	evaluated1 := EvaluateInitContent(layer1Cfg, vm, "/root", "/root", layerMap)
	MergeInitLayers(layerMap, "default", evaluated1, layer1Cfg.Init)

	// Layer 2: removes .editorconfig (marks as deleteOnly)
	layer2Cfg := &Config{
		Init: MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:      ScopeGitRoot,
				DeleteOnly: true,
			},
		},
	}

	evaluated2 := EvaluateInitContent(layer2Cfg, vm, "/root", "/root", layerMap)
	MergeInitLayers(layerMap, "auto", evaluated2, layer2Cfg.Init)

	// DeleteOnly entry should not produce content
	if len(evaluated2) != 0 {
		t.Errorf("expected no content from deleteOnly layer, got %d entries", len(evaluated2))
	}

	// Layer history should have 2 layers: first with content, second without
	history := layerMap[".editorconfig"]
	if len(history.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(history.Layers))
	}
	if history.Layers[0].GeneratedContent == nil {
		t.Error("first layer should have generated content")
	}
	if history.Layers[1].GeneratedContent != nil {
		t.Error("second layer (deleteOnly) should not have generated content")
	}

	// FinalConfig should reflect the deleteOnly state
	if !history.FinalConfig.DeleteOnly {
		t.Error("FinalConfig should have DeleteOnly=true after layer 2")
	}
}

func TestEdgeCaseContentThrowsDuringEvaluation(t *testing.T) {
	vm := goja.New()

	// A content function that always throws
	throwFn, err := vm.RunString(`(function(context) { throw new Error("cannot generate content"); })`)
	if err != nil {
		t.Fatalf("failed to create JS function: %v", err)
	}

	// A content function that succeeds
	okFn, err := vm.RunString(`(function(context) { return "ok content"; })`)
	if err != nil {
		t.Fatalf("failed to create JS function: %v", err)
	}

	cfg := &Config{
		Init: MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: throwFn,
			},
			"lefthook.yml": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: okFn,
			},
		},
	}

	// EvaluateInitContent should not fail overall - throwing entries are skipped
	result := EvaluateInitContent(cfg, vm, "/root", "/root", make(InitLayerMap))

	// The throwing entry should be skipped, the ok entry should succeed
	if _, hasEditor := result[".editorconfig"]; hasEditor {
		t.Error("throwing entry should be skipped, but .editorconfig was in result")
	}
	if result["lefthook.yml"] != "ok content" {
		t.Errorf("non-throwing entry: expected 'ok content', got %q", result["lefthook.yml"])
	}

	// Merge: the throwing entry won't appear in evaluatedContent,
	// but will still be tracked as a non-content layer via initConfigs
	layerMap := make(InitLayerMap)
	MergeInitLayers(layerMap, "default", result, cfg.Init)

	editorHistory := layerMap[".editorconfig"]
	if editorHistory == nil {
		t.Fatal("expected .editorconfig in layerMap even when content() threw")
	}
	if editorHistory.Layers[0].GeneratedContent != nil {
		t.Error("entry whose content() threw should not have generated content")
	}
}

func TestEdgeCaseScopeFiltering(t *testing.T) {
	vm := goja.New()

	gitRootFn, err := vm.RunString(`(function(context) { return "git-root content"; })`)
	if err != nil {
		t.Fatalf("failed to create JS function: %v", err)
	}

	projectFn, err := vm.RunString(`(function(context) { return "project content"; })`)
	if err != nil {
		t.Fatalf("failed to create JS function: %v", err)
	}

	cfg := &Config{
		Init: MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: gitRootFn,
			},
			"tsconfig.json": ConfigInit{
				Scope:        ScopeProject,
				Content:      projectFn,
				ProjectTypes: []string{"typescript"},
			},
		},
	}

	// EvaluateInitContent evaluates ALL entries regardless of scope.
	// Scope filtering happens at install time, not at evaluation time.
	result := EvaluateInitContent(cfg, vm, "/root", "/root/packages/web", make(InitLayerMap))

	if len(result) != 2 {
		t.Fatalf("expected 2 entries (both scopes evaluated), got %d", len(result))
	}
	if result[".editorconfig"] != "git-root content" {
		t.Errorf("git-root entry: expected 'git-root content', got %q", result[".editorconfig"])
	}
	if result["tsconfig.json"] != "project content" {
		t.Errorf("project entry: expected 'project content', got %q", result["tsconfig.json"])
	}

	// Merge preserves scope in FinalConfig
	layerMap := make(InitLayerMap)
	MergeInitLayers(layerMap, "default", result, cfg.Init)

	if layerMap[".editorconfig"].FinalConfig.Scope != ScopeGitRoot {
		t.Errorf("expected git-root scope, got %q", layerMap[".editorconfig"].FinalConfig.Scope)
	}
	if layerMap["tsconfig.json"].FinalConfig.Scope != ScopeProject {
		t.Errorf("expected project scope, got %q", layerMap["tsconfig.json"].FinalConfig.Scope)
	}
	if len(layerMap["tsconfig.json"].FinalConfig.ProjectTypes) != 1 || layerMap["tsconfig.json"].FinalConfig.ProjectTypes[0] != "typescript" {
		t.Errorf("expected projectTypes [typescript], got %v", layerMap["tsconfig.json"].FinalConfig.ProjectTypes)
	}
}

func TestOriginalContentReadsFromRootPathForGitRootScope(t *testing.T) {
	// When cwdPath != rootPath, git-root scoped entries should read originalContent
	// from rootPath (the git root), not cwdPath (the subdirectory).
	vm := goja.New()

	fn, err := vm.RunString(`(function(context) {
		if (context.originalContent) {
			return "found: " + context.originalContent;
		}
		return "not found";
	})`)
	if err != nil {
		t.Fatalf("failed to create JS function: %v", err)
	}

	// Create a temp directory structure simulating a monorepo
	rootDir := t.TempDir()
	subDir := filepath.Join(rootDir, "packages", "frontend")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	// Write the file at git root (where git-root scoped entries live)
	if err := os.WriteFile(filepath.Join(rootDir, ".editorconfig"), []byte("root content"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	cfg := &Config{
		Init: MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: fn,
			},
		},
	}

	layerMap := make(InitLayerMap)
	// Pass different rootPath and cwdPath to simulate running from subdirectory
	result := EvaluateInitContent(cfg, vm, rootDir, subDir, layerMap)

	expected := "found: root content"
	if result[".editorconfig"] != expected {
		t.Errorf("expected %q, got %q", expected, result[".editorconfig"])
	}

	// Verify originalContent was stored correctly in layer map
	history := layerMap[".editorconfig"]
	if history.OriginalContent == nil {
		t.Fatal("expected OriginalContent to be set")
	}
	if *history.OriginalContent != "root content" {
		t.Errorf("expected OriginalContent 'root content', got %q", *history.OriginalContent)
	}
}
