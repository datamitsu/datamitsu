package runtimemanager

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	entry, _ := rm.pnpmInstall.LoadOrStore(key, &installOnce{})
	once := entry.(*installOnce)
	once.once.Do(func() {
		once.err = rm.downloadPNPMFromRegistry(version, destDir, pnpmHash)
	})
	if once.err != nil {
		rm.pnpmInstall.CompareAndDelete(key, entry)
		return once.err
	}
	return nil
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
	entry, _ := rm.nodeInstall.LoadOrStore(nodeVersion, &installOnce{})
	once := entry.(*installOnce)
	once.once.Do(func() {
		once.err = rm.installNodeVersionOnce(fnmPath, nodeVersion, cacheRoot)
	})
	if once.err != nil {
		rm.nodeInstall.CompareAndDelete(nodeVersion, entry)
		return once.err
	}
	return nil
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
	cmd.Env = buildEnvWithOverrides(os.Environ(), map[string]string{
		"FNM_DIR": fnmDir,
	})
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

	appEnvPath, err = rm.GetAppPath(appName, config.RuntimeKindFNM, appConfig.Version, appConfig.Dependencies, lockFileHash(appConfig.LockFile), files, archives, runtimeName, FNMAppPathExtra{
		PackageName: appConfig.PackageName,
		BinPath:     appConfig.BinPath,
	})
	if err != nil {
		return "", "", config.RuntimeConfig{}, err
	}

	return appEnvPath, runtimeName, rc, nil
}

// InstallFNMApp installs an FNM-managed app if not already cached.
// If files is non-empty, writes them to the app directory before running pnpm.
// Safe for concurrent use from multiple goroutines.
func (rm *RuntimeManager) InstallFNMApp(appName string, appConfig *binmanager.AppConfigFNM, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	key := "fnm/" + appName
	entry, _ := rm.appInstall.LoadOrStore(key, &installOnce{})
	once := entry.(*installOnce)
	once.once.Do(func() {
		once.err = rm.installFNMAppOnce(appName, appConfig, files, archives)
	})
	if once.err != nil {
		rm.appInstall.CompareAndDelete(key, entry)
		return once.err
	}
	return nil
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
	if _, err := os.Stat(appBinPath); err == nil {
		log.Debug("FNM app already installed",
			zap.String("app", appName),
			zap.String("path", appBinPath),
		)
		return nil
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

	if len(files) > 0 || len(archives) > 0 {
		if err := binmanager.WriteAppFiles(appEnvPath, files, archives); err != nil {
			return fmt.Errorf("failed to write app files/archives for %q: %w", appName, err)
		}
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
	args := []string{pnpmCjsPath, "install", "--silent"}
	if hasLockFile {
		args = append(args, "--frozen-lockfile")
	}
	return args
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
