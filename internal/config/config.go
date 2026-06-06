// Package config defines the datamitsu configuration schema (apps, runtimes,
// tools, init commands) and loads, type-strips and validates the JavaScript
// config that produces it.
package config

import (
	_ "embed"
	"fmt"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/logger"

	"github.com/evanw/esbuild/pkg/api"
	"go.uber.org/zap"
)

//go:embed config.js
var defaultConfig string

//go:embed config.d.ts
var defaultConfigDTS string

// ========================================
// Project Type Detection
// ========================================

// ProjectType describes how a project type is detected via marker files.
type ProjectType struct {
	Markers     []string `json:"markers"`
	Description string   `json:"description,omitempty"`
}

// MapOfProjectTypes maps a project-type name to its detection definition.
type MapOfProjectTypes map[string]ProjectType

// ========================================
// Tool Execution Configuration
// ========================================

// ToolScope controls how a tool operation is batched across the file set.
type ToolScope string

// Tool operation scopes: run once per repository, once per project, or per file.
const (
	ToolScopeRepository ToolScope = "repository"
	ToolScopePerProject ToolScope = "per-project"
	ToolScopePerFile    ToolScope = "per-file"
)

// OperationType distinguishes the kind of work a tool operation performs.
type OperationType string

// Tool operation types: applying fixes or only reporting lint findings.
const (
	OpFix  OperationType = "fix"
	OpLint OperationType = "lint"
)

// ToolOperation describes a single fix or lint invocation of a tool.
type ToolOperation struct {
	App          string            `json:"app"`
	Args         []string          `json:"args"`
	Scope        ToolScope         `json:"scope"`
	Batch        *bool             `json:"batch,omitempty"` // Batch mode (default: true for per-project and repository, false for per-file)
	Globs        []string          `json:"globs,omitempty"`
	ExcludeGlobs []string          `json:"excludeGlobs,omitempty"`
	Priority     int               `json:"priority,omitempty"`
	Cache        *bool             `json:"cache,omitempty"`        // Enable caching (default: true)
	InvalidateOn []string          `json:"invalidateOn,omitempty"` // Config files that invalidate cache
	Env          map[string]string `json:"env,omitempty"`          // Extra environment variables for this operation
}

// Tool groups the fix and lint operations of a single development tool.
type Tool struct {
	Name         string                          `json:"name"`
	ProjectTypes []string                        `json:"projectTypes,omitempty"`
	Operations   map[OperationType]ToolOperation `json:"operations"`
}

// MapOfTools maps a tool name to its configuration.
type MapOfTools map[string]Tool

// ========================================
// Init Commands
// ========================================

// InitCommand describes a command run during project initialization.
type InitCommand struct {
	Command      string   `json:"command"`
	Args         []string `json:"args"`
	ProjectTypes []string `json:"projectTypes,omitempty"`
	Description  string   `json:"description,omitempty"`
	When         string   `json:"when,omitempty"`
}

// MapOfInitCommands maps an init-command name to its definition.
type MapOfInitCommands map[string]InitCommand

// ========================================
// Config File Management (ENHANCED)
// ========================================

// ConfigContext is passed to JS content functions when generating a config
// file, describing the target project and any pre-existing file.
type ConfigContext struct { //nolint:revive // exported: name kept explicit; config.ConfigContext reads clearer than the bare config.Context
	ProjectTypes    []string `json:"projectTypes"`
	RootPath        string   `json:"rootPath"`
	CwdPath         string   `json:"cwdPath"`
	IsRoot          bool     `json:"isRoot"`
	ExistingContent *string  `json:"existingContent,omitempty"`
	ExistingPath    *string  `json:"existingPath,omitempty"`
}

// Config-file generation scopes: per project directory or at the git root.
const (
	ScopeProject = "project"
	ScopeGitRoot = "git-root"
)

// ConfigInit describes how a managed config file is generated or linked.
type ConfigInit struct { //nolint:revive // exported: name kept explicit; config.ConfigInit reads clearer than the bare config.Init
	ProjectTypes      []string `json:"projectTypes,omitempty"`
	Scope             string   `json:"scope,omitempty"`
	OtherFileNameList []string `json:"otherFileNameList,omitempty"`
	DeleteOnly        bool     `json:"deleteOnly,omitempty"`
	LinkTarget        string   `json:"linkTarget,omitempty"`
	// Content function will be called from JavaScript
	Content any `json:"-"`
}

// MapOfConfigInit maps a config-file name to its generation definition.
type MapOfConfigInit map[string]ConfigInit

// ========================================
// Runtime Configuration
// ========================================

// RuntimeMode selects whether a runtime is datamitsu-managed or system-provided.
type RuntimeMode string

// Runtime modes: managed (downloaded by datamitsu) or system (already installed).
const (
	RuntimeModeManaged RuntimeMode = "managed"
	RuntimeModeSystem  RuntimeMode = "system"
)

// RuntimeKind identifies the language/toolchain family of a runtime.
type RuntimeKind string

// Supported runtime kinds.
const (
	RuntimeKindUV   RuntimeKind = "uv"
	RuntimeKindNode RuntimeKind = "node"
	RuntimeKindJVM  RuntimeKind = "jvm"
	RuntimeKindGo   RuntimeKind = "go"
)

// RuntimeConfigManaged holds the managed-mode binaries for a runtime.
type RuntimeConfigManaged struct {
	Binaries binmanager.MapOfBinaries `json:"binaries"`
}

// RuntimeConfigSystem points at a system-installed runtime command.
type RuntimeConfigSystem struct {
	Command       string `json:"command"`
	SystemVersion string `json:"systemVersion,omitempty"`
}

// RuntimeConfigNode holds node-specific config for the archive-based node
// runtime (kind "node"). Node is fetched as a direct archive (url + hash),
// jvm-style.
type RuntimeConfigNode struct {
	NodeVersion string `json:"nodeVersion"`
	PNPMVersion string `json:"pnpmVersion"`
	PNPMHash    string `json:"pnpmHash"`
}

// RuntimeConfigUV holds uv/Python-specific runtime config.
type RuntimeConfigUV struct {
	PythonVersion string `json:"pythonVersion,omitempty"`
}

// RuntimeConfigJVM holds JVM-specific runtime config.
type RuntimeConfigJVM struct {
	JavaVersion string `json:"javaVersion"`
}

// RuntimeConfigGo holds Go-toolchain-specific runtime config.
type RuntimeConfigGo struct {
	GoVersion string `json:"goVersion"`
}

// RuntimeConfig is a single runtime entry: its kind, mode and kind-specific sub-config.
type RuntimeConfig struct {
	Kind    RuntimeKind           `json:"kind"`
	Mode    RuntimeMode           `json:"mode"`
	Managed *RuntimeConfigManaged `json:"managed,omitempty"`
	System  *RuntimeConfigSystem  `json:"system,omitempty"`
	Node    *RuntimeConfigNode    `json:"node,omitempty"`
	UV      *RuntimeConfigUV      `json:"uv,omitempty"`
	JVM     *RuntimeConfigJVM     `json:"jvm,omitempty"`
	Go      *RuntimeConfigGo      `json:"go,omitempty"`
}

// MapOfRuntimes maps a runtime name to its configuration.
type MapOfRuntimes map[string]RuntimeConfig

// ========================================
// Main Config (ENHANCED)
// ========================================

// Config is the fully resolved datamitsu configuration produced by the JS config layer.
type Config struct {
	Apps          binmanager.MapOfApps    `json:"apps,omitempty"`
	Bundles       binmanager.MapOfBundles `json:"bundles,omitempty"`
	Runtimes      MapOfRuntimes           `json:"runtimes,omitempty"`
	Init          MapOfConfigInit         `json:"init,omitempty"`
	ProjectTypes  MapOfProjectTypes       `json:"projectTypes,omitempty"`
	Tools         MapOfTools              `json:"tools,omitempty"`
	InitCommands  MapOfInitCommands       `json:"initCommands,omitempty"`
	IgnoreRules   []string                `json:"ignoreRules,omitempty"`
	SharedStorage map[string]string       `json:"sharedStorage,omitempty"`
}

// GetDefaultConfig returns the embedded default config JS with TypeScript types stripped.
func GetDefaultConfig() (string, error) {
	return StripTypes(defaultConfig)
}

// GetDefaultConfigDTS returns the embedded TypeScript declarations for the config.
func GetDefaultConfigDTS() string {
	return defaultConfigDTS
}

// StripTypes transpiles TypeScript config source to plain JavaScript via esbuild.
func StripTypes(tsCode string) (string, error) {
	t0 := time.Now()
	result := api.Transform(tsCode, api.TransformOptions{
		Loader: api.LoaderTS,
	})
	logger.Logger.Debug("esbuild StripTypes", zap.Duration("elapsed", time.Since(t0)))

	if len(result.Errors) > 0 {
		return "", fmt.Errorf("transform error: %s", result.Errors[0].Text)
	}

	return string(result.Code), nil
}
