// Package binmanager downloads, verifies, installs and executes managed
// binaries, bundles and runtime apps across platforms.
package binmanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
	"github.com/datamitsu/datamitsu/internal/ui"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

var log = logger.Logger.With(zap.Namespace("binmanager"))

// networkDownloads counts every entry into a binary download path,
// process-wide. It exists as a guard for the side-effect-free resolution path
// (ResolveCommandInfo): a refactor that reintroduces a fetch there flips this
// counter and fails the guard test deterministically — including offline,
// where the fetch would error for an unrelated reason and hide the regression.
var networkDownloads atomic.Int64

// NetworkDownloads reports how many binary downloads have been started in this
// process. Test-visible guard, not part of the runtime contract.
func NetworkDownloads() int64 { return networkDownloads.Load() }

// MapOfBinaries maps OS and architecture to the binaries available for it.
type MapOfBinaries = map[syslist.OsType]map[syslist.ArchType]map[string]BinaryOsArchInfo

// MapOfApps maps an app name to its App definition.
type MapOfApps = map[string]App

// AppConfigBinary configures an app distributed as a per-platform downloadable binary.
type AppConfigBinary struct {
	Binaries MapOfBinaries `json:"binaries"`
	Version  string        `json:"version,omitempty"`
}

// AppConfigUV configures a Python tool installed under the uv-managed runtime.
type AppConfigUV struct {
	PackageName    string `json:"packageName"`
	Version        string `json:"version"`
	Runtime        string `json:"runtime,omitempty"`
	LockFile       string `json:"lockFile,omitempty"`
	RequiresPython string `json:"requiresPython,omitempty"`
}

// AppConfigNode configures an npm tool installed under the archive-based node
// runtime (kind "node"). Node apps are pnpm-installed npm packages; node itself
// is acquired as a direct, hash-pinned archive (see runtimemanager/node.go).
type AppConfigNode struct {
	PackageName  string            `json:"packageName"`
	Version      string            `json:"version"`
	BinPath      string            `json:"binPath"`
	Runtime      string            `json:"runtime,omitempty"`
	LockFile     string            `json:"lockFile,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

// AppConfigJVM configures a JAR-based tool run under the managed JVM runtime.
type AppConfigJVM struct {
	JarURL    string `json:"jarUrl"`
	JarHash   string `json:"jarHash"`
	Version   string `json:"version"`
	Runtime   string `json:"runtime,omitempty"`
	MainClass string `json:"mainClass,omitempty"`
}

// AppConfigGo configures a Go tool installed under the managed Go runtime.
type AppConfigGo struct {
	PackageName string `json:"packageName"`
	Version     string `json:"version"`
	Runtime     string `json:"runtime,omitempty"`
	LockFile    string `json:"lockFile,omitempty"`
}

// AppConfigShell configures an app that resolves to a shell command on PATH.
type AppConfigShell struct {
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}

// AppVersionCheck configures how an app's version is verified, or disables the check.
type AppVersionCheck struct {
	Disabled bool     `json:"disabled,omitempty"`
	Args     []string `json:"args,omitempty"`
}

// App is the registry definition of a managed application across its supported kinds.
type App struct {
	// Required binary (downloaded during Install())
	// Optional binaries are downloaded only on first access via GetBinaryPath()
	Required bool `json:"required,omitempty"`

	// Lazy defers installation: the app is NOT installed during `datamitsu init`,
	// even when it declares Links, and is installed only on first `datamitsu exec`.
	// Its `.datamitsu/` links are materialized at that point. Use for user-invoked
	// CLIs (e.g. a presentation tool) whose deps/links aren't needed until run.
	// Apps consumed by hooks, tools, or ConfigSetup must stay eager (Lazy=false),
	// since smart-init can't otherwise see those references.
	Lazy bool `json:"lazy,omitempty"`

	Description  string           `json:"description,omitempty"`
	VersionCheck *AppVersionCheck `json:"versionCheck,omitempty"`

	Binary *AppConfigBinary `json:"binary,omitempty"`
	Uv     *AppConfigUV     `json:"uv,omitempty"`
	Node   *AppConfigNode   `json:"node,omitempty"`
	Jvm    *AppConfigJVM    `json:"jvm,omitempty"`
	Go     *AppConfigGo     `json:"go,omitempty"`
	Shell  *AppConfigShell  `json:"shell,omitempty"`

	// Env holds user-defined environment variables applied to all app kinds, both
	// at install time (uv/node/go) and run time. Values support ${STORE} and
	// ${APP_DIR} placeholders. Keys already set by datamitsu/the runtime win.
	Env map[string]string `json:"env,omitempty"`

	Files    map[string]string       `json:"files,omitempty"`
	Links    map[string]string       `json:"links,omitempty"`
	Archives map[string]*ArchiveSpec `json:"archives,omitempty"`
}

// ArchiveSpec represents an archive that can be extracted into an app's install directory.
// Supports inline (brotli-compressed tar) and external (URL with hash) formats.
type ArchiveSpec struct {
	Inline string         `json:"inline,omitempty"`
	URL    string         `json:"url,omitempty"`
	Hash   string         `json:"hash,omitempty"`
	Format BinContentType `json:"format,omitempty"`
}

// IsInline reports whether the archive is supplied inline rather than by URL.
func (a *ArchiveSpec) IsInline() bool {
	return a.Inline != "" && a.URL == ""
}

// IsExternal reports whether the archive is fetched from an external URL.
func (a *ArchiveSpec) IsExternal() bool {
	return a.URL != "" && a.Inline == ""
}

// RuntimeAppManager handles runtime-managed applications (uv, node, jvm, go).
// Implemented by runtimemanager.RuntimeManager to avoid circular imports.
type RuntimeAppManager interface {
	GetCommandInfo(ctx context.Context, appName string, app App) (*CommandInfo, error)
	// ResolveCommandInfo returns the same CommandInfo without installing
	// anything: no download, no subprocess. Takes no context because it cannot
	// block on I/O worth cancelling.
	ResolveCommandInfo(appName string, app App) (*CommandInfo, error)
	ComputeAppPath(appName string, app App) (string, error)
}

// BinManager downloads, verifies, installs and runs managed apps and bundles.
type BinManager struct {
	mapOfApps      MapOfApps
	mapOfBundles   MapOfBundles
	runtimeManager RuntimeAppManager
	resolver       *target.Resolver

	// downloadGroup coalesces concurrent downloads of the same binary so that
	// N parallel GetBinaryPath calls for one uninstalled binary trigger exactly
	// one download (keyed by binary name).
	downloadGroup singleflight.Group
}

// New creates a BinManager for the given apps and bundles using the host target resolver.
func New(mapOfApps MapOfApps, mapOfBundles MapOfBundles, runtimeManager RuntimeAppManager) *BinManager {
	return &BinManager{
		mapOfApps:      mapOfApps,
		mapOfBundles:   mapOfBundles,
		runtimeManager: runtimeManager,
		resolver:       target.NewResolver(target.HostTarget()),
	}
}

// NewWithResolver creates a BinManager with a custom resolver (for testing).
func NewWithResolver(mapOfApps MapOfApps, mapOfBundles MapOfBundles, runtimeManager RuntimeAppManager, resolver *target.Resolver) *BinManager {
	return &BinManager{
		mapOfApps:      mapOfApps,
		mapOfBundles:   mapOfBundles,
		runtimeManager: runtimeManager,
		resolver:       resolver,
	}
}

// parseBinaryCandidates converts the nested storage map (os -> arch -> libc -> BinaryOsArchInfo)
// into a flat list of Candidate structs for the resolver.
func parseBinaryCandidates(binaries MapOfBinaries) []target.Candidate {
	var candidates []target.Candidate
	for osType, archMap := range binaries {
		for archType, libcMap := range archMap {
			for libc, info := range libcMap {
				infoCopy := info
				candidates = append(candidates, target.Candidate{
					Target: target.Target{
						OS:   string(osType),
						Arch: string(archType),
						Libc: target.LibcType(libc),
					},
					Info: &infoCopy,
				})
			}
		}
	}
	return candidates
}

// resolveInstallTimeoutSeconds returns the effective per-app install timeout in
// seconds, read through runtimeconfig (the single source of truth) rather than
// env directly. It falls back to a fresh Compute() when runtimeconfig.Init() has
// not run (e.g. unit tests constructing a BinManager directly), mirroring the
// engine's configinputs fallback.
func resolveInstallTimeoutSeconds() int {
	eff, err := runtimeconfig.Get()
	if err != nil {
		eff = runtimeconfig.Compute()
	}
	return eff.InstallTimeoutSeconds
}

// newInstallContext derives a context carrying the effective per-app install
// timeout. A configured value of 0 disables the deadline: the returned context
// is cancelable but never expires. timeoutSec is returned so callers can render
// a precise "timed out after Ns" message. Callers MUST always call cancel
// (defer cancel()).
func newInstallContext(parent context.Context) (ctx context.Context, cancel context.CancelFunc, timeoutSec int) {
	timeoutSec = resolveInstallTimeoutSeconds()
	if timeoutSec <= 0 {
		ctx, cancel = context.WithCancel(parent)
		return ctx, cancel, 0
	}
	ctx, cancel = context.WithTimeout(parent, time.Duration(timeoutSec)*time.Second)
	return ctx, cancel, timeoutSec
}

// wrapInstallTimeout turns a context-deadline failure from an install download
// into a clear, user-facing timeout message. Non-timeout errors (and nil) pass
// through unchanged.
func wrapInstallTimeout(err error, timeoutSec int) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("installation timed out after %ds: %w", timeoutSec, err)
	}
	return err
}

// DownloadResult records the outcome of downloading a single named binary.
type DownloadResult struct {
	Name  string
	Error error
}

// SkippedBinary records a binary that was not installed, with the reason
// (typically "binary 'X' is not available for <host>").
type SkippedBinary struct {
	Name   string
	Reason string
}

// InstallStats summarizes the results of an install run across all binaries.
type InstallStats struct {
	Skipped       []SkippedBinary
	AlreadyCached []string
	Downloaded    []string
	Failed        []DownloadResult
}

// InstallWithConcurrency installs binaries with specified concurrency level
// Returns installation statistics
func (bm *BinManager) InstallWithConcurrency(ctx context.Context, includeOptional bool, concurrency int, failOnError bool) (InstallStats, error) {
	stats := InstallStats{
		Skipped:       []SkippedBinary{},
		AlreadyCached: []string{},
		Downloaded:    []string{},
		Failed:        []DownloadResult{},
	}

	var toDownload []string
	for name, app := range bm.mapOfApps {
		if app.Binary == nil {
			continue
		}

		if !includeOptional && !app.Required {
			log.Debug("skipping optional binary", zap.String("name", name))
			continue
		}

		binPath, err := bm.getBinaryPath(name)
		if err != nil {
			// A resolve failure here means no binary matches this host (the only
			// error getBinaryPath returns once app.Binary != nil); keep the reason
			// so init can show why the tool was skipped instead of a bare "skipped".
			stats.Skipped = append(stats.Skipped, SkippedBinary{Name: name, Reason: err.Error()})
			continue
		}

		if _, err := os.Stat(binPath); err == nil {
			stats.AlreadyCached = append(stats.AlreadyCached, name)
			continue
		}

		toDownload = append(toDownload, name)
	}

	if len(toDownload) == 0 {
		return stats, nil
	}

	jobs := make(chan string, len(toDownload))
	results := make(chan DownloadResult, len(toDownload))

	var wg sync.WaitGroup

	for range concurrency {
		wg.Go(func() {
			for name := range jobs {
				err := bm.downloadWithTimeout(ctx, name)
				results <- DownloadResult{
					Name:  name,
					Error: err,
				}

				if failOnError && err != nil {
					return
				}
			}
		})
	}

	for _, name := range toDownload {
		jobs <- name
	}
	close(jobs)

	wg.Wait()
	close(results)

	for result := range results {
		if result.Error != nil {
			stats.Failed = append(stats.Failed, result)
			if failOnError {
				return stats, fmt.Errorf("failed to download %s: %w", result.Name, result.Error)
			}
		} else {
			stats.Downloaded = append(stats.Downloaded, result.Name)
		}
	}

	return stats, nil
}

// Install downloads and caches only required binaries (Required: true)
func (bm *BinManager) Install(ctx context.Context) error {
	return bm.installInternal(ctx, false)
}

// GetBinaryPath returns the path to a binary, downloading it if necessary (lazy loading)
func (bm *BinManager) GetBinaryPath(ctx context.Context, name string) (string, error) {
	binPath, err := bm.getBinaryPath(name)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(binPath); err == nil {
		log.Debug("binary found in cache", zap.String("name", name), zap.String("path", binPath))
		return binPath, nil
	}

	log.Debug("binary not found in cache, downloading", zap.String("name", name))

	// Coalesce concurrent downloads of the same binary: only one goroutine
	// performs the download, the rest wait and share its result. Re-check the
	// cache inside the critical section so a download that completed while we
	// were blocked is a no-op.
	_, err, _ = bm.downloadGroup.Do(name, func() (any, error) {
		if _, statErr := os.Stat(binPath); statErr == nil {
			return struct{}{}, nil
		}

		// Progress (a bar in a terminal, throttled lines in CI) is rendered by
		// the download layer through the shared ui display.
		if err := bm.downloadWithTimeout(ctx, name); err != nil {
			return nil, fmt.Errorf("failed to download %s: %w", name, err)
		}

		return struct{}{}, nil
	})
	if err != nil {
		return "", fmt.Errorf("download %s: %w", name, err)
	}

	return binPath, nil
}

// ensureToolsConcurrency bounds how many distinct tools EnsureTools installs in
// parallel. Same-tool concurrency cannot occur (names are deduped), and each
// install path is itself single-flighted, so this is purely a throughput knob.
const ensureToolsConcurrency = 4

// EnsureTools installs every distinct tool named in names before the caller
// runs them, so that subsequent parallel execution never triggers a lazy,
// racy install. Names are deduplicated; each distinct tool is resolved once via
// GetCommandInfo, which installs binaries (through GetBinaryPath) and uv/node/
// go/jvm runtime apps (through the single-flighted runtime manager). Shell apps
// need no install and resolve cheaply.
//
// Errors are aggregated: a non-fatal failure on one tool does not abort the
// rest of the set. An unknown tool name surfaces as a clear error. An empty
// list is a no-op.
func (bm *BinManager) EnsureTools(ctx context.Context, names []string) error {
	// Deduplicate while preserving determinism.
	seen := make(map[string]struct{}, len(names))
	distinct := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		distinct = append(distinct, name)
	}
	sort.Strings(distinct)

	if len(distinct) == 0 {
		return nil
	}

	concurrency := min(len(distinct), ensureToolsConcurrency)

	jobs := make(chan string)
	errs := make([]error, len(distinct))
	idxOf := make(map[string]int, len(distinct))
	for i, name := range distinct {
		idxOf[name] = i
	}

	var wg sync.WaitGroup
	for range concurrency {
		wg.Go(func() {
			for name := range jobs {
				if err := ctx.Err(); err != nil {
					errs[idxOf[name]] = err
					continue
				}
				if _, err := bm.GetCommandInfo(ctx, name); err != nil {
					errs[idxOf[name]] = fmt.Errorf("failed to ensure tool %q: %w", name, err)
				}
			}
		})
	}

	for _, name := range distinct {
		jobs <- name
	}
	close(jobs)
	wg.Wait()

	return errors.Join(errs...)
}

// GetCommandInfo returns command information for executing an application
// Works with all application types: binary, shell, uv, node, jvm, go
func (bm *BinManager) GetCommandInfo(ctx context.Context, appName string) (*CommandInfo, error) {
	app, ok := bm.mapOfApps[appName]
	if !ok {
		return nil, fmt.Errorf("app '%s' not found in registry", appName)
	}

	var cmdInfo *CommandInfo

	switch {
	case app.Shell != nil:
		cmdInfo = &CommandInfo{
			Type:    "shell",
			Command: app.Shell.Name,
			Args:    app.Shell.Args,
		}

	case app.Binary != nil:
		binPath, err := bm.GetBinaryPath(ctx, appName)
		if err != nil {
			return nil, err
		}
		cmdInfo = &CommandInfo{
			Type:    "binary",
			Command: binPath,
		}

	case app.Uv != nil || app.Node != nil || app.Jvm != nil || app.Go != nil:
		if bm.runtimeManager == nil {
			return nil, fmt.Errorf("no runtime manager configured for runtime-managed app %q", appName)
		}
		ci, err := bm.runtimeManager.GetCommandInfo(ctx, appName, app)
		if err != nil {
			return nil, err
		}
		cmdInfo = ci

	default:
		return nil, fmt.Errorf("app '%s' has no valid configuration", appName)
	}

	bm.mergeAppEnv(appName, app, cmdInfo)

	return cmdInfo, nil
}

// ResolveCommandInfo answers "where would this app be, and is it there?"
// without touching the network. It returns the same CommandInfo shape
// GetCommandInfo does — including the merged app Env — plus whether the
// resolved Command currently exists on disk.
//
// This is an addition, not a replacement: GetCommandInfo still installs. The
// two differ only in side effects, so an app that reports installed=true here
// runs from exactly the path GetCommandInfo would hand the exec path.
//
// Shell apps resolve to their bare command name with installed=true: their
// executable is found through the inherited PATH at spawn time, so there is no
// store path to stat.
func (bm *BinManager) ResolveCommandInfo(appName string) (*CommandInfo, bool, error) {
	app, ok := bm.mapOfApps[appName]
	if !ok {
		return nil, false, fmt.Errorf("app '%s' not found in registry", appName)
	}

	var (
		cmdInfo   *CommandInfo
		installed bool
	)

	switch {
	case app.Shell != nil:
		cmdInfo = &CommandInfo{
			Type:    "shell",
			Command: app.Shell.Name,
			Args:    app.Shell.Args,
		}
		installed = true

	case app.Binary != nil:
		// getBinaryPath is the non-downloading half of GetBinaryPath: it does
		// the same config-hash path math and stops before the fetch.
		binPath, err := bm.getBinaryPath(appName)
		if err != nil {
			return nil, false, err
		}
		cmdInfo = &CommandInfo{
			Type:    "binary",
			Command: binPath,
		}
		installed = pathExists(binPath)

	case app.Uv != nil || app.Node != nil || app.Jvm != nil || app.Go != nil:
		if bm.runtimeManager == nil {
			return nil, false, fmt.Errorf("no runtime manager configured for runtime-managed app %q", appName)
		}
		ci, err := bm.runtimeManager.ResolveCommandInfo(appName, app)
		if err != nil {
			return nil, false, err
		}
		cmdInfo = ci
		// Every path the runtime declares required, not just the one that gets
		// exec'd: the runtime installers treat a wrapper without its package, or
		// a venv without its interpreter, as not installed, and an answer that
		// disagreed with them would let the shim skip the repair they exist to
		// trigger.
		installed = allPathsExist(cmdInfo.HealthPaths())

	default:
		return nil, false, fmt.Errorf("app '%s' has no valid configuration", appName)
	}

	bm.mergeAppEnv(appName, app, cmdInfo)

	return cmdInfo, installed, nil
}

// pathExists reports whether path names an existing filesystem entry. A
// symlink is followed, matching what execve does.
func pathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// allPathsExist reports whether every path exists. An empty list is false: an
// app with nothing to stat has not been resolved to anywhere, so it cannot be
// installed.
func allPathsExist(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !pathExists(p) {
			return false
		}
	}
	return true
}

// ResolvedBinaryInfo returns the BinaryOsArchInfo selected for the host
// target without downloading anything. Used by the OCI bundle seeder to
// re-verify seeded binaries against the published hash.
func (bm *BinManager) ResolvedBinaryInfo(name string) (BinaryOsArchInfo, error) {
	_, info, err := bm.getBinaryInfo(name)
	return info, err
}

// ComputeInstallPath returns the install directory path for an app without checking existence.
func (bm *BinManager) ComputeInstallPath(appName string) (string, error) {
	app, ok := bm.mapOfApps[appName]
	if !ok {
		return "", fmt.Errorf("app %q not found in registry", appName)
	}

	if app.Binary != nil {
		return bm.getBinaryPath(appName)
	}

	if app.Uv != nil || app.Node != nil || app.Jvm != nil || app.Go != nil {
		if bm.runtimeManager == nil {
			return "", fmt.Errorf("no runtime manager configured for runtime-managed app %q", appName)
		}
		return bm.runtimeManager.ComputeAppPath(appName, app)
	}

	return "", fmt.Errorf("app %q has no valid configuration for install path", appName)
}

// GetInstallRoot returns the install directory for an app, verifying it exists.
func (bm *BinManager) GetInstallRoot(appName string) (string, error) {
	installPath, err := bm.ComputeInstallPath(appName)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(installPath); err != nil {
		return "", fmt.Errorf("app %q is not installed (path %s does not exist)", appName, installPath)
	}

	return installPath, nil
}

// WriteAppFiles writes files and extracts archives into the application install directory.
//
// Initialization order:
//  1. Archives extracted first, sorted alphabetically by name. Later archives overwrite
//     files from earlier archives when paths overlap.
//  2. Files written second. Files can overwrite any content extracted from archives.
//
// This ordering means Files always take precedence over Archives, and among Archives,
// later names (alphabetically) take precedence over earlier ones for overlapping paths.
func WriteAppFiles(ctx context.Context, installPath string, files map[string]string, archives map[string]*ArchiveSpec) error {
	if err := os.MkdirAll(installPath, 0o755); err != nil {
		return fmt.Errorf("failed to create install directory: %w", err)
	}

	if len(archives) > 0 {
		if err := extractArchives(ctx, installPath, archives); err != nil {
			return fmt.Errorf("failed to extract archives: %w", err)
		}
	}

	if len(files) > 0 {
		if err := writeFiles(installPath, files); err != nil {
			return fmt.Errorf("failed to write files: %w", err)
		}
	}

	return nil
}

// extractArchives extracts all archives into installPath in alphabetical order by name.
// When multiple archives contain files at the same path, later archives (alphabetically)
// overwrite earlier ones.
func extractArchives(ctx context.Context, installPath string, archives map[string]*ArchiveSpec) error {
	names := make([]string, 0, len(archives))
	for name := range archives {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		spec := archives[name]
		if spec == nil {
			return fmt.Errorf("archive %q: spec is nil", name)
		}

		switch {
		case spec.IsInline():
			tarData, err := DecompressArchive(spec.Inline)
			if err != nil {
				return fmt.Errorf("archive %q: failed to decompress inline archive: %w", name, err)
			}

			if _, err := extractArchiveToPath(installPath, tarData, "", BinContentTypeTar); err != nil {
				return fmt.Errorf("archive %q: failed to extract inline archive: %w", name, err)
			}

			log.Debug("extracted inline archive", zap.String("name", name), zap.String("dest", installPath))
		case spec.IsExternal():
			if err := downloadAndExtractExternalArchive(ctx, name, spec, installPath); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive %q: must have either inline or url field set", name)
		}
	}

	return nil
}

func downloadAndExtractExternalArchive(ctx context.Context, name string, spec *ArchiveSpec, installPath string) error {
	if spec.Hash == "" {
		return fmt.Errorf("archive %q: external archive must have hash field (SHA-256)", name)
	}
	if spec.Format == "" {
		return fmt.Errorf("archive %q: external archive must have format field", name)
	}

	tmpFile, err := os.CreateTemp("", "archive-*")
	if err != nil {
		return fmt.Errorf("archive %q: failed to create temp file: %w", name, err)
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		log.Warn("failed to close temp file", zap.String("path", tmpPath), zap.Error(err))
	}
	defer func() {
		if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
			log.Warn("failed to remove temp file", zap.String("path", tmpPath), zap.Error(err))
		}
	}()

	if err := downloadFileSimple(ctx, spec.URL, tmpPath); err != nil {
		return fmt.Errorf("archive %q: download failed: %w", name, err)
	}

	if err := verifyFileHash(tmpPath, spec.Hash, BinHashTypeSHA256); err != nil {
		return fmt.Errorf("archive %q: hash verification failed: %w", name, err)
	}

	if _, err := extractArchiveToPath(installPath, nil, tmpPath, spec.Format); err != nil {
		return fmt.Errorf("archive %q: extraction failed: %w", name, err)
	}

	log.Debug("extracted external archive",
		zap.String("name", name),
		zap.String("url", spec.URL),
		zap.String("dest", installPath),
	)

	return nil
}

func writeFiles(installPath string, files map[string]string) error {
	cleanInstall := filepath.Clean(installPath)
	for filename, content := range files {
		filePath := filepath.Join(installPath, filename)
		if !strings.HasPrefix(filepath.Clean(filePath), cleanInstall+string(filepath.Separator)) {
			return fmt.Errorf("file %q escapes install directory", filename)
		}
		if dir := filepath.Dir(filePath); dir != installPath {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("failed to create directory for file %q: %w", filename, err)
			}
		}
		if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("failed to write file %q: %w", filename, err)
		}
	}
	return nil
}

func downloadFileSimple(ctx context.Context, url, destPath string) error {
	if err := httpx.GuardOffline("download of " + url); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP GET failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn("failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer func() {
		if err := out.Close(); err != nil {
			log.Warn("failed to close output file", zap.String("path", destPath), zap.Error(err))
		}
	}()

	written, err := io.Copy(out, io.LimitReader(resp.Body, MaxBinarySize+1))
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	if written > MaxBinarySize {
		return fmt.Errorf("download exceeds maximum size of %d bytes", MaxBinarySize)
	}

	return nil
}

// AppInfo describes a registered app for listing and introspection.
type AppInfo struct {
	Name        string
	Type        string
	Command     string
	Version     string
	PackageName string
	Description string
}

// CommandInfo contains information about command for executing an application
type CommandInfo struct {
	Type    string            // "binary", "shell", "uv", "node", "jvm", "go"
	Command string            // Path to binary or command name
	Args    []string          // Additional arguments (for shell)
	Env     map[string]string // Additional env variables (for shell)

	// Artifact is the file whose presence means "this app is installed", when
	// that is not Command itself. A JVM app runs `java -jar app.jar`: Command is
	// an interpreter that a system-mode runtime may supply from PATH, so it says
	// nothing about whether the app was ever downloaded. Empty means Command is
	// the artifact.
	Artifact string

	// RequiredPaths are the other absolute paths that must exist for the app to
	// run correctly, beyond InstalledPath.
	//
	// One path is not enough for a runtime-managed app, and each installer
	// already knows it: a UV app needs its venv interpreter as well as the
	// wrapper script (the interpreter is a symlink into the shared Python dir and
	// dangles after a partial store restore), a node app needs the installed
	// package under node_modules as well as pnpm's .bin shim, and a
	// managed-runtime app needs its runtime binary. Reporting such an app
	// installed on the strength of the wrapper alone makes the exec fail in the
	// tool's own voice — or, worse for a node .bin shim, succeed against a system
	// `node` found through PATH.
	//
	// A path here must be one whose absence means "reinstall", matching the
	// health rule the installer applies; an interpreter a system-mode runtime
	// names by bare word belongs on PATH, not here.
	RequiredPaths []string
}

// InstalledPath returns the file whose existence decides whether the app is
// installed — Artifact when the invocation runs through an interpreter,
// Command otherwise.
func (c *CommandInfo) InstalledPath() string {
	if c.Artifact != "" {
		return c.Artifact
	}
	return c.Command
}

// HealthPaths returns every file that must exist for the app to be considered
// installed: InstalledPath plus RequiredPaths, with empties dropped.
func (c *CommandInfo) HealthPaths() []string {
	paths := make([]string, 0, 1+len(c.RequiredPaths))
	if p := c.InstalledPath(); p != "" {
		paths = append(paths, p)
	}
	for _, p := range c.RequiredPaths {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// GetAppsList returns sorted list of all available applications with types
func (bm *BinManager) GetAppsList() []AppInfo {
	apps := make([]AppInfo, 0, len(bm.mapOfApps))

	for name, app := range bm.mapOfApps {
		info := AppInfo{
			Name:        name,
			Type:        "unknown",
			Description: app.Description,
		}

		switch {
		case app.Binary != nil:
			info.Type = "binary"
			info.Version = app.Binary.Version
		case app.Uv != nil:
			info.Type = "uv"
			info.Version = app.Uv.Version
			info.PackageName = app.Uv.PackageName
		case app.Node != nil:
			info.Type = "node"
			info.Version = app.Node.Version
			info.PackageName = app.Node.PackageName
		case app.Jvm != nil:
			info.Type = "jvm"
			info.Version = app.Jvm.Version
		case app.Go != nil:
			info.Type = "go"
			info.Version = app.Go.Version
			info.PackageName = app.Go.PackageName
		case app.Shell != nil:
			info.Type = "shell"
			info.Command = app.Shell.Name
		}

		apps = append(apps, info)
	}

	return apps
}

// GetExecCmd returns an exec.Cmd ready to execute the given app with args.
// For binary apps: ensures binary is cached (downloads if needed).
// For runtime apps: delegates to runtimeManager.GetCommandInfo.
// Returns (nil, nil) for shell apps — callers must handle nil.
func (bm *BinManager) GetExecCmd(ctx context.Context, name string, args []string) (*exec.Cmd, error) {
	app, ok := bm.mapOfApps[name]
	if !ok {
		return nil, fmt.Errorf("app '%s' not found in registry", name)
	}

	if app.Shell != nil {
		// Shell apps have no exec.Cmd; callers check for a nil cmd (documented contract).
		return nil, nil //nolint:nilnil
	}

	cmdInfo, err := bm.GetCommandInfo(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get command info for %s: %w", name, err)
	}

	allArgs := make([]string, 0, len(cmdInfo.Args)+len(args))
	allArgs = append(allArgs, cmdInfo.Args...)
	allArgs = append(allArgs, args...)

	cmd := exec.CommandContext(ctx, cmdInfo.Command, allArgs...) //nolint:gosec // G204: command path comes from the trusted managed store and args from validated config
	cmd.Env = mergeExecEnv(os.Environ(), cmdInfo.Env)

	return cmd, nil
}

// Exec runs application as child process with environment variables passed through
func (bm *BinManager) Exec(ctx context.Context, appName string, args []string) error {
	cmdInfo, err := bm.GetCommandInfo(ctx, appName)
	if err != nil {
		return fmt.Errorf("failed to get command info for %s: %w", appName, err)
	}

	allArgs := make([]string, 0, len(cmdInfo.Args)+len(args))
	allArgs = append(allArgs, cmdInfo.Args...)
	allArgs = append(allArgs, args...)

	cmd := exec.CommandContext(ctx, cmdInfo.Command, allArgs...) //nolint:gosec // G204: command path comes from the trusted managed store and args from validated config

	log.Debug("executing app",
		zap.String("name", appName),
		zap.String("type", cmdInfo.Type),
		zap.String("command", cmdInfo.Command),
		zap.Strings("args", allArgs),
	)

	cmd.Env = mergeExecEnv(os.Environ(), cmdInfo.Env)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to execute %s: %w", appName, err)
	}

	return nil
}

// ExecCaptured runs a managed app like Exec but captures combined stdout+stderr
// into the returned string instead of streaming to the terminal. Callers use it
// to keep clean output on success and surface the captured text only on failure.
func (bm *BinManager) ExecCaptured(ctx context.Context, appName string, args []string) (string, error) {
	cmdInfo, err := bm.GetCommandInfo(ctx, appName)
	if err != nil {
		return "", fmt.Errorf("failed to get command info for %s: %w", appName, err)
	}

	allArgs := make([]string, 0, len(cmdInfo.Args)+len(args))
	allArgs = append(allArgs, cmdInfo.Args...)
	allArgs = append(allArgs, args...)

	cmd := exec.CommandContext(ctx, cmdInfo.Command, allArgs...) //nolint:gosec // G204: command path comes from the trusted managed store and args from validated config
	cmd.Env = mergeExecEnv(os.Environ(), cmdInfo.Env)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Run(); err != nil {
		return buf.String(), fmt.Errorf("failed to execute %s: %w", appName, err)
	}

	return buf.String(), nil
}

// BinaryAvailable reports whether app can run on the current host. It only
// resolves (no download): non-binary apps (shell/uv/node/jvm/go) always report
// available since they have their own fallback paths; a binary app reports
// unavailable, with the host string as detail, when no build matches this
// os/arch/libc. Implements tooling.PlatformChecker, so the planner can mark
// platform-unsupported tools skipped instead of letting install hard-fail.
func (bm *BinManager) BinaryAvailable(name string) (available bool, detail string) {
	app, ok := bm.mapOfApps[name]
	if !ok {
		// Plans only reference real apps; treat an unknown name as available so
		// this seam never fabricates a skip for a missing registry entry.
		return true, ""
	}
	if app.Binary == nil {
		return true, ""
	}
	if _, _, err := bm.getBinaryInfo(name); err != nil {
		return false, bm.resolver.Host().String()
	}
	return true, ""
}

func (bm *BinManager) getBinaryInfo(name string) (*target.ResolvedTarget, BinaryOsArchInfo, error) {
	app, ok := bm.mapOfApps[name]
	if !ok {
		return nil, BinaryOsArchInfo{}, fmt.Errorf("binary '%s' not found in registry", name)
	}

	if app.Binary == nil {
		return nil, BinaryOsArchInfo{}, fmt.Errorf("app '%s' is not a binary type", name)
	}

	candidates := parseBinaryCandidates(app.Binary.Binaries)
	resolved, info := bm.resolver.Resolve(name, candidates)
	if resolved == nil {
		host := bm.resolver.Host()
		return nil, BinaryOsArchInfo{}, fmt.Errorf("binary '%s' is not available for %s", name, host.String())
	}

	if resolved.Source == target.ResolutionFallback && resolved.FallbackInfo != nil {
		warning := target.FallbackWarning(name, *resolved)
		if warning != "" {
			log.Debug(warning)
		}
	}

	binInfo, ok := info.(*BinaryOsArchInfo)
	if !ok {
		return nil, BinaryOsArchInfo{}, fmt.Errorf("binary '%s' resolved to unexpected info type %T", name, info)
	}

	return resolved, *binInfo, nil
}

func (bm *BinManager) getBinaryPath(name string) (string, error) {
	resolved, binaryInfo, err := bm.getBinaryInfo(name)
	if err != nil {
		return "", err
	}

	configHash := calculateConfigHash(binaryInfo, *resolved)

	binPath := filepath.Join(env.GetBinPath(), name, configHash)
	return binPath, nil
}

func (bm *BinManager) downloadInternal(ctx context.Context, name string) error {
	networkDownloads.Add(1)

	resolved, binaryInfo, err := bm.getBinaryInfo(name)
	if err != nil {
		return err
	}

	hashType := defaultBinHashType
	if binaryInfo.HashType != nil {
		hashType = *binaryInfo.HashType
	}

	log.Debug("downloading binary",
		zap.String("name", name),
		zap.String("url", binaryInfo.URL),
	)

	configHash := calculateConfigHash(binaryInfo, *resolved)
	binPath := filepath.Join(env.GetBinPath(), name, configHash)

	tmpDir := filepath.Join(env.GetStorePath(), "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	downloadedPath, err := downloadAndVerifyWithName(ctx, binaryInfo.URL, binaryInfo.Hash, hashType, tmpDir, name)
	if err != nil {
		return fmt.Errorf("failed to download and verify: %w", err)
	}
	defer func() {
		if err := os.Remove(downloadedPath); err != nil {
			log.Warn("failed to remove downloaded file", zap.String("path", downloadedPath), zap.Error(err))
		}
	}()

	if binaryInfo.ExtractDir {
		// Directory archives (runtimes like node) are large and slow to unpack —
		// xz/gzip decompression plus thousands of files — and this happens AFTER
		// the download bar completes. Show a spinner so the wait is not dead air.
		sp := ui.Current().Spinner("Extracting " + name + "…")

		extractedDir, err := extractBinaryToDir(downloadedPath, binaryInfo.ContentType, tmpDir)
		if err != nil {
			sp.Fail()
			return fmt.Errorf("failed to extract archive to directory: %w", err)
		}

		if err := moveDir(extractedDir, binPath); err != nil {
			sp.Fail()
			_ = os.RemoveAll(extractedDir)
			return fmt.Errorf("failed to move extracted directory to cache: %w", err)
		}

		sp.Done("")
	} else {
		extractedPath, err := extractBinary(downloadedPath, binaryInfo.ContentType, binaryInfo.BinaryPath, tmpDir)
		if err != nil {
			return fmt.Errorf("failed to extract binary: %w", err)
		}

		if err := moveFile(extractedPath, binPath); err != nil {
			return fmt.Errorf("failed to move binary to cache: %w", err)
		}
	}

	log.Debug("binary installed successfully",
		zap.String("name", name),
		zap.String("path", binPath),
	)

	return nil
}

// downloadWithTimeout installs one app under a per-app install timeout context,
// translating a deadline into a clear timeout error. Progress (a bar in an
// interactive terminal, throttled lines in CI) is rendered through the shared
// ui display by the download layer.
func (bm *BinManager) downloadWithTimeout(ctx context.Context, name string) error {
	ctx, cancel, timeoutSec := newInstallContext(ctx)
	defer cancel()
	return wrapInstallTimeout(bm.downloadInternal(ctx, name), timeoutSec)
}

func (bm *BinManager) installInternal(ctx context.Context, includeOptional bool) error {
	for name := range bm.mapOfApps {
		if bm.mapOfApps[name].Binary == nil {
			continue
		}

		if !includeOptional && !bm.mapOfApps[name].Required {
			log.Debug("skipping optional binary", zap.String("name", name))
			continue
		}

		binPath, err := bm.getBinaryPath(name)
		if err != nil {
			return fmt.Errorf("failed to get binary path for %s: %w", name, err)
		}

		if _, err := os.Stat(binPath); err == nil {
			log.Debug("binary already cached, skipping", zap.String("name", name), zap.String("path", binPath))
			continue
		}

		if err := bm.downloadWithTimeout(ctx, name); err != nil {
			return fmt.Errorf("failed to install %s: %w", name, err)
		}
	}

	return nil
}

// mergeAppEnv merges the app's user-defined Env into cmdInfo.Env, expanding
// ${STORE}/${APP_DIR} placeholders. Keys already set by datamitsu/the runtime
// take precedence — a user config can never override a reserved runtime key.
func (bm *BinManager) mergeAppEnv(appName string, app App, cmdInfo *CommandInfo) {
	if len(app.Env) == 0 {
		return
	}

	// ${APP_DIR} is best-effort: if the install path can't be computed, appDir
	// is empty and ${APP_DIR} expands to "" rather than failing the command.
	appDir, _ := bm.ComputeInstallPath(appName)

	if cmdInfo.Env == nil {
		cmdInfo.Env = make(map[string]string, len(app.Env))
	}

	for k, v := range app.Env {
		if _, exists := cmdInfo.Env[k]; exists {
			continue
		}
		cmdInfo.Env[k] = env.ExpandPlaceholders(v, appDir)
	}
}

// mergeExecEnv merges base environment variables with app-specific overrides.
// Uses a map index for O(1) key lookups instead of O(n) linear scans.
func mergeExecEnv(base []string, appEnv map[string]string) []string {
	env := make([]string, 0, len(base)+len(appEnv))
	env = append(env, base...)

	keyToIdx := make(map[string]int, len(env))
	for i, e := range env {
		if j := strings.IndexByte(e, '='); j > 0 {
			keyToIdx[e[:j]] = i
		}
	}

	for key, value := range appEnv {
		envVar := fmt.Sprintf("%s=%s", key, value)
		if idx, ok := keyToIdx[key]; ok {
			env[idx] = envVar
		} else {
			keyToIdx[key] = len(env)
			env = append(env, envVar)
		}
	}

	return env
}
