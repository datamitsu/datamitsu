package binmanager

import (
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/vbauerster/mpb/v8"
	"go.uber.org/zap"
)

var log = logger.Logger.With(zap.Namespace("binmanager"))

type MapOfBinaries = map[syslist.OsType]map[syslist.ArchType]map[string]BinaryOsArchInfo

type MapOfApps = map[string]App

type AppConfigBinary struct {
	Binaries MapOfBinaries `json:"binaries"`
	Version  string        `json:"version,omitempty"`
}

type AppConfigUV struct {
	PackageName    string `json:"packageName"`
	Version        string `json:"version"`
	Runtime        string `json:"runtime,omitempty"`
	LockFile       string `json:"lockFile,omitempty"`
	RequiresPython string `json:"requiresPython,omitempty"`
}

type AppConfigFNM struct {
	PackageName  string            `json:"packageName"`
	Version      string            `json:"version"`
	BinPath      string            `json:"binPath"`
	Runtime      string            `json:"runtime,omitempty"`
	LockFile     string            `json:"lockFile,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

type AppConfigJVM struct {
	JarURL  string `json:"jarUrl"`
	JarHash string `json:"jarHash"`
	Version string `json:"version"`
	Runtime string `json:"runtime,omitempty"`
	MainClass string `json:"mainClass,omitempty"`
}

type AppConfigShell struct {
	Name string            `json:"name"`
	Args []string          `json:"args,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

type AppVersionCheck struct {
	Disabled bool     `json:"disabled,omitempty"`
	Args     []string `json:"args,omitempty"`
}

type App struct {
	// Required binary (downloaded during Install())
	// Optional binaries are downloaded only on first access via GetBinaryPath()
	Required bool `json:"required,omitempty"`

	Description  string           `json:"description,omitempty"`
	VersionCheck *AppVersionCheck `json:"versionCheck,omitempty"`

	Binary *AppConfigBinary `json:"binary,omitempty"`
	Uv     *AppConfigUV     `json:"uv,omitempty"`
	Fnm    *AppConfigFNM    `json:"fnm,omitempty"`
	Jvm    *AppConfigJVM    `json:"jvm,omitempty"`
	Shell  *AppConfigShell  `json:"shell,omitempty"`

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

func (a *ArchiveSpec) IsInline() bool {
	return a.Inline != "" && a.URL == ""
}

func (a *ArchiveSpec) IsExternal() bool {
	return a.URL != "" && a.Inline == ""
}

// RuntimeAppManager handles runtime-managed applications (uv, fnm).
// Implemented by runtimemanager.RuntimeManager to avoid circular imports.
type RuntimeAppManager interface {
	GetCommandInfo(appName string, app App) (*CommandInfo, error)
	ComputeAppPath(appName string, app App) (string, error)
}

type BinManager struct {
	mapOfApps      MapOfApps
	mapOfBundles   MapOfBundles
	runtimeManager RuntimeAppManager
	resolver       *target.Resolver
}

func New(mapOfApps MapOfApps, mapOfBundles MapOfBundles, runtimeManager RuntimeAppManager) *BinManager {
	return &BinManager{
		mapOfApps:      mapOfApps,
		mapOfBundles:   mapOfBundles,
		runtimeManager: runtimeManager,
		resolver:       target.NewResolver(target.DetectHost()),
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
			log.Warn(warning)
		}
	}

	return resolved, *info.(*BinaryOsArchInfo), nil
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

func (bm *BinManager) downloadInternal(name string, progress *mpb.Progress) error {
	resolved, binaryInfo, err := bm.getBinaryInfo(name)
	if err != nil {
		return err
	}

	var hashType = defaultBinHashType
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
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	var downloadedPath string
	if progress != nil {
		downloadedPath, err = downloadAndVerifyWithProgress(binaryInfo.URL, binaryInfo.Hash, hashType, tmpDir, name, progress)
	} else {
		downloadedPath, err = downloadAndVerify(binaryInfo.URL, binaryInfo.Hash, hashType, tmpDir)
	}
	if err != nil {
		return fmt.Errorf("failed to download and verify: %w", err)
	}
	defer func() {
		if err := os.Remove(downloadedPath); err != nil {
			log.Warn("failed to remove downloaded file", zap.String("path", downloadedPath), zap.Error(err))
		}
	}()

	if binaryInfo.ExtractDir {
		extractedDir, err := extractBinaryToDir(downloadedPath, binaryInfo.ContentType, tmpDir)
		if err != nil {
			return fmt.Errorf("failed to extract archive to directory: %w", err)
		}

		if err := moveDir(extractedDir, binPath); err != nil {
			_ = os.RemoveAll(extractedDir)
			return fmt.Errorf("failed to move extracted directory to cache: %w", err)
		}
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

func (bm *BinManager) download(name string) error {
	return bm.downloadInternal(name, nil)
}

func (bm *BinManager) downloadWithProgress(name string, progress *mpb.Progress) error {
	return bm.downloadInternal(name, progress)
}

type DownloadResult struct {
	Name  string
	Error error
}

type InstallStats struct {
	Skipped       []string
	AlreadyCached []string
	Downloaded    []string
	Failed        []DownloadResult
}

func (bm *BinManager) installInternal(includeOptional bool) error {
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

		if err := bm.download(name); err != nil {
			return fmt.Errorf("failed to install %s: %w", name, err)
		}
	}

	return nil
}

// InstallWithConcurrency installs binaries with specified concurrency level
// Returns installation statistics
func (bm *BinManager) InstallWithConcurrency(includeOptional bool, concurrency int, failOnError bool) (InstallStats, error) {
	stats := InstallStats{
		Skipped:       []string{},
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
			stats.Skipped = append(stats.Skipped, name)
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

	progress := mpb.New(mpb.WithWidth(60))

	jobs := make(chan string, len(toDownload))
	results := make(chan DownloadResult, len(toDownload))

	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range jobs {
				err := bm.downloadWithProgress(name, progress)
				results <- DownloadResult{
					Name:  name,
					Error: err,
				}

				if failOnError && err != nil {
					return
				}
			}
		}()
	}

	for _, name := range toDownload {
		jobs <- name
	}
	close(jobs)

	wg.Wait()
	close(results)

	progress.Wait()

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
func (bm *BinManager) Install() error {
	return bm.installInternal(false)
}

// GetBinaryPath returns the path to a binary, downloading it if necessary (lazy loading)
func (bm *BinManager) GetBinaryPath(name string) (string, error) {
	binPath, err := bm.getBinaryPath(name)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(binPath); err == nil {
		log.Debug("binary found in cache", zap.String("name", name), zap.String("path", binPath))
		return binPath, nil
	}

	log.Debug("binary not found in cache, downloading", zap.String("name", name))

	fmt.Fprintf(os.Stderr, "⬇️  Downloading %s...\n", name)

	if err := bm.download(name); err != nil {
		return "", fmt.Errorf("failed to download %s: %w", name, err)
	}

	fmt.Fprintf(os.Stderr, "✅ Downloaded %s\n", name)

	return binPath, nil
}

// GetCommandInfo returns command information for executing an application
// Works with all application types: binary, shell, uv, fnm
func (bm *BinManager) GetCommandInfo(appName string) (*CommandInfo, error) {
	app, ok := bm.mapOfApps[appName]
	if !ok {
		return nil, fmt.Errorf("app '%s' not found in registry", appName)
	}

	if app.Shell != nil {
		return &CommandInfo{
			Type:    "shell",
			Command: app.Shell.Name,
			Args:    app.Shell.Args,
			Env:     app.Shell.Env,
		}, nil
	}

	if app.Binary != nil {
		binPath, err := bm.GetBinaryPath(appName)
		if err != nil {
			return nil, err
		}
		return &CommandInfo{
			Type:    "binary",
			Command: binPath,
		}, nil
	}

	if app.Uv != nil || app.Fnm != nil || app.Jvm != nil {
		if bm.runtimeManager == nil {
			return nil, fmt.Errorf("no runtime manager configured for runtime-managed app %q", appName)
		}
		return bm.runtimeManager.GetCommandInfo(appName, app)
	}

	return nil, fmt.Errorf("app '%s' has no valid configuration", appName)
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

	if app.Uv != nil || app.Fnm != nil || app.Jvm != nil {
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
func WriteAppFiles(installPath string, files map[string]string, archives map[string]*ArchiveSpec) error {
	if err := os.MkdirAll(installPath, 0755); err != nil {
		return fmt.Errorf("failed to create install directory: %w", err)
	}

	if len(archives) > 0 {
		if err := extractArchives(installPath, archives); err != nil {
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
func extractArchives(installPath string, archives map[string]*ArchiveSpec) error {
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

		if spec.IsInline() {
			tarData, err := DecompressArchive(spec.Inline)
			if err != nil {
				return fmt.Errorf("archive %q: failed to decompress inline archive: %w", name, err)
			}

			if _, err := extractArchiveToPath(installPath, tarData, "", BinContentTypeTar); err != nil {
				return fmt.Errorf("archive %q: failed to extract inline archive: %w", name, err)
			}

			log.Debug("extracted inline archive", zap.String("name", name), zap.String("dest", installPath))

		} else if spec.IsExternal() {
			if err := downloadAndExtractExternalArchive(name, spec, installPath); err != nil {
				return err
			}

		} else {
			return fmt.Errorf("archive %q: must have either inline or url field set", name)
		}
	}

	return nil
}

func downloadAndExtractExternalArchive(name string, spec *ArchiveSpec, installPath string) error {
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

	if err := downloadFileSimple(spec.URL, tmpPath); err != nil {
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
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create directory for file %q: %w", filename, err)
			}
		}
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write file %q: %w", filename, err)
		}
	}
	return nil
}

func downloadFileSimple(url, destPath string) error {
	resp, err := httpClient.Get(url)
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
	Type    string            // "binary", "shell", "uv", "fnm"
	Command string            // Path to binary or command name
	Args    []string          // Additional arguments (for shell)
	Env     map[string]string // Additional env variables (for shell)
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

		if app.Binary != nil {
			info.Type = "binary"
			info.Version = app.Binary.Version
		} else if app.Uv != nil {
			info.Type = "uv"
			info.Version = app.Uv.Version
			info.PackageName = app.Uv.PackageName
		} else if app.Fnm != nil {
			info.Type = "fnm"
			info.Version = app.Fnm.Version
			info.PackageName = app.Fnm.PackageName
		} else if app.Jvm != nil {
			info.Type = "jvm"
			info.Version = app.Jvm.Version
		} else if app.Shell != nil {
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
func (bm *BinManager) GetExecCmd(name string, args []string) (*exec.Cmd, error) {
	app, ok := bm.mapOfApps[name]
	if !ok {
		return nil, fmt.Errorf("app '%s' not found in registry", name)
	}

	if app.Shell != nil {
		return nil, nil
	}

	cmdInfo, err := bm.GetCommandInfo(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get command info for %s: %w", name, err)
	}

	allArgs := make([]string, 0, len(cmdInfo.Args)+len(args))
	allArgs = append(allArgs, cmdInfo.Args...)
	allArgs = append(allArgs, args...)

	cmd := exec.Command(cmdInfo.Command, allArgs...)
	cmd.Env = mergeExecEnv(os.Environ(), cmdInfo.Env)

	return cmd, nil
}

// Exec runs application as child process with environment variables passed through
func (bm *BinManager) Exec(appName string, args []string) error {
	cmdInfo, err := bm.GetCommandInfo(appName)
	if err != nil {
		return fmt.Errorf("failed to get command info for %s: %w", appName, err)
	}

	allArgs := make([]string, 0, len(cmdInfo.Args)+len(args))
	allArgs = append(allArgs, cmdInfo.Args...)
	allArgs = append(allArgs, args...)

	cmd := exec.Command(cmdInfo.Command, allArgs...)

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

// mergeExecEnv merges base environment variables with app-specific overrides.
// Uses a map index for O(1) key lookups instead of O(n) linear scans.
func mergeExecEnv(base []string, appEnv map[string]string) []string {
	env := make([]string, len(base))
	copy(env, base)

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
