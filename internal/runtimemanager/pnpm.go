package runtimemanager

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"github.com/datamitsu/datamitsu/internal/pnpmdefaults"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/goccy/go-yaml"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pnpm.go holds the pnpm download + npm-app-install helpers shared by the node
// runtime (node.go). These were originally part of the fnm runtime; node still
// installs npm tools the same way (pnpm is downloaded directly from the npm
// registry with a pinned SHA-256 + the registry's SHA-512 integrity, and tools
// are installed with `node <pnpm.cjs> install`). Only node's acquisition changed
// from the fnm manager binary to a direct, hash-pinned archive — see node.go.

var pnpmHTTPClient = &http.Client{
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
	resp, err := pnpmHTTPClient.Get(url)
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

	tarResp, err := pnpmHTTPClient.Get(meta.Dist.Tarball)
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

func buildPNPMInstallArgs(pnpmCjsPath string, hasLockFile bool) []string {
	args := []string{pnpmCjsPath, "install"}
	if hasLockFile {
		args = append(args, "--frozen-lockfile")
	}
	return args
}

// defaultPNPMWorkspaceConfig returns the recommended pnpm 11 workspace
// security defaults applied to every node app environment. Delegates to
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

// writeAppWorkspaceFile writes mergedYAML to {appEnvPath}/pnpm-workspace.yaml.
// Callers MUST invoke this AFTER any archive extraction so archives cannot
// overwrite the secure defaults.
func writeAppWorkspaceFile(appEnvPath, mergedYAML string) error {
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
