package config

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/logger"
	_ "embed"
	"fmt"
	"time"

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

type ProjectType struct {
	Markers     []string `json:"markers"`
	Description string   `json:"description,omitempty"`
}

type MapOfProjectTypes map[string]ProjectType

// ========================================
// Tool Execution Configuration
// ========================================

type ToolScope string

const (
	ToolScopeRepository ToolScope = "repository"
	ToolScopePerProject ToolScope = "per-project"
	ToolScopePerFile    ToolScope = "per-file"
)

type OperationType string

const (
	OpFix  OperationType = "fix"
	OpLint OperationType = "lint"
)

type ToolOperation struct {
	App          string            `json:"app"`
	Args         []string          `json:"args"`
	Scope        ToolScope         `json:"scope"`
	Batch        *bool             `json:"batch,omitempty"`        // Batch mode (default: true for per-project and repository, false for per-file)
	Globs        []string          `json:"globs"`
	Priority     int               `json:"priority,omitempty"`
	Cache        *bool             `json:"cache,omitempty"`        // Enable caching (default: true)
	InvalidateOn []string          `json:"invalidateOn,omitempty"` // Config files that invalidate cache
	Env          map[string]string `json:"env,omitempty"`          // Extra environment variables for this operation
}

type Tool struct {
	Name         string                          `json:"name"`
	ProjectTypes []string                        `json:"projectTypes,omitempty"`
	Operations   map[OperationType]ToolOperation `json:"operations"`
}

type MapOfTools map[string]Tool

// ========================================
// Init Commands
// ========================================

type InitCommand struct {
	Command      string   `json:"command"`
	Args         []string `json:"args"`
	ProjectTypes []string `json:"projectTypes,omitempty"`
	Description  string   `json:"description,omitempty"`
	When         string   `json:"when,omitempty"`
}

type MapOfInitCommands map[string]InitCommand

// ========================================
// Config File Management (ENHANCED)
// ========================================

type ConfigContext struct {
	ProjectTypes    []string `json:"projectTypes"`
	RootPath        string   `json:"rootPath"`
	CwdPath         string   `json:"cwdPath"`
	IsRoot          bool     `json:"isRoot"`
	ExistingContent *string  `json:"existingContent,omitempty"`
	ExistingPath    *string  `json:"existingPath,omitempty"`
}

const (
	ScopeProject = "project"
	ScopeGitRoot = "git-root"
)

type ConfigInit struct {
	ProjectTypes      []string `json:"projectTypes,omitempty"`
	Scope             string   `json:"scope,omitempty"`
	OtherFileNameList []string `json:"otherFileNameList,omitempty"`
	DeleteOnly        bool     `json:"deleteOnly,omitempty"`
	LinkTarget        string   `json:"linkTarget,omitempty"`
	// Content function will be called from JavaScript
	Content interface{} `json:"-"`
}

type MapOfConfigInit map[string]ConfigInit

// ========================================
// Runtime Configuration
// ========================================

type RuntimeMode string

const (
	RuntimeModeManaged RuntimeMode = "managed"
	RuntimeModeSystem  RuntimeMode = "system"
)

type RuntimeKind string

const (
	RuntimeKindUV  RuntimeKind = "uv"
	RuntimeKindFNM RuntimeKind = "fnm"
	RuntimeKindJVM RuntimeKind = "jvm"
)

type RuntimeConfigManaged struct {
	Binaries binmanager.MapOfBinaries `json:"binaries"`
}

type RuntimeConfigSystem struct {
	Command       string `json:"command"`
	SystemVersion string `json:"systemVersion,omitempty"`
}

type RuntimeConfigFNM struct {
	NodeVersion string `json:"nodeVersion"`
	PNPMVersion string `json:"pnpmVersion"`
	PNPMHash    string `json:"pnpmHash"`
}

type RuntimeConfigUV struct {
	PythonVersion string `json:"pythonVersion,omitempty"`
}

type RuntimeConfigJVM struct {
	JavaVersion string `json:"javaVersion"`
}

type RuntimeConfig struct {
	Kind            RuntimeKind           `json:"kind"`
	Mode            RuntimeMode           `json:"mode"`
	Managed         *RuntimeConfigManaged `json:"managed,omitempty"`
	System          *RuntimeConfigSystem  `json:"system,omitempty"`
	FNM             *RuntimeConfigFNM     `json:"fnm,omitempty"`
	UV              *RuntimeConfigUV      `json:"uv,omitempty"`
	JVM             *RuntimeConfigJVM     `json:"jvm,omitempty"`
}

type MapOfRuntimes map[string]RuntimeConfig

// ========================================
// Main Config (ENHANCED)
// ========================================

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

func GetDefaultConfig() (string, error) {
	return StripTypes(defaultConfig)
}

func GetDefaultConfigDTS() string {
	return defaultConfigDTS
}

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
