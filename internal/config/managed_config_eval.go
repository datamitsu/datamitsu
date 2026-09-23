package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/datamitsu/datamitsu/internal/trace"

	"github.com/dop251/goja"
)

// Managed-config evaluation counters. Only `config reconcile` and
// `config chain-hash` consume this pass — the call
// counts are the evidence for how much a lint run reads and evaluates for
// nothing.
var (
	cntManagedConfigEntries = trace.NewCounter("config.managed_configs.entries")
	cntManagedConfigReads   = trace.NewCounter("config.managed_configs.files_read")
	cntManagedConfigCalls   = trace.NewCounter("config.managed_configs.content_calls")
)

// ProjectLocation pairs a detected project type with the directory that holds
// its marker file, expressed relative to the git root ("." for the root). It is
// the shape exposed to managed config content() functions as context.projectLocations[i].
// It is intentionally decoupled from project.ProjectLocation (which carries an
// absolute path) to keep internal/config free of an import cycle on
// internal/project.
type ProjectLocation struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

// ApplyProjectContext sets projectTypes and projectLocations on a managed config
// content() context object so the eager (config-load) and install paths expose
// an identical shape. projectLocations is rendered as plain {type, path}
// objects to keep stable JS keys regardless of goja field-name mapping.
//
// projectTypes/projectLocations are inputs to managed config content() evaluation only.
// A load that evaluates managed config content never uses the config-evaluation cache
// (configCacheUsable in cmd/config_cache.go), so they are deliberately absent
// from configcache.Inputs. If managed config evaluation ever gains its own cache, they
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
func getPriorLayerContent(priorLayers ManagedConfigLayerMap, fileName string) *string {
	history, ok := priorLayers[fileName]
	if !ok {
		return nil
	}
	return GetLastGeneratedContent(history)
}

// MergeManagedConfigLayers merges evaluated content from a config layer into the layer map.
// For each managed config entry, it appends a layer entry to the history. Entries with
// evaluated content are marked as content layers; entries without (e.g., linkTarget-only)
// are recorded as non-content layers. FinalConfig is always updated to the latest metadata.
func MergeManagedConfigLayers(layerMap ManagedConfigLayerMap, layerName string, evaluatedContent map[string]string, managedConfigs MapOfManagedConfigs) {
	// Process managed config entries in sorted order for determinism.
	names := make([]string, 0, len(managedConfigs))
	for name := range managedConfigs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		cfg := managedConfigs[name]

		history, ok := layerMap[name]
		if !ok {
			history = &ManagedConfigLayerHistory{
				FileName: name,
			}
			layerMap[name] = history
		}

		entry := ManagedConfigLayerEntry{
			LayerName: layerName,
		}

		if content, hasContent := evaluatedContent[name]; hasContent {
			entry.GeneratedContent = &content
		}

		history.Layers = append(history.Layers, entry)
		history.FinalConfig = cfg
	}
}

// EvaluateManagedConfigContent evaluates content() functions from managedConfigs entries.
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
func EvaluateManagedConfigContent(cfg *Config, vm *goja.Runtime, rootPath, cwdPath string, priorLayers ManagedConfigLayerMap) map[string]string {
	return EvaluateManagedConfigContentWithProjects(cfg, vm, rootPath, cwdPath, priorLayers, nil, nil)
}

// EvaluateManagedConfigContentWithProjects is EvaluateManagedConfigContent with the detected
// project context (types + git-root-relative locations) exposed to content() as
// context.projectTypes / context.projectLocations. EvaluateManagedConfigContent passes
// nil for both, preserving the prior empty-context behavior for callers that do
// not run project detection.
func EvaluateManagedConfigContentWithProjects(cfg *Config, vm *goja.Runtime, rootPath, cwdPath string, priorLayers ManagedConfigLayerMap, projectTypes []string, projectLocations []ProjectLocation) map[string]string {
	if cfg.ManagedConfigs == nil {
		return nil
	}

	evalSpan := trace.Start(trace.CatConfig, "managed_config.evaluate")
	defer func() { evalSpan.EndWith(trace.A("entries", len(cfg.ManagedConfigs))) }()
	cntManagedConfigEntries.Add(int64(len(cfg.ManagedConfigs)))

	result := make(map[string]string)

	// Process in sorted order for determinism
	names := make([]string, 0, len(cfg.ManagedConfigs))
	for name := range cfg.ManagedConfigs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		managedCfg := cfg.ManagedConfigs[name]

		// Read original file content from disk and store in layer map (once per file).
		// This must happen before the skip checks so that originalContent is available
		// even for entries that don't have content functions in this layer.
		// Use rootPath for git-root scoped entries, cwdPath otherwise.
		if _, exists := priorLayers[name]; !exists {
			basePath := cwdPath
			if managedCfg.Scope == ScopeGitRoot {
				basePath = rootPath
			}
			cntManagedConfigReads.Add(1)
			originalContent := readFileContent(filepath.Join(basePath, name))
			priorLayers[name] = &ManagedConfigLayerHistory{
				FileName:        name,
				OriginalContent: originalContent,
			}
		}

		if managedCfg.DeleteOnly || managedCfg.LinkTarget != "" || managedCfg.Content == nil {
			continue
		}

		contentValue, ok := managedCfg.Content.(goja.Value)
		if !ok {
			continue
		}

		contentFunc, ok := goja.AssertFunction(contentValue)
		if !ok {
			continue
		}

		basePath := cwdPath
		if managedCfg.Scope == ScopeGitRoot {
			basePath = rootPath
		}
		cc := ContentContext{
			RootPath:         rootPath,
			CwdPath:          cwdPath,
			ProjectTypes:     projectTypes,
			ProjectLocations: projectLocations,
			Placement:        PlacementRepo,
			OutputPath:       filepath.Join(basePath, name),
			ExistingContent:  getPriorLayerContent(priorLayers, name),
		}
		if managedCfg.Ejectable {
			cc.ProjectTypes, cc.ProjectLocations = nil, nil
		}
		if history, ok := priorLayers[name]; ok {
			cc.OriginalContent = history.OriginalContent
		}
		contextObj := NewContentContextObject(vm, cc)

		cntManagedConfigCalls.Add(1)
		callResult, err := contentFunc(goja.Undefined(), contextObj)
		if err != nil {
			priorLayers[name].RenderFailed = true
			continue
		}

		if callResult == nil || goja.IsUndefined(callResult) || goja.IsNull(callResult) {
			continue
		}

		result[name] = callResult.String()
	}

	return result
}

// ContentContext is the input of one content() call. OutputPath is the
// absolute path the result is written to; it differs from CwdPath/name only
// for an internal-placement render.
type ContentContext struct {
	RootPath         string
	CwdPath          string
	ProjectTypes     []string
	ProjectLocations []ProjectLocation
	Placement        ManagedConfigPlacement
	OutputPath       string
	ExistingContent  *string
	OriginalContent  *string
	ExistingPath     *string
}

// NewContentContextObject builds the object a content() function receives.
//
// Two path conventions meet here. datamitsuDir is relative to cwdPath, which is
// where a tool runs and what a CWD-relative reference (gitleaks' extend.path)
// resolves against. datamitsuDirFromOutput is relative to the file's own
// directory, which is what a relative import inside the file resolves against.
// They coincide for a file written into the repository and diverge for one
// rendered into .datamitsu/configs/.
func NewContentContextObject(vm *goja.Runtime, c ContentContext) *goja.Object {
	obj := vm.NewObject()
	ApplyProjectContext(obj, c.ProjectTypes, c.ProjectLocations)
	_ = obj.Set("rootPath", c.RootPath)
	_ = obj.Set("cwdPath", c.CwdPath)
	_ = obj.Set("isRoot", c.RootPath == c.CwdPath)

	datamitsuAbsDir := filepath.Join(c.RootPath, DatamitsuDirName)
	_ = obj.Set("datamitsuDir", relOr(c.CwdPath, datamitsuAbsDir, DatamitsuDirName))

	placement := c.Placement
	if placement == "" {
		placement = PlacementRepo
	}
	_ = obj.Set("placement", string(placement))
	if c.OutputPath != "" {
		outputDir := filepath.Dir(c.OutputPath)
		_ = obj.Set("outputPath", c.OutputPath)
		_ = obj.Set("outputDir", outputDir)
		_ = obj.Set("datamitsuDirFromOutput", relOr(outputDir, datamitsuAbsDir, DatamitsuDirName))
	}

	if c.ExistingContent != nil {
		_ = obj.Set("existingContent", *c.ExistingContent)
	}
	if c.OriginalContent != nil {
		_ = obj.Set("originalContent", *c.OriginalContent)
	}
	if c.ExistingPath != nil {
		_ = obj.Set("existingPath", *c.ExistingPath)
	}
	return obj
}

func relOr(base, target, fallback string) string {
	if rel, err := filepath.Rel(base, target); err == nil {
		return rel
	}
	return fallback
}

// ManagedConfigLayer is one evaluated config layer's managed configs, with the
// VM that created the context objects its content functions receive.
type ManagedConfigLayer struct {
	Name    string
	Configs MapOfManagedConfigs
	VM      *goja.Runtime
}

// RenderManagedConfigFromScratch replays one git-root entry's content functions
// through the ordered layers the way the eager pass does — an inherited
// function runs again in every later layer, fed what the layers before it
// produced — but for the given placement and as if no file existed: there is
// no originalContent. It returns nil when no layer produced content.
//
// A throwing content() is an error here rather than a skipped layer, because
// the result decides what is written and what is deleted.
func RenderManagedConfigFromScratch(layers []ManagedConfigLayer, key, rootPath string, placement ManagedConfigPlacement) (*string, error) {
	outputPath := filepath.Join(rootPath, filepath.FromSlash(key))
	if placement == PlacementInternal {
		outputPath = filepath.Join(rootPath, filepath.FromSlash(InternalConfigRelPath(key)))
	}

	var prior *string
	for _, layer := range layers {
		mc, ok := layer.Configs[key]
		if !ok || mc.DeleteOnly || mc.LinkTarget != "" || mc.Content == nil {
			continue
		}
		contentValue, ok := mc.Content.(goja.Value)
		if !ok {
			continue
		}
		contentFunc, ok := goja.AssertFunction(contentValue)
		if !ok {
			continue
		}

		contextObj := NewContentContextObject(layer.VM, ContentContext{
			RootPath:        rootPath,
			CwdPath:         rootPath,
			Placement:       placement,
			OutputPath:      outputPath,
			ExistingContent: prior,
		})
		cntManagedConfigCalls.Add(1)
		result, err := contentFunc(goja.Undefined(), contextObj)
		if err != nil {
			return nil, fmt.Errorf("managed config %q: content() failed in %s: %w", key, layer.Name, err)
		}
		if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
			continue
		}
		rendered := result.String()
		prior = &rendered
	}
	return prior, nil
}
