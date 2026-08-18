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

// ToolArity is the shape of paths an operation accepts on its command line.
// Orthogonal to ToolScope: scope says where the process starts, arity says what
// may go in argv. Inferred from Args (InferArity); declaring it is an assertion,
// not an override.
type ToolArity string

// Tool argument arities, in the order InferArity tests for them.
const (
	ArityDir  ToolArity = "dir"  // one directory via {target}, no file list
	ArityMany ToolArity = "many" // arbitrary list of paths via {files}
	ArityOne  ToolArity = "one"  // exactly one path per process via {file}
	ArityNone ToolArity = "none" // no path in argv; the file set is only a trigger
)

// OperationType distinguishes the kind of work a tool operation performs.
type OperationType string

// Tool operation types: applying fixes or only reporting lint findings.
const (
	OpFix  OperationType = "fix"
	OpLint OperationType = "lint"
)

// ToolInputMode controls how a tool operation receives file content.
type ToolInputMode string

// Tool input modes: pass file paths as arguments (default) or pipe the file's
// content to the tool's standard input.
const (
	// ToolInputFile passes file paths via the {file}/{files} placeholders. This
	// is the default and preserves the historical behavior for every tool.
	ToolInputFile ToolInputMode = "file"
	// ToolInputStdin pipes the target file's content to the tool's stdin (the
	// stdin→stdout formatter contract). Used with per-file scope.
	ToolInputStdin ToolInputMode = "stdin"
)

// ToolOutputMode controls how a tool operation's result is captured.
type ToolOutputMode string

// Tool output modes: let the tool mutate files in place (default) or capture
// stdout separately as the candidate formatted content.
const (
	// ToolOutputInplace captures stdout+stderr combined for reporting and lets
	// the tool mutate files directly. This is the default.
	ToolOutputInplace ToolOutputMode = "inplace"
	// ToolOutputStdout captures the tool's stdout separately (kept apart from
	// stderr) as the candidate formatted content for the diff-in-core path.
	ToolOutputStdout ToolOutputMode = "stdout"
)

// ToolOperation describes a single fix or lint invocation of a tool.
type ToolOperation struct {
	App   string    `json:"app"`
	Args  []string  `json:"args"`
	Scope ToolScope `json:"scope"`
	// Arity asserts the argv path shape; must equal InferArity(op) when set.
	Arity ToolArity `json:"arity,omitempty"`
	// Granularity is the smallest input set on which this operation's verdict is
	// complete. Inferred when unset (InferGranularity); declaring "file" is a
	// speed decision, declaring "unit"/"repo" is always safe.
	Granularity ToolGranularity `json:"granularity,omitempty"`
	// Batch is deprecated in favour of Arity. Retained so a config still setting
	// it errors loudly — goja's ExportTo consults only fields on this struct, so
	// removing it would drop the key silently. See ValidateToolDeprecations.
	Batch        *bool             `json:"batch,omitempty"`
	Globs        []string          `json:"globs,omitempty"`
	ExcludeGlobs []string          `json:"excludeGlobs,omitempty"`
	Priority     int               `json:"priority,omitempty"`
	Cache        *bool             `json:"cache,omitempty"`        // Enable caching (default: true)
	InvalidateOn []string          `json:"invalidateOn,omitempty"` // Config files that invalidate cache
	Env          map[string]string `json:"env,omitempty"`          // Extra environment variables for this operation
	// Input selects how the file content reaches the tool: "file" (default,
	// path via {file}/{files}) or "stdin" (pipe the file's content to stdin).
	Input ToolInputMode `json:"input,omitempty"`
	// Output selects how the tool's result is captured: "inplace" (default,
	// combined stdout+stderr, tool mutates files) or "stdout" (capture stdout
	// separately as the candidate formatted content).
	Output ToolOutputMode `json:"output,omitempty"`
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
	// OutputParser selects the WASM parser for this tool's output: which `parsers`
	// entry (module) to load and which parser inside it (dispatch key) to run. nil
	// when the tool has no parser. See OutputParser.
	OutputParser *OutputParser `json:"outputParser,omitempty"`
}

// OutputParser references a tool's output parser as (module, parser): Module is
// the `parsers` config entry to load — a specific WASM artifact, so different
// versions are different entries — and Parser is the dispatch key inside that
// module (a name from `devtools parsers list`). The two are kept separate so two
// tools can point at two versions of the same module. Always an object in config;
// there is no string shorthand.
type OutputParser struct {
	Module string `json:"module"`
	Parser string `json:"parser"`
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

// ParserOCI declares a parser module published as an OCI artifact: the registry
// repository plus the mandatory digest pinning its artifact manifest. It
// deliberately mirrors OCIRef so there is one reference vocabulary, one
// validator and one set of error strings across bundles and parsers.
type ParserOCI struct {
	// Ref is the full repository reference including the registry host
	// (e.g. "ghcr.io/datamitsu/datamitsu-parsers"). Tags and digests are not
	// allowed inside the ref — content is pinned exclusively by Digest.
	Ref string `json:"ref"`
	// Digest pins the artifact manifest in "sha256:<64 hex>" form. The single
	// layer inside that manifest must have digest "sha256:" + Parser.Hash, so a
	// substituted payload is rejected before it is requested.
	Digest string `json:"digest"`
	// Signer is REJECTED at validation while sigstore verification is
	// unimplemented (as it is for a bundle). The field exists so the eventual
	// implementation reuses the one signer vocabulary rather than inventing a
	// second, and so a config that sets it fails loudly instead of silently
	// getting no verification at all.
	Signer *OCISigner `json:"signer,omitempty"`
}

// Parser declares a WASM output-parser module as a hash-pinned data artifact
// (modeled on ArchiveSpec/Bundle — data, not a process). The module is fetched,
// SHA-256 verified, then loaded into the sandboxed WASM runtime. Exactly one
// source — URL or OCI — must be declared.
type Parser struct {
	// URL is the https:// source of the module (file:// additionally, and only,
	// in a dev-link build). omitempty so an oci-sourced entry does not marshal
	// `"url":""`; a url-sourced entry marshals byte-identically to before the
	// field became optional, so no existing config's execution-cache key moves.
	URL string `json:"url,omitempty"`
	// Hash is the mandatory SHA-256 (64 lowercase hex) of the .wasm module, for
	// every source. Empty/malformed is a config error per the security policy.
	// For an OCI source it is simultaneously the expected layer blob digest.
	Hash string `json:"hash"`
	// OCI sources the module from a registry instead of over https. omitempty is
	// load-bearing for the same reason as Config.OCI: the whole Config is
	// marshaled into the execution-cache invalidation key.
	OCI *ParserOCI `json:"oci,omitempty"`
	// NOTE: there is intentionally no `version` field. A module reports its own
	// build-injected version via its WASM `describe` export (read by
	// `datamitsu devtools parsers list`), so declaring it here too would only let
	// the declared and actual versions drift. The config carries a source and a
	// hash only.
}

// MapOfParsers maps a parser name to its declaration.
type MapOfParsers map[string]Parser

// ========================================
// Main Config (ENHANCED)
// ========================================

// Config is the fully resolved datamitsu configuration produced by the JS config layer.
type Config struct {
	Apps         binmanager.MapOfApps    `json:"apps,omitempty"`
	Bundles      binmanager.MapOfBundles `json:"bundles,omitempty"`
	Runtimes     MapOfRuntimes           `json:"runtimes,omitempty"`
	Setup        MapOfConfigSetup        `json:"setup,omitempty"`
	ProjectTypes MapOfProjectTypes       `json:"projectTypes,omitempty"`
	Tools        MapOfTools              `json:"tools,omitempty"`
	// Execution holds run-shaping policy not tied to a single tool. A pointer so
	// omitempty actually elides it: the whole config is marshalled into the cache
	// invalidation key, and a struct value would serialize as {} for every config
	// and reset every user's cache on upgrade.
	Execution     *Execution        `json:"execution,omitempty"`
	InitCommands  MapOfInitCommands `json:"initCommands,omitempty"`
	IgnoreRules   []string          `json:"ignoreRules,omitempty"`
	SharedStorage map[string]string `json:"sharedStorage,omitempty"`
	// OCI pins the store-seeding bundle. omitempty is load-bearing: the whole
	// Config is marshaled into the execution-cache invalidation key, and a nil
	// field must not change that key on upgrade.
	OCI *OCIRef `json:"oci,omitempty"`
	// Parsers declares WASM output-parser modules (hash-pinned data artifacts),
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
