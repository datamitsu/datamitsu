package config

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/dop251/goja"
)

// readFileContent reads a file from disk and returns its content as a *string.
// Returns nil if the file doesn't exist or cannot be read.
func readFileContent(path string) *string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(data)
	return &content
}

// getPriorLayerContent returns the last generated content for a given filename
// from the layer history. Returns nil if no prior layer generated content.
func getPriorLayerContent(priorLayers InitLayerMap, fileName string) *string {
	history, ok := priorLayers[fileName]
	if !ok {
		return nil
	}
	return GetLastGeneratedContent(history)
}

// MergeInitLayers merges evaluated content from a config layer into the layer map.
// For each init config entry, it appends a layer entry to the history. Entries with
// evaluated content are marked as content layers; entries without (e.g., linkTarget-only)
// are recorded as non-content layers. FinalConfig is always updated to the latest metadata.
func MergeInitLayers(layerMap InitLayerMap, layerName string, evaluatedContent map[string]string, initConfigs MapOfConfigInit) {
	// Process init config entries in sorted order for determinism
	names := make([]string, 0, len(initConfigs))
	for name := range initConfigs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		cfg := initConfigs[name]

		history, ok := layerMap[name]
		if !ok {
			history = &InitLayerHistory{
				FileName: name,
			}
			layerMap[name] = history
		}

		entry := InitLayerEntry{
			LayerName: layerName,
		}

		if content, hasContent := evaluatedContent[name]; hasContent {
			entry.GeneratedContent = &content
		}

		history.Layers = append(history.Layers, entry)
		history.FinalConfig = cfg
	}
}

// EvaluateInitContent evaluates content() functions from a config's Init entries.
// It passes the previous layer's generated content as existingContent in the context.
// Returns a map of filename -> generated content for entries that have content functions.
// Entries with LinkTarget, DeleteOnly, or no Content function are skipped.
// Entries whose content() throws are silently skipped (best-effort evaluation);
// the installer will fall back to generating content at install time for those.
//
// originalContent vs existingContent:
//   - originalContent: the unmodified file content read from disk once (on first layer).
//     Stays constant across all layers so configs can reference what the user had on disk.
//   - existingContent: the output of the previous layer's content() call.
//     Changes with each layer, enabling incremental transformations.
func EvaluateInitContent(cfg *Config, vm *goja.Runtime, rootPath, cwdPath string, priorLayers InitLayerMap) map[string]string {
	if cfg.Init == nil {
		return nil
	}

	result := make(map[string]string)

	// Process in sorted order for determinism
	names := make([]string, 0, len(cfg.Init))
	for name := range cfg.Init {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		initCfg := cfg.Init[name]

		// Read original file content from disk and store in layer map (once per file).
		// This must happen before the skip checks so that originalContent is available
		// even for entries that don't have content functions in this layer.
		// Use rootPath for git-root scoped entries, cwdPath otherwise.
		if _, exists := priorLayers[name]; !exists {
			basePath := cwdPath
			if initCfg.Scope == ScopeGitRoot {
				basePath = rootPath
			}
			originalContent := readFileContent(filepath.Join(basePath, name))
			priorLayers[name] = &InitLayerHistory{
				FileName:        name,
				OriginalContent: originalContent,
			}
		}

		if initCfg.DeleteOnly || initCfg.LinkTarget != "" || initCfg.Content == nil {
			continue
		}

		contentValue, ok := initCfg.Content.(goja.Value)
		if !ok {
			continue
		}

		contentFunc, ok := goja.AssertFunction(contentValue)
		if !ok {
			continue
		}

		contextObj := vm.NewObject()
		_ = contextObj.Set("projectTypes", []string{})
		_ = contextObj.Set("rootPath", rootPath)
		_ = contextObj.Set("cwdPath", cwdPath)
		_ = contextObj.Set("isRoot", rootPath == cwdPath)

		datamitsuAbsDir := filepath.Join(rootPath, ".datamitsu")
		datamitsuRelDir := ".datamitsu"
		if relDir, err := filepath.Rel(cwdPath, datamitsuAbsDir); err == nil {
			datamitsuRelDir = relDir
		}
		_ = contextObj.Set("datamitsuDir", datamitsuRelDir)

		priorContent := getPriorLayerContent(priorLayers, name)
		if priorContent != nil {
			_ = contextObj.Set("existingContent", *priorContent)
		}

		if history, ok := priorLayers[name]; ok && history.OriginalContent != nil {
			_ = contextObj.Set("originalContent", *history.OriginalContent)
		}

		callResult, err := contentFunc(goja.Undefined(), contextObj)
		if err != nil {
			continue
		}

		if callResult == nil || goja.IsUndefined(callResult) || goja.IsNull(callResult) {
			continue
		}

		result[name] = callResult.String()
	}

	return result
}
