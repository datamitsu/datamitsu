package config

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/datamitsu/datamitsu/internal/trace"

	"github.com/dop251/goja"
)

// Setup-evaluation counters. Every command that loads config pays this pass,
// but only setup/init and `config chain-hash` consume the result — the call
// counts are the evidence for how much a lint run reads and evaluates for
// nothing.
var (
	cntSetupEntries = trace.NewCounter("config.setup.entries")
	cntSetupReads   = trace.NewCounter("config.setup.files_read")
	cntSetupCalls   = trace.NewCounter("config.setup.content_calls")
)

// ProjectLocation pairs a detected project type with the directory that holds
// its marker file, expressed relative to the git root ("." for the root). It is
// the shape exposed to setup content() functions as context.projectLocations[i].
// It is intentionally decoupled from project.ProjectLocation (which carries an
// absolute path) to keep internal/config free of an import cycle on
// internal/project.
type ProjectLocation struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

// ApplyProjectContext sets projectTypes and projectLocations on a setup
// content() context object so the eager (config-load) and install paths expose
// an identical shape. projectLocations is rendered as plain {type, path}
// objects to keep stable JS keys regardless of goja field-name mapping.
//
// projectTypes/projectLocations are inputs to setup content() evaluation only.
// A load that evaluates setup content never uses the config-evaluation cache
// (configCacheUsable in cmd/config_cache.go), so they are deliberately absent
// from configcache.Inputs. If the setup layer ever gains its own cache, they
// MUST be folded into its key.
func ApplyProjectContext(obj *goja.Object, projectTypes []string, locations []ProjectLocation) {
	if projectTypes == nil {
		projectTypes = []string{}
	}
	_ = obj.Set("projectTypes", projectTypes)

	locs := make([]map[string]string, 0, len(locations))
	for _, l := range locations {
		locs = append(locs, map[string]string{"type": l.Type, "path": l.Path})
	}
	_ = obj.Set("projectLocations", locs)
}

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
func getPriorLayerContent(priorLayers SetupLayerMap, fileName string) *string {
	history, ok := priorLayers[fileName]
	if !ok {
		return nil
	}
	return GetLastGeneratedContent(history)
}

// MergeSetupLayers merges evaluated content from a config layer into the layer map.
// For each init config entry, it appends a layer entry to the history. Entries with
// evaluated content are marked as content layers; entries without (e.g., linkTarget-only)
// are recorded as non-content layers. FinalConfig is always updated to the latest metadata.
func MergeSetupLayers(layerMap SetupLayerMap, layerName string, evaluatedContent map[string]string, initConfigs MapOfConfigSetup) {
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
			history = &SetupLayerHistory{
				FileName: name,
			}
			layerMap[name] = history
		}

		entry := SetupLayerEntry{
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
func EvaluateInitContent(cfg *Config, vm *goja.Runtime, rootPath, cwdPath string, priorLayers SetupLayerMap) map[string]string {
	return EvaluateInitContentWithProjects(cfg, vm, rootPath, cwdPath, priorLayers, nil, nil)
}

// EvaluateInitContentWithProjects is EvaluateInitContent with the detected
// project context (types + git-root-relative locations) exposed to content() as
// context.projectTypes / context.projectLocations. EvaluateInitContent passes
// nil for both, preserving the prior empty-context behavior for callers that do
// not run project detection.
func EvaluateInitContentWithProjects(cfg *Config, vm *goja.Runtime, rootPath, cwdPath string, priorLayers SetupLayerMap, projectTypes []string, projectLocations []ProjectLocation) map[string]string {
	if cfg.Setup == nil {
		return nil
	}

	evalSpan := trace.Start(trace.CatConfig, "setup.evaluate")
	defer func() { evalSpan.EndWith(trace.A("entries", len(cfg.Setup))) }()
	cntSetupEntries.Add(int64(len(cfg.Setup)))

	result := make(map[string]string)

	// Process in sorted order for determinism
	names := make([]string, 0, len(cfg.Setup))
	for name := range cfg.Setup {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		initCfg := cfg.Setup[name]

		// Read original file content from disk and store in layer map (once per file).
		// This must happen before the skip checks so that originalContent is available
		// even for entries that don't have content functions in this layer.
		// Use rootPath for git-root scoped entries, cwdPath otherwise.
		if _, exists := priorLayers[name]; !exists {
			basePath := cwdPath
			if initCfg.Scope == ScopeGitRoot {
				basePath = rootPath
			}
			cntSetupReads.Add(1)
			originalContent := readFileContent(filepath.Join(basePath, name))
			priorLayers[name] = &SetupLayerHistory{
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
		ApplyProjectContext(contextObj, projectTypes, projectLocations)
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

		cntSetupCalls.Add(1)
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
