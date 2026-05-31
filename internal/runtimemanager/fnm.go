package runtimemanager

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/pnpmdefaults"
	"github.com/datamitsu/datamitsu/internal/target"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/goccy/go-yaml"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

var fnmHTTPClient = &http.Client{
	Timeout: 5 * time.Minute,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme == "http" {
			return fmt.Errorf("HTTPS to HTTP redirect rejected: %s", req.URL)
		}
		return nil
	},
}

const maxPNPMDownloadSize = 100 * 1024 * 1024   // 100 MiB
const maxTotalExtractedSize = 500 * 1024 * 1024 // 500 MiB

func getFNMEnvVars(appEnvPath string) map[string]string {
	storePath := env.GetPNPMStorePath()
	return map[string]string{
		"npm_config_store_dir":         storePath,
		"npm_config_virtual_store_dir": filepath.Join(appEnvPath, "node_modules", ".pnpm"),
		"npm_config_global_dir":        filepath.Join(appEnvPath, "global"),
	}
}

type npmVersionMeta struct {
	Dist struct {
		Tarball   string `json:"tarball"`
		Shasum    string `json:"shasum"`
		Integrity string `json:"integrity"`
	} `json:"dist"`
}

func (rm *RuntimeManager) installPNPM(version string, destDir string, pnpmHash string) error {
	key := version + "\x00" + pnpmHash
	_, err, _ := rm.pnpmInstall.Do(key, func() (any, error) {
		return nil, rm.downloadPNPMFromRegistry(version, destDir, pnpmHash)
	})
	return err
}

func (rm *RuntimeManager) downloadPNPMFromRegistry(version string, destDir string, pnpmHash string) error {
	if pnpmHash == "" {
		return fmt.Errorf("PNPM hash is required but not provided for pnpm@%s", version)
	}

	pnpmCjsPath := filepath.Join(destDir, "package", "bin", "pnpm.cjs")
	if _, err := os.Stat(pnpmCjsPath); err == nil {
		return nil
	}

	url := fmt.Sprintf("https://registry.npmjs.org/pnpm/%s", version)
	resp, err := fnmHTTPClient.Get(url)
	if err != nil {
		return fmt.Errorf("failed to fetch PNPM metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("npm registry returned status %d for pnpm@%s", resp.StatusCode, version)
	}

	var meta npmVersionMeta
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&meta); err != nil {
		return fmt.Errorf("failed to decode PNPM metadata: %w", err)
	}

	if meta.Dist.Tarball == "" {
		return fmt.Errorf("no tarball URL found for pnpm@%s", version)
	}
	if meta.Dist.Integrity == "" || !strings.HasPrefix(meta.Dist.Integrity, "sha512-") {
		return fmt.Errorf("pnpm@%s: SHA-512 integrity required but not found in registry metadata", version)
	}

	tarResp, err := fnmHTTPClient.Get(meta.Dist.Tarball)
	if err != nil {
		return fmt.Errorf("failed to download PNPM tarball: %w", err)
	}
	defer func() { _ = tarResp.Body.Close() }()

	if tarResp.StatusCode != http.StatusOK {
		return fmt.Errorf("pnpm tarball download returned status %d", tarResp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "pnpm-*.tgz")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	sha256Hasher := sha256.New()
	sha512Hasher := sha512.New()
	writer := io.MultiWriter(tmpFile, sha256Hasher, sha512Hasher)
	limitedBody := io.LimitReader(tarResp.Body, maxPNPMDownloadSize+1)

	written, err := io.Copy(writer, limitedBody)
	if err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to download PNPM tarball: %w", err)
	}
	_ = tmpFile.Close()

	if written > maxPNPMDownloadSize {
		return fmt.Errorf("pnpm tarball exceeds maximum size of %d bytes", maxPNPMDownloadSize)
	}

	if err := verifyPNPMPinnedHash(pnpmHash, sha256Hasher.Sum(nil)); err != nil {
		return err
	}

	if err := verifyPNPMIntegrity(meta, sha512Hasher.Sum(nil)); err != nil {
		return err
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create PNPM directory: %w", err)
	}

	if err := extractFullTgz(tmpPath, destDir); err != nil {
		_ = os.RemoveAll(destDir)
		return fmt.Errorf("failed to extract PNPM tarball: %w", err)
	}

	return nil
}

func extractFullTgz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	var totalExtracted int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, hdr.Name)

		cleanDest := filepath.Clean(destDir) + string(filepath.Separator)
		cleanTarget := filepath.Clean(target)
		if cleanTarget != filepath.Clean(destDir) && !strings.HasPrefix(cleanTarget, cleanDest) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0777)
			if err != nil {
				return err
			}
			written, copyErr := io.Copy(outFile, io.LimitReader(tr, maxPNPMDownloadSize+1))
			_ = outFile.Close()
			if copyErr != nil {
				return copyErr
			}
			if written > maxPNPMDownloadSize {
				return fmt.Errorf("tar entry %q exceeds maximum size of %d bytes", hdr.Name, maxPNPMDownloadSize)
			}
			totalExtracted += written
			if totalExtracted > maxTotalExtractedSize {
				return fmt.Errorf("total extracted size exceeds maximum of %d bytes", maxTotalExtractedSize)
			}
		case tar.TypeSymlink:
			linkTarget := hdr.Linkname
			if filepath.IsAbs(linkTarget) {
				continue
			}
			resolvedTarget := filepath.Clean(filepath.Join(filepath.Dir(cleanTarget), linkTarget))
			if resolvedTarget != filepath.Clean(destDir) && !strings.HasPrefix(resolvedTarget, cleanDest) {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := os.Symlink(linkTarget, target); err != nil {
				return err
			}
		}
	}
	return nil
}

// verifyPNPMPinnedHash verifies the downloaded PNPM tarball against the
// pinned SHA-256 hash from configuration. This is the primary security check
// per the project's security policy: all downloads must be verified against
// a pinned hash, not against untrusted registry-provided metadata.
func verifyPNPMPinnedHash(expectedHash string, actualSHA256 []byte) error {
	if expectedHash == "" {
		return fmt.Errorf("pnpm tarball SHA-256 hash is required but not configured")
	}
	actualHex := hex.EncodeToString(actualSHA256)
	if actualHex != expectedHash {
		return fmt.Errorf("pnpm tarball SHA-256 hash mismatch: expected %q, got %q", expectedHash, actualHex)
	}
	return nil
}

// verifyPNPMIntegrity checks the downloaded tarball against the npm registry
// SHA-512 integrity metadata (SRI format). SHA-1 fallback is not supported.
func verifyPNPMIntegrity(meta npmVersionMeta, sha512Sum []byte) error {
	if meta.Dist.Integrity == "" || !strings.HasPrefix(meta.Dist.Integrity, "sha512-") {
		return fmt.Errorf("SHA-512 integrity required but not found in registry metadata")
	}

	expectedB64 := strings.TrimPrefix(meta.Dist.Integrity, "sha512-")
	expectedHash, err := base64.StdEncoding.DecodeString(expectedB64)
	if err != nil {
		return fmt.Errorf("failed to decode integrity hash: %w", err)
	}
	actualB64 := base64.StdEncoding.EncodeToString(sha512Sum)
	expectedB64Normalized := base64.StdEncoding.EncodeToString(expectedHash)
	if actualB64 != expectedB64Normalized {
		return fmt.Errorf("pnpm tarball SHA-512 integrity mismatch: expected %q, got %q", meta.Dist.Integrity, "sha512-"+actualB64)
	}
	return nil
}

func (rm *RuntimeManager) installNodeVersion(fnmPath, nodeVersion, cacheRoot string) error {
	_, err, _ := rm.nodeInstall.Do(nodeVersion, func() (any, error) {
		return nil, rm.installNodeVersionOnce(fnmPath, nodeVersion, cacheRoot)
	})
	return err
}

// fnmMuslNodeDistMirror is the unofficial-builds mirror that publishes
// musl-linked Node.js binaries. The default fnm mirror (nodejs.org) only ships
// glibc builds, which cannot execute on musl hosts (Alpine Linux).
const fnmMuslNodeDistMirror = "https://unofficial-builds.nodejs.org/download/release"

// muslFNMArch maps a Go architecture (GOARCH) to the FNM_ARCH token used by the
// unofficial-builds musl mirror. It returns "" for architectures that have no
// published musl build, signaling that fnm should stay on its default mirror.
func muslFNMArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64-musl"
	case "arm64":
		return "arm64-musl"
	default:
		return ""
	}
}

// buildFNMInstallEnv builds the environment overrides applied to the `fnm
// install` child process. It always sets FNM_DIR. On musl hosts with a
// supported architecture it additionally points fnm at the unofficial-builds
// musl mirror (FNM_NODE_DIST_MIRROR) and selects the musl arch token
// (FNM_ARCH) so the downloaded Node.js binary can actually execute. Each of
// those two vars is injected only when the user has not already set it (checked
// via os.LookupEnv), so an explicit override always wins. glibc hosts,
// unsupported arches, and LibcUnknown hosts get FNM_DIR alone, leaving fnm on
// its default mirror.
func buildFNMInstallEnv(host target.Target, fnmDir string) map[string]string {
	envOverrides := map[string]string{"FNM_DIR": fnmDir}

	if host.Libc != target.LibcMusl {
		return envOverrides
	}
	muslArch := muslFNMArch(host.Arch)
	if muslArch == "" {
		return envOverrides
	}

	if _, ok := os.LookupEnv("FNM_NODE_DIST_MIRROR"); !ok {
		envOverrides["FNM_NODE_DIST_MIRROR"] = fnmMuslNodeDistMirror
	}
	if _, ok := os.LookupEnv("FNM_ARCH"); !ok {
		envOverrides["FNM_ARCH"] = muslArch
	}
	return envOverrides
}

// fnmInstallEnv computes the env overrides for the `fnm install` child process
// for this host and emits an Info log when datamitsu auto-configures the musl
// Node.js mirror. The log fires only when datamitsu injected BOTH the
// mirror and the arch (i.e. the user supplied neither) so the message never
// reports a value the user already controls; whenever the user has set either
// FNM_NODE_DIST_MIRROR or FNM_ARCH themselves the override is left to them and
// no log is emitted.
func (rm *RuntimeManager) fnmInstallEnv(fnmDir string) map[string]string {
	overrides := buildFNMInstallEnv(rm.hostTarget, fnmDir)
	mirror, hasMirror := overrides["FNM_NODE_DIST_MIRROR"]
	arch, hasArch := overrides["FNM_ARCH"]
	if hasMirror && hasArch {
		log.Info("configuring fnm for musl Node.js builds",
			zap.String("mirror", mirror),
			zap.String("arch", arch),
		)
	}
	return overrides
}

func (rm *RuntimeManager) installNodeVersionOnce(fnmPath, nodeVersion, cacheRoot string) error {
	nodeBinPath := env.GetNodeBinaryPath(cacheRoot, nodeVersion)

	if _, err := os.Stat(nodeBinPath); err == nil {
		log.Debug("Node.js already installed",
			zap.String("version", nodeVersion),
			zap.String("path", nodeBinPath),
		)
		return nil
	}

	fnmDir := filepath.Join(cacheRoot, ".runtimes", "fnm-nodes")
	if err := os.MkdirAll(fnmDir, 0755); err != nil {
		return fmt.Errorf("failed to create FNM directory: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Installing Node.js %s...\n", nodeVersion)

	cmd := exec.Command(fnmPath, "install", nodeVersion)
	cmd.Env = buildEnvWithOverrides(os.Environ(), rm.fnmInstallEnv(fnmDir))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install Node.js %s via FNM: %w", nodeVersion, err)
	}

	// FNM stores at fnmDir/node-versions/v{version}/installation/...
	// Move to fnmDir/v{version}/installation/... to match GetNodeBinaryPath
	fnmVersionDir := filepath.Join(fnmDir, "node-versions", "v"+nodeVersion)
	expectedDir := filepath.Join(fnmDir, "v"+nodeVersion)

	if _, err := os.Stat(fnmVersionDir); err == nil {
		if err := os.Rename(fnmVersionDir, expectedDir); err != nil {
			if copyErr := copyDir(fnmVersionDir, expectedDir); copyErr != nil {
				return fmt.Errorf("failed to move Node.js installation: rename: %w, copy: %w", err, copyErr)
			}
			_ = os.RemoveAll(fnmVersionDir)
		}
		_ = os.Remove(filepath.Join(fnmDir, "node-versions"))
	}

	if _, err := os.Stat(nodeBinPath); err != nil {
		return fmt.Errorf("node.js binary not found at %q after installation", nodeBinPath)
	}

	fmt.Fprintf(os.Stderr, "Installed Node.js %s\n", nodeVersion)

	return nil
}

func (rm *RuntimeManager) resolveFNMAppEnvPath(appName string, appConfig *binmanager.AppConfigFNM, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (appEnvPath string, runtimeName string, rc config.RuntimeConfig, err error) {
	runtimeName, rc, err = rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindFNM)
	if err != nil {
		return "", "", config.RuntimeConfig{}, fmt.Errorf("failed to resolve FNM runtime for %q: %w", appName, err)
	}

	// Hash the actual merged pnpm-workspace.yaml content (defaults + user
	// override) rather than only the user's raw override. This way the cache
	// key invalidates when pnpmdefaults.Defaults() is tightened, so the
	// new defaults propagate to existing installs.
	filesForHash, err := filesWithMergedWorkspaceYAML(files)
	if err != nil {
		return "", "", config.RuntimeConfig{}, fmt.Errorf("failed to compute pnpm-workspace.yaml for %q: %w", appName, err)
	}

	appEnvPath, err = rm.GetAppPath(appName, config.RuntimeKindFNM, appConfig.Version, appConfig.Dependencies, lockFileHash(appConfig.LockFile), filesForHash, archives, runtimeName, FNMAppPathExtra{
		PackageName: appConfig.PackageName,
		BinPath:     appConfig.BinPath,
	})
	if err != nil {
		return "", "", config.RuntimeConfig{}, err
	}

	return appEnvPath, runtimeName, rc, nil
}

// filesWithMergedWorkspaceYAML returns a copy of files where the
// pnpm-workspace.yaml entry is replaced by the actual merged content
// (defaults + user override). The original files map is not mutated.
// This is used only for cache-key computation; the real merge for writing
// to disk is performed by preparePNPMWorkspaceForApp.
func filesWithMergedWorkspaceYAML(files map[string]string) (map[string]string, error) {
	merged, err := buildPNPMWorkspaceForApp(files)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(files)+1)
	for k, v := range files {
		out[k] = v
	}
	out["pnpm-workspace.yaml"] = merged
	return out, nil
}

// InstallFNMApp installs an FNM-managed app if not already cached.
// If files is non-empty, writes them to the app directory before running pnpm.
// Safe for concurrent use from multiple goroutines.
func (rm *RuntimeManager) InstallFNMApp(appName string, appConfig *binmanager.AppConfigFNM, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	key := "fnm/" + appName
	_, err, _ := rm.appInstall.Do(key, func() (any, error) {
		return nil, rm.installFNMAppOnce(appName, appConfig, files, archives)
	})
	return err
}

func (rm *RuntimeManager) installFNMAppOnce(appName string, appConfig *binmanager.AppConfigFNM, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	appEnvPath, runtimeName, rc, err := rm.resolveFNMAppEnvPath(appName, appConfig, files, archives)
	if err != nil {
		return err
	}

	if rc.FNM == nil {
		return fmt.Errorf("runtime for %q has no FNM config (nodeVersion/pnpmVersion)", appName)
	}
	nodeVersion := rc.FNM.NodeVersion
	pnpmVersion := rc.FNM.PNPMVersion
	pnpmHash := rc.FNM.PNPMHash

	if err := validateRelativePath(appConfig.BinPath); err != nil {
		return fmt.Errorf("app %q: unsafe binPath: %w", appName, err)
	}
	appBinPath := filepath.Join(appEnvPath, appConfig.BinPath)
	appModulePkg := filepath.Join(appEnvPath, "node_modules", appConfig.PackageName, "package.json")
	if _, err := os.Stat(appBinPath); err == nil {
		if _, err := os.Stat(appModulePkg); err == nil {
			log.Debug("FNM app already installed",
				zap.String("app", appName),
				zap.String("path", appBinPath),
			)
			return nil
		}
		log.Warn("FNM app bin shim exists but module is missing, reinstalling",
			zap.String("app", appName),
			zap.String("module", appModulePkg),
		)
		_ = os.RemoveAll(appEnvPath)
	}

	fnmPath, err := rm.GetRuntimePath(runtimeName)
	if err != nil {
		return fmt.Errorf("failed to get FNM runtime path: %w", err)
	}

	storeRoot := env.GetStorePath()

	if err := rm.installNodeVersion(fnmPath, nodeVersion, storeRoot); err != nil {
		return err
	}

	pnpmDir := filepath.Join(storeRoot, ".runtimes", "fnm-pnpm", pnpmVersion, pnpmHash)
	if err := rm.installPNPM(pnpmVersion, pnpmDir, pnpmHash); err != nil {
		return fmt.Errorf("failed to download PNPM: %w", err)
	}

	if err := os.MkdirAll(appEnvPath, 0755); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}

	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(appEnvPath)
		}
	}()

	mergedWorkspaceYAML, filesToWrite, err := preparePNPMWorkspaceForApp(files)
	if err != nil {
		return fmt.Errorf("failed to set up pnpm-workspace.yaml for %q: %w", appName, err)
	}

	if len(filesToWrite) > 0 || len(archives) > 0 {
		if err := binmanager.WriteAppFiles(appEnvPath, filesToWrite, archives); err != nil {
			return fmt.Errorf("failed to write app files/archives for %q: %w", appName, err)
		}
	}

	// Write pnpm-workspace.yaml AFTER WriteAppFiles so the secure defaults
	// always win over anything an archive might place at this path.
	if err := writeFNMAppWorkspaceFile(appEnvPath, mergedWorkspaceYAML); err != nil {
		return fmt.Errorf("failed to write pnpm-workspace.yaml for %q: %w", appName, err)
	}

	packageJSON, err := buildPackageJSON(appConfig.PackageName, appConfig.Version, appConfig.Dependencies)
	if err != nil {
		return fmt.Errorf("failed to build package.json: %w", err)
	}

	packageJSONPath := filepath.Join(appEnvPath, "package.json")
	if err := os.WriteFile(packageJSONPath, packageJSON, 0644); err != nil {
		return fmt.Errorf("failed to write package.json: %w", err)
	}

	if appConfig.LockFile != "" {
		lockContent, decErr := DecompressLockFile(appConfig.LockFile)
		if decErr != nil {
			return fmt.Errorf("failed to decompress lock file for %q: %w", appName, decErr)
		}
		lockFilePath := filepath.Join(appEnvPath, "pnpm-lock.yaml")
		if err := os.WriteFile(lockFilePath, []byte(lockContent), 0644); err != nil {
			return fmt.Errorf("failed to write pnpm-lock.yaml for %q: %w", appName, err)
		}
	}

	envVars := getFNMEnvVars(appEnvPath)

	for _, dir := range []string{envVars["npm_config_store_dir"], envVars["npm_config_global_dir"]} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", dir, err)
		}
	}

	nodeBinPath := env.GetNodeBinaryPath(storeRoot, nodeVersion)
	pnpmCjsPath := env.GetPNPMPath(storeRoot, pnpmVersion, pnpmHash)

	args := buildPNPMInstallArgs(pnpmCjsPath, appConfig.LockFile != "")

	nodeBinDir := filepath.Dir(nodeBinPath)
	envVars["PATH"] = nodeBinDir + string(os.PathListSeparator) + os.Getenv("PATH")
	cmdEnv := buildEnvWithOverrides(os.Environ(), envVars)

	cmd := exec.Command(nodeBinPath, args...)
	cmd.Dir = appEnvPath
	cmd.Env = cmdEnv
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	log.Debug("installing FNM app",
		zap.String("app", appName),
		zap.String("package", appConfig.PackageName),
		zap.String("node", nodeVersion),
		zap.String("pnpm", pnpmVersion),
	)

	fmt.Fprintf(os.Stderr, "Installing %s...\n", appName)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install FNM app %q: %w", appName, err)
	}

	fmt.Fprintf(os.Stderr, "Installed %s\n", appName)

	cleanupOnError = false
	return nil
}

func (rm *RuntimeManager) GetFNMCommandInfo(appName string, appConfig *binmanager.AppConfigFNM, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (*binmanager.CommandInfo, error) {
	appEnvPath, _, rc, err := rm.resolveFNMAppEnvPath(appName, appConfig, files, archives)
	if err != nil {
		return nil, err
	}

	if rc.FNM == nil {
		return nil, fmt.Errorf("runtime for %q has no FNM config (nodeVersion/pnpmVersion)", appName)
	}

	if err := validateRelativePath(appConfig.BinPath); err != nil {
		return nil, fmt.Errorf("app %q: unsafe binPath: %w", appName, err)
	}

	storeRoot := env.GetStorePath()
	nodeBinPath := env.GetNodeBinaryPath(storeRoot, rc.FNM.NodeVersion)
	nodeBinDir := filepath.Dir(nodeBinPath)
	appBinPath := filepath.Join(appEnvPath, appConfig.BinPath)

	envVars := getFNMEnvVars(appEnvPath)
	envVars["PATH"] = nodeBinDir + string(os.PathListSeparator) + os.Getenv("PATH")

	return &binmanager.CommandInfo{
		Type:    "fnm",
		Command: appBinPath,
		Args:    nil,
		Env:     envVars,
	}, nil
}

func buildPNPMInstallArgs(pnpmCjsPath string, hasLockFile bool) []string {
	args := []string{pnpmCjsPath, "install"}
	if hasLockFile {
		args = append(args, "--frozen-lockfile")
	}
	return args
}

// defaultPNPMWorkspaceConfig returns the recommended pnpm 11 workspace
// security defaults applied to every FNM app environment. Delegates to
// internal/pnpmdefaults — the single source shared with the JS engine that
// injects the same map as a global so config.js can publish it via
// sharedStorage["pnpm-workspace-defaults"].
func defaultPNPMWorkspaceConfig() map[string]any {
	return pnpmdefaults.Defaults()
}

// mergePNPMWorkspaceConfig shallow-merges parsed user YAML on top of the
// base defaults map. Top-level user keys win; unset keys keep their default.
// An empty userYAML returns a copy of base unchanged.
func mergePNPMWorkspaceConfig(base map[string]any, userYAML string) (map[string]any, error) {
	merged := make(map[string]any, len(base))
	for k, v := range base {
		merged[k] = v
	}

	if strings.TrimSpace(userYAML) == "" {
		return merged, nil
	}

	var user map[string]any
	if err := yaml.Unmarshal([]byte(userYAML), &user); err != nil {
		return nil, fmt.Errorf("failed to parse user pnpm-workspace.yaml: %w", err)
	}

	for k, v := range user {
		merged[k] = v
	}
	return merged, nil
}

// preparePNPMWorkspaceForApp computes the merged pnpm-workspace.yaml content
// (defaults + user override) and returns a copy of files with the
// pnpm-workspace.yaml entry removed (consumed by the merge). Callers MUST
// write the returned YAML to disk AFTER any archive extraction so that
// archives cannot overwrite the secure defaults. The input files map is not
// mutated; when it does not contain the workspace entry the same map is
// returned.
func preparePNPMWorkspaceForApp(files map[string]string) (mergedYAML string, filteredFiles map[string]string, err error) {
	mergedYAML, err = buildPNPMWorkspaceForApp(files)
	if err != nil {
		return "", nil, err
	}

	if _, has := files["pnpm-workspace.yaml"]; !has {
		return mergedYAML, files, nil
	}

	filtered := make(map[string]string, len(files)-1)
	for k, v := range files {
		if k == "pnpm-workspace.yaml" {
			continue
		}
		filtered[k] = v
	}
	return mergedYAML, filtered, nil
}

// writeFNMAppWorkspaceFile writes mergedYAML to {appEnvPath}/pnpm-workspace.yaml.
// Callers MUST invoke this AFTER any archive extraction so archives cannot
// overwrite the secure defaults.
func writeFNMAppWorkspaceFile(appEnvPath, mergedYAML string) error {
	if err := os.MkdirAll(appEnvPath, 0755); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	workspacePath := filepath.Join(appEnvPath, "pnpm-workspace.yaml")
	if err := os.WriteFile(workspacePath, []byte(mergedYAML), 0644); err != nil {
		return fmt.Errorf("failed to write pnpm-workspace.yaml: %w", err)
	}
	return nil
}

// buildPNPMWorkspaceForApp returns the YAML string to write as
// pnpm-workspace.yaml in the app environment. It starts from the
// recommended defaults and shallow-merges the user's
// files["pnpm-workspace.yaml"] entry on top. Returns defaults alone when
// the user provides no override.
func buildPNPMWorkspaceForApp(files map[string]string) (string, error) {
	userYAML := ""
	if files != nil {
		userYAML = files["pnpm-workspace.yaml"]
	}

	merged, err := mergePNPMWorkspaceConfig(defaultPNPMWorkspaceConfig(), userYAML)
	if err != nil {
		return "", err
	}

	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("failed to marshal pnpm-workspace.yaml: %w", err)
	}
	return string(out), nil
}

func buildPackageJSON(packageName string, version string, deps map[string]string) ([]byte, error) {
	allDeps := make(map[string]string, len(deps)+1)
	allDeps[packageName] = version
	for k, v := range deps {
		allDeps[k] = v
	}

	pkg := map[string]any{
		"name":         "datamitsu-app-" + strings.NewReplacer("@", "", "/", "-").Replace(packageName),
		"version":      "0.0.0",
		"private":      true,
		"dependencies": allDeps,
		"type":         "module",
	}

	return json.MarshalIndent(pkg, "", "  ")
}
