package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dop251/goja"
)

// TestMultiLayerContentEvaluation is an end-to-end integration test that verifies
// the full multi-layer content evaluation pipeline:
//   - Default layer generates .editorconfig content
//   - Auto layer receives default's content as existingContent
//   - LayerMap correctly tracks both layers
func TestMultiLayerContentEvaluation(t *testing.T) {
	vm := goja.New()

	defaultContent := "root = true\n\n[*]\nindent_style = space\nindent_size = 2\n"

	// Layer 1: default config generates .editorconfig
	defaultFn, err := vm.RunString(`(function(context) {
		return "root = true\n\n[*]\nindent_style = space\nindent_size = 2\n";
	})`)
	if err != nil {
		t.Fatalf("failed to create default content function: %v", err)
	}

	// Layer 2: auto config reads existingContent and overrides
	autoFn, err := vm.RunString(`(function(context) {
		if (!context.existingContent) {
			throw new Error("expected existingContent from default layer");
		}
		return "root = true\n\n[*]\nindent_style = space\nindent_size = 4\n";
	})`)
	if err != nil {
		t.Fatalf("failed to create auto content function: %v", err)
	}

	layerMap := make(InitLayerMap)

	// --- Process Layer 1: default ---
	defaultCfg := &Config{
		Init: MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: defaultFn,
			},
		},
	}

	evaluated1 := EvaluateInitContent(defaultCfg, vm, "/project", "/project", layerMap)
	MergeInitLayers(layerMap, "default", evaluated1, defaultCfg.Init)

	// Verify default layer produced expected content
	if evaluated1[".editorconfig"] != defaultContent {
		t.Errorf("default layer: expected %q, got %q", defaultContent, evaluated1[".editorconfig"])
	}

	// --- Process Layer 2: auto ---
	autoCfg := &Config{
		Init: MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: autoFn,
			},
		},
	}

	evaluated2 := EvaluateInitContent(autoCfg, vm, "/project", "/project", layerMap)
	MergeInitLayers(layerMap, "auto", evaluated2, autoCfg.Init)

	// Verify auto layer received default's content as existingContent and produced override
	autoExpected := "root = true\n\n[*]\nindent_style = space\nindent_size = 4\n"
	if evaluated2[".editorconfig"] != autoExpected {
		t.Errorf("auto layer: expected %q, got %q", autoExpected, evaluated2[".editorconfig"])
	}

	// --- Verify layerMap state ---
	history := layerMap[".editorconfig"]
	if history == nil {
		t.Fatal("expected .editorconfig in layerMap")
	}

	if len(history.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(history.Layers))
	}

	if history.Layers[0].LayerName != "default" {
		t.Errorf("first layer: expected 'default', got %q", history.Layers[0].LayerName)
	}
	if history.Layers[1].LayerName != "auto" {
		t.Errorf("second layer: expected 'auto', got %q", history.Layers[1].LayerName)
	}

	if history.Layers[0].GeneratedContent == nil || history.Layers[1].GeneratedContent == nil {
		t.Error("both layers should have generated content")
	}

	// Verify GetLastGeneratedContent returns auto layer's output
	lastContent := GetLastGeneratedContent(history)
	if lastContent == nil || *lastContent != autoExpected {
		t.Errorf("GetLastGeneratedContent: expected %q, got %v", autoExpected, lastContent)
	}
}

// TestMultiLayerContentThrowSkipsEntry verifies that when a layer's content()
// function throws, that entry is skipped and the previous layer's content is preserved.
func TestMultiLayerContentThrowSkipsEntry(t *testing.T) {
	vm := goja.New()

	// Layer 1: default config generates content
	defaultFn, err := vm.RunString(`(function(context) {
		return "root = true\n\n[*]\nindent_style = tab\nindent_size = 4\n";
	})`)
	if err != nil {
		t.Fatalf("failed to create default content function: %v", err)
	}

	// Layer 2: auto config throws an error
	autoFn, err := vm.RunString(`(function(context) {
		throw new Error("upstream content changed");
	})`)
	if err != nil {
		t.Fatalf("failed to create auto content function: %v", err)
	}

	layerMap := make(InitLayerMap)

	// Process default layer
	defaultCfg := &Config{
		Init: MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: defaultFn,
			},
		},
	}
	evaluated1 := EvaluateInitContent(defaultCfg, vm, "/project", "/project", layerMap)
	MergeInitLayers(layerMap, "default", evaluated1, defaultCfg.Init)

	// Process auto layer - content() throws, entry is skipped
	autoCfg := &Config{
		Init: MapOfConfigInit{
			".editorconfig": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: autoFn,
			},
		},
	}
	evaluated2 := EvaluateInitContent(autoCfg, vm, "/project", "/project", layerMap)

	// The auto layer's content() threw, so it should be skipped
	if _, hasEditor := evaluated2[".editorconfig"]; hasEditor {
		t.Error("expected .editorconfig to be skipped when content() throws")
	}

	MergeInitLayers(layerMap, "auto", evaluated2, autoCfg.Init)

	// Layer history should show auto layer as non-content (content() threw)
	history := layerMap[".editorconfig"]
	if len(history.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(history.Layers))
	}
	if history.Layers[0].GeneratedContent == nil {
		t.Error("default layer should have generated content")
	}
	if history.Layers[1].GeneratedContent != nil {
		t.Error("auto layer should NOT have generated content (content() threw)")
	}

	// GetLastGeneratedContent should return default layer's content (the last successful one)
	lastContent := GetLastGeneratedContent(history)
	if lastContent == nil {
		t.Fatal("expected non-nil last content")
	}
	changedDefault := "root = true\n\n[*]\nindent_style = tab\nindent_size = 4\n"
	if *lastContent != changedDefault {
		t.Errorf("expected default's content %q, got %q", changedDefault, *lastContent)
	}
}

// TestMultiLayerChainDefaultRemoteAutoExplicit verifies a 4-layer config chain:
// default -> remote -> auto -> explicit, with content threading through all layers.
func TestMultiLayerChainDefaultRemoteAutoExplicit(t *testing.T) {
	vm := goja.New()

	layerMap := make(InitLayerMap)

	layerDefs := []struct {
		name    string
		content string
	}{
		{"default", "base"},
		{"remote", ""},  // will read existingContent
		{"auto", ""},    // will read existingContent
		{"explicit", ""}, // will read existingContent
	}

	for i, ld := range layerDefs {
		var fnScript string
		if i == 0 {
			fnScript = `(function(context) { return "base"; })`
		} else {
			fnScript = `(function(context) {
				return context.existingContent ? context.existingContent + " > ` + ld.name + `" : "` + ld.name + ` only";
			})`
		}

		fnVal, err := vm.RunString(fnScript)
		if err != nil {
			t.Fatalf("layer %s: failed to create function: %v", ld.name, err)
		}

		cfg := &Config{
			Init: MapOfConfigInit{
				".editorconfig": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: fnVal,
				},
			},
		}

		evaluated := EvaluateInitContent(cfg, vm, "/root", "/root", layerMap)
		MergeInitLayers(layerMap, ld.name, evaluated, cfg.Init)
	}

	// Verify the content threaded through all 4 layers
	history := layerMap[".editorconfig"]
	if len(history.Layers) != 4 {
		t.Fatalf("expected 4 layers, got %d", len(history.Layers))
	}

	expectedFinal := "base > remote > auto > explicit"
	lastContent := GetLastGeneratedContent(history)
	if lastContent == nil || *lastContent != expectedFinal {
		t.Errorf("expected final content %q, got %v", expectedFinal, lastContent)
	}

	// Verify each layer name is recorded correctly
	expectedNames := []string{"default", "remote", "auto", "explicit"}
	for i, name := range expectedNames {
		if history.Layers[i].LayerName != name {
			t.Errorf("layer %d: expected name %q, got %q", i, name, history.Layers[i].LayerName)
		}
		if history.Layers[i].GeneratedContent == nil {
			t.Errorf("layer %d (%s): should have generated content", i, name)
		}
	}
}

// TestOriginalContentAvailableInAllLayers verifies that originalContent (read from disk)
// is correctly available in each layer's content() function during eager evaluation,
// and that it always contains the unmodified disk content regardless of which layer accesses it.
func TestOriginalContentAvailableInAllLayers(t *testing.T) {
	tmpDir := t.TempDir()

	diskContent := `{"name": "my-app", "version": "1.0.0"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(diskContent), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	vm := goja.New()
	layerMap := make(InitLayerMap)

	// Layer 1 (default): returns originalContent + marker
	defaultFn, err := vm.RunString(`(function(context) {
		if (!context.originalContent) {
			throw new Error("originalContent not available in default layer");
		}
		return context.originalContent + " [default]";
	})`)
	if err != nil {
		t.Fatalf("failed to create default function: %v", err)
	}

	// Layer 2 (auto): verifies originalContent is still the disk content, not modified by default layer
	autoFn, err := vm.RunString(`(function(context) {
		if (!context.originalContent) {
			throw new Error("originalContent not available in auto layer");
		}
		if (context.originalContent !== '{"name": "my-app", "version": "1.0.0"}') {
			throw new Error("originalContent was modified: " + context.originalContent);
		}
		return context.originalContent + " [auto]";
	})`)
	if err != nil {
		t.Fatalf("failed to create auto function: %v", err)
	}

	// Layer 3 (explicit): also checks originalContent is unchanged
	explicitFn, err := vm.RunString(`(function(context) {
		if (!context.originalContent) {
			throw new Error("originalContent not available in explicit layer");
		}
		if (context.originalContent !== '{"name": "my-app", "version": "1.0.0"}') {
			throw new Error("originalContent was modified in explicit layer: " + context.originalContent);
		}
		return "final content";
	})`)
	if err != nil {
		t.Fatalf("failed to create explicit function: %v", err)
	}

	layers := []struct {
		name string
		fn   goja.Value
	}{
		{"default", defaultFn},
		{"auto", autoFn},
		{"explicit", explicitFn},
	}

	for _, l := range layers {
		cfg := &Config{
			Init: MapOfConfigInit{
				"package.json": ConfigInit{
					Scope:   ScopeGitRoot,
					Content: l.fn,
				},
			},
		}
		evaluated := EvaluateInitContent(cfg, vm, tmpDir, tmpDir, layerMap)
		MergeInitLayers(layerMap, l.name, evaluated, cfg.Init)

		if _, ok := evaluated["package.json"]; !ok {
			t.Fatalf("layer %s: content() was skipped (likely threw an error)", l.name)
		}
	}

	// Verify originalContent in layer map is the disk content
	history := layerMap["package.json"]
	if history == nil {
		t.Fatal("expected package.json in layerMap")
	}
	if history.OriginalContent == nil {
		t.Fatal("OriginalContent should not be nil")
	}
	if *history.OriginalContent != diskContent {
		t.Errorf("OriginalContent: expected %q, got %q", diskContent, *history.OriginalContent)
	}

	// Verify all 3 layers are recorded
	if len(history.Layers) != 3 {
		t.Fatalf("expected 3 layers, got %d", len(history.Layers))
	}

	// Verify final content is from the explicit layer
	lastContent := GetLastGeneratedContent(history)
	if lastContent == nil || *lastContent != "final content" {
		t.Errorf("expected final content %q, got %v", "final content", lastContent)
	}
}

// TestOriginalContentPackageJsonMerge verifies a package.json-style merge scenario:
// the original file has existing fields, and the config layer merges new fields
// while preserving the original user-defined fields.
func TestOriginalContentPackageJsonMerge(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a "package.json" with user-defined fields
	originalPkg := `{"name":"user-app","version":"2.0.0","description":"User app","scripts":{"test":"jest"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(originalPkg), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	vm := goja.New()
	layerMap := make(InitLayerMap)

	// Config layer: merge tool-managed fields into existing package.json
	// Uses originalContent to preserve user fields while adding/overriding tool config
	mergeFn, err := vm.RunString(`(function(context) {
		var original = {};
		if (context.originalContent) {
			original = JSON.parse(context.originalContent);
		}
		// Merge: preserve user fields, add tool-managed scripts
		var result = {
			name: original.name || "unknown",
			version: original.version || "0.0.0",
			description: original.description || "",
			scripts: Object.assign({}, original.scripts || {}, {
				lint: "datamitsu lint",
				fix: "datamitsu fix"
			})
		};
		return JSON.stringify(result);
	})`)
	if err != nil {
		t.Fatalf("failed to create merge function: %v", err)
	}

	cfg := &Config{
		Init: MapOfConfigInit{
			"package.json": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: mergeFn,
			},
		},
	}

	evaluated := EvaluateInitContent(cfg, vm, tmpDir, tmpDir, layerMap)
	MergeInitLayers(layerMap, "default", evaluated, cfg.Init)

	content, ok := evaluated["package.json"]
	if !ok {
		t.Fatal("package.json content not generated")
	}

	// Parse and verify the merged result
	// Should have original user fields plus new tool scripts
	expected := `{"name":"user-app","version":"2.0.0","description":"User app","scripts":{"test":"jest","lint":"datamitsu lint","fix":"datamitsu fix"}}`
	if content != expected {
		t.Errorf("merged content mismatch\nexpected: %s\ngot:      %s", expected, content)
	}

	// Verify originalContent is preserved in layer map
	history := layerMap["package.json"]
	if history.OriginalContent == nil || *history.OriginalContent != originalPkg {
		t.Errorf("OriginalContent not preserved correctly")
	}
}

// TestOriginalContentUndefinedWhenFileDoesNotExist verifies that when the file does not
// exist on disk, originalContent is undefined (not set) in the JS context.
func TestOriginalContentUndefinedWhenFileDoesNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	// No file written - file doesn't exist

	vm := goja.New()
	layerMap := make(InitLayerMap)

	fn, err := vm.RunString(`(function(context) {
		if (typeof context.originalContent !== "undefined") {
			throw new Error("originalContent should be undefined for non-existent file, got: " + context.originalContent);
		}
		return "new file content";
	})`)
	if err != nil {
		t.Fatalf("failed to create function: %v", err)
	}

	cfg := &Config{
		Init: MapOfConfigInit{
			"new-config.yml": ConfigInit{
				Scope:   ScopeGitRoot,
				Content: fn,
			},
		},
	}

	evaluated := EvaluateInitContent(cfg, vm, tmpDir, tmpDir, layerMap)
	if _, ok := evaluated["new-config.yml"]; !ok {
		t.Fatal("content() was skipped (likely threw because originalContent was unexpectedly defined)")
	}

	// Verify originalContent is nil in history
	history := layerMap["new-config.yml"]
	if history.OriginalContent != nil {
		t.Errorf("OriginalContent should be nil for non-existent file, got %q", *history.OriginalContent)
	}
}
