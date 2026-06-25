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
	Name         string   `json:"name"`
	ProjectTypes []string `json:"projectTypes,omitempty"`
	// Skip reports the tool as skipped and never plans or runs it. Prefer this
	// over conditionally omitting the tool from config: an omitted tool is
	// invisible, a skipped one is shown with its reason.
	Skip bool `json:"skip,omitempty"`
	// SkipReason is the human-readable reason shown in the skipped report when
	// Skip is true (e.g. "runs in CI only"). Empty falls back to a generic label.
	SkipReason string                          `json:"skipReason,omitempty"`
	Operations map[OperationType]ToolOperation `json:"operations"`
	// OutputParser names a parser declared in Config.Parsers used to parse this
	// tool's text output into structured results. A dangling reference is a
	// load-time config error.
	OutputParser string `json:"outputParser,omitempty"`
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

// ConfigSetup describes how a managed config file is generated or linked.
type ConfigSetup struct { //nolint:revive // exported: name kept explicit; config.ConfigSetup reads clearer than the bare config.Setup
	ProjectTypes []string `json:"projectTypes,omitempty"`
	// Tools associates this config file with one or more tools (names matching
	// keys in MapOfTools). With `setup --tools`, only configs whose Tools
	// intersect the selected set are written; empty Tools means unassociated
	// infra (skipped under --tools, installed normally without it).
	Tools             []string `json:"tools,omitempty"`
	Scope             string   `json:"scope,omitempty"`
	OtherFileNameList []string `json:"otherFileNameList,omitempty"`
	DeleteOnly        bool     `json:"deleteOnly,omitempty"`
	LinkTarget        string   `json:"linkTarget,omitempty"`
	// ExpectChainHash, when set on the root (topmost) config layer, pins the
	// XXH3-128 hash ("xxh3:<hex>" or bare hex) of the content entering that
	// layer — the output of the whole upstream chain (remote/before layers)
	// before this layer transforms it. setup recomputes that hash and aborts
	// with a drift report when it diverges, so upstream changes to a pinned
	// file surface before any overwrite. Opt-in per file; only the root layer's
	// value is consulted. Bypass with --no-verify-hash.
	ExpectChainHash string `json:"expectChainHash,omitempty"`
	// Content function will be called from JavaScript
	Content any `json:"-"`
}

// MapOfConfigSetup maps a config-file name to its generation definition.
type MapOfConfigSetup map[string]ConfigSetup

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
// OCI Bundle Declaration
// ========================================

// OCIRef declares the OCI bundle that seeds the tool store: the registry
// repository plus the mandatory sha256 digest pinning the bundle content.
// It chains through the config layers as a scalar — the last layer that set
// or spread it wins; `oci: undefined` or `oci: null` resets it.
type OCIRef struct {
	// Ref is the full repository reference including the registry host
	// (e.g. "ghcr.io/owner/repo"). Tags and digests are not allowed inside
	// the ref — content is pinned exclusively by Digest.
	Ref string `json:"ref"`
	// Digest is the bundle index/manifest digest in "sha256:<64 hex>" form.
	Digest string `json:"digest"`
	// Signer optionally pins the sigstore keyless identity of the bundle
	// publisher. When set, pull must verify the signature before layout.
	Signer *OCISigner `json:"signer,omitempty"`
}

// OCISigner pins a sigstore keyless publisher identity: the certificate
// identity (e.g. a GitHub Actions workflow ref) and the OIDC issuer URL.
type OCISigner struct {
	Identity string `json:"identity"`
	Issuer   string `json:"issuer"`
}

// ========================================
// LSP Declarations (reserved, Phase 3+)
// ========================================

// LspType discriminates the kind of an LSP entry.
type LspType string

// LSP entry types: a proxy over a standalone language-server app, or an entry
// derived from an existing tool.
const (
	LspTypeProxy   LspType = "proxy"
	LspTypeDerived LspType = "derived"
)

// LspEntry declares an LSP server. RESERVED for Phase 3+ — declaration only,
// no runtime behavior in this release. Go has no discriminated unions, so the
// proxy/derived variants are flattened into one struct keyed by Type:
//   - Type "proxy":   App + ProjectTypes are used.
//   - Type "derived": Tool is used (inherits its projectTypes/globs + parser).
type LspEntry struct {
	Type         LspType  `json:"type"`
	App          string   `json:"app,omitempty"`
	ProjectTypes []string `json:"projectTypes,omitempty"`
	Tool         string   `json:"tool,omitempty"`
	Order        int      `json:"order,omitempty"`
}

// MapOfLsp maps an LSP entry name to its declaration.
type MapOfLsp map[string]LspEntry

// LspSortable pairs an LSP entry's name with its order for deterministic
// ordering. Phase 3 will consume this when composing the LSP server set; it is
// declared (and unit-tested) now so the tie-break convention is pinned.
type LspSortable struct {
	Name  string
	Order int
}

// lessLspByOrderThenName orders LSP entries by Order ascending, breaking ties
// alphabetically by entry name. This reuses the actual existing tie-break
// mechanism — the planner sorts tool names with sort.Strings (see
// tooling/planner.go collectTasks) — rather than definition order. Pure; not yet
// wired into behavior (Phase 3 consumes it).
func lessLspByOrderThenName(a, b LspSortable) bool {
	if a.Order != b.Order {
		return a.Order < b.Order
	}
	return a.Name < b.Name
}

// ========================================
// Output Parsers (WASM)
// ========================================

// Parser declares a WASM output-parser module as a url+hash data artifact
// (modeled on ArchiveSpec/Bundle — data, not a process). The module is
// downloaded, SHA-256 verified, then loaded into the sandboxed WASM runtime.
type Parser struct {
	URL string `json:"url"`
	// Hash is the mandatory SHA-256 (64 lowercase hex) of the .wasm module.
	// Empty/malformed is a config error per the security policy.
	Hash    string `json:"hash"`
	Version string `json:"version,omitempty"`
}

// MapOfParsers maps a parser name to its declaration.
type MapOfParsers map[string]Parser

// ========================================
// Main Config (ENHANCED)
// ========================================

// Config is the fully resolved datamitsu configuration produced by the JS config layer.
type Config struct {
	Apps          binmanager.MapOfApps    `json:"apps,omitempty"`
	Bundles       binmanager.MapOfBundles `json:"bundles,omitempty"`
	Runtimes      MapOfRuntimes           `json:"runtimes,omitempty"`
	Setup         MapOfConfigSetup        `json:"setup,omitempty"`
	ProjectTypes  MapOfProjectTypes       `json:"projectTypes,omitempty"`
	Tools         MapOfTools              `json:"tools,omitempty"`
	InitCommands  MapOfInitCommands       `json:"initCommands,omitempty"`
	IgnoreRules   []string                `json:"ignoreRules,omitempty"`
	SharedStorage map[string]string       `json:"sharedStorage,omitempty"`
	// OCI pins the store-seeding bundle. omitempty is load-bearing: the whole
	// Config is marshaled into the execution-cache invalidation key, and a nil
	// field must not change that key on upgrade.
	OCI *OCIRef `json:"oci,omitempty"`
	// Parsers declares WASM output-parser modules (url+hash data artifacts),
	// referenced by name from Tool.outputParser.
	Parsers MapOfParsers `json:"parsers,omitempty"`
	// Lsp declares LSP servers (reserved for Phase 3+; structurally validated
	// at load time but with no runtime behavior in this release).
	Lsp MapOfLsp `json:"lsp,omitempty"`
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
