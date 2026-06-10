package runtimemanager

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/pnpmdefaults"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/goccy/go-yaml"
)

// pnpmReporter parses pnpm's --reporter=ndjson stream into live progress for a
// ui.Spinner and collects human-readable errors. pnpm emits errors as ndjson
// objects on stdout (level "error" with err.message/hint/code), not on stderr,
// so the error text must be extracted here rather than read from stderr.
type pnpmReporter struct {
	sp *ui.Spinner

	resolved   int
	downloaded int
	added      int
	errs       []string
}

func newPNPMReporter(sp *ui.Spinner) *pnpmReporter {
	return &pnpmReporter{sp: sp}
}

// line consumes one ndjson event. Unknown or malformed lines are ignored so a
// reporter-format change can never break an install — at worst progress is less
// detailed.
func (p *pnpmReporter) line(b []byte) {
	var ev struct {
		Name   string `json:"name"`
		Level  string `json:"level"`
		Status string `json:"status"`
		Hint   string `json:"hint"`
		Code   string `json:"code"`
		Added  *int   `json:"added"`
		Err    struct {
			Message string `json:"message"`
		} `json:"err"`
	}
	if json.Unmarshal(b, &ev) != nil {
		return
	}

	if ev.Level == "error" {
		msg := ev.Err.Message
		if msg == "" {
			msg = ev.Code
		}
		if ev.Hint != "" {
			msg = strings.TrimSpace(msg + "\n" + ev.Hint)
		}
		if msg != "" {
			p.errs = append(p.errs, msg)
		}
		return
	}

	switch ev.Name {
	case "pnpm:progress":
		switch ev.Status {
		case "resolved":
			p.resolved++
		case "fetched":
			p.downloaded++
		}
	case "pnpm:stats":
		if ev.Added != nil && *ev.Added > p.added {
			p.added = *ev.Added
		}
	default:
		return
	}

	p.sp.SetDetail(fmt.Sprintf("resolved %4d, downloaded %4d, added %4d",
		p.resolved, p.downloaded, p.added))
}

// errorOutput returns the best human-readable failure text: pnpm's ndjson error
// events when present, otherwise the captured stderr.
func (p *pnpmReporter) errorOutput(stderr string) string {
	if len(p.errs) > 0 {
		return strings.Join(p.errs, "\n")
	}
	return stderr
}

// pnpm.go holds the pnpm download + npm-app-install helpers shared by the node
// runtime (node.go). pnpm is downloaded directly from the npm registry with a
// pinned SHA-256 + the registry's SHA-512 integrity, and npm tools are installed
// with `node <pnpm.cjs> install`.

var pnpmHTTPClient = httpx.NewHardenedClient(5 * time.Minute)

const maxPNPMDownloadSize = 100 * 1024 * 1024 // 100 MiB

type npmVersionMeta struct {
	Dist struct {
		Tarball   string `json:"tarball"`
		Shasum    string `json:"shasum"`
		Integrity string `json:"integrity"`
	} `json:"dist"`
}

func (rm *RuntimeManager) installPNPM(ctx context.Context, version string, destDir string, pnpmHash string) error {
	key := version + "\x00" + pnpmHash
	_, err, _ := rm.pnpmInstall.Do(key, func() (any, error) {
		return nil, rm.downloadPNPMFromRegistry(ctx, version, destDir, pnpmHash)
	})
	if err != nil {
		return fmt.Errorf("failed to install pnpm %q: %w", version, err)
	}
	return nil
}

func (rm *RuntimeManager) downloadPNPMFromRegistry(ctx context.Context, version string, destDir string, pnpmHash string) error {
	return rm.downloadPNPMFromRegistryURL(ctx, "https://registry.npmjs.org", version, destDir, pnpmHash)
}

// downloadPNPMFromRegistryURL downloads, verifies, and extracts pnpm from the
// given npm registry base URL. The base URL is a parameter purely so tests can
// point it at a mock registry and exercise the real download/verify/extract
// path (pinned SHA-256 + registry SHA-512); production always passes the public
// npm registry via downloadPNPMFromRegistry.
func (rm *RuntimeManager) downloadPNPMFromRegistryURL(ctx context.Context, registryBaseURL, version, destDir, pnpmHash string) error {
	if pnpmHash == "" {
		return fmt.Errorf("PNPM hash is required but not provided for pnpm@%s", version)
	}

	pnpmCjsPath := filepath.Join(destDir, "package", "bin", "pnpm.cjs")
	if _, err := os.Stat(pnpmCjsPath); err == nil {
		return nil
	}

	if err := httpx.GuardOffline("pnpm runtime download"); err != nil {
		return err
	}

	url := fmt.Sprintf("%s/pnpm/%s", registryBaseURL, version)
	metaReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to build PNPM metadata request: %w", err)
	}
	resp, err := pnpmHTTPClient.Do(metaReq)
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
	// The pinned SHA-256 is the integrity anchor, but the tarball must still be
	// fetched over TLS so the registry response cannot downgrade us to a
	// plaintext URL (mirrors fetchPNPMTarballHash on the pull side).
	if !strings.HasPrefix(meta.Dist.Tarball, "https://") {
		return fmt.Errorf("pnpm@%s: tarball URL is not https: %s", version, meta.Dist.Tarball)
	}
	if !hasSHA512Prefix(meta.Dist.Integrity) {
		return fmt.Errorf("pnpm@%s: SHA-512 integrity required but not found in registry metadata", version)
	}

	tarReq, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.Dist.Tarball, nil)
	if err != nil {
		return fmt.Errorf("failed to build PNPM tarball request: %w", err)
	}
	tarResp, err := pnpmHTTPClient.Do(tarReq)
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

	// Render the pnpm tarball download through the shared display, like the node
	// runtime and managed binaries (a bar in a terminal, throttled lines in CI).
	tracked := ui.Current().Download("pnpm "+version, tarResp.ContentLength, limitedBody)
	defer func() { _ = tracked.Close() }()

	written, err := io.Copy(writer, tracked)
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

	// Extract through binmanager's single hardened tar path (the same walker
	// used for managed-binary installs): traversal entries, absolute symlinks,
	// and escaping symlinks are skipped, and size limits are enforced. It writes
	// directly into destDir, so {destDir}/package/bin/pnpm.cjs lands as expected.
	if err := binmanager.ExtractArchiveToDir(tmpPath, binmanager.BinContentTypeTarGz, destDir); err != nil {
		_ = os.RemoveAll(destDir)
		return fmt.Errorf("failed to extract PNPM tarball: %w", err)
	}

	return nil
}

// verifyPNPMPinnedHash verifies the downloaded PNPM tarball against the
// pinned SHA-256 hash from configuration. This is the primary security check
// per the project's security policy: all downloads must be verified against
// a pinned hash, not against untrusted registry-provided metadata.
func verifyPNPMPinnedHash(expectedHash string, actualSHA256 []byte) error {
	if expectedHash == "" {
		return errors.New("pnpm tarball SHA-256 hash is required but not configured")
	}
	actualHex := hex.EncodeToString(actualSHA256)
	if actualHex != expectedHash {
		return fmt.Errorf("pnpm tarball SHA-256 hash mismatch: expected %q, got %q", expectedHash, actualHex)
	}
	return nil
}

// hasSHA512Prefix reports whether an npm SRI integrity string carries the
// required "sha512-" prefix. An empty string returns false, so callers reject
// both missing and non-sha512 integrity with a single !hasSHA512Prefix(s) check.
func hasSHA512Prefix(integrity string) bool {
	return strings.HasPrefix(integrity, "sha512-")
}

// verifyPNPMIntegrity checks the downloaded tarball against the npm registry
// SHA-512 integrity metadata (SRI format). SHA-1 fallback is not supported.
func verifyPNPMIntegrity(meta npmVersionMeta, sha512Sum []byte) error {
	if !hasSHA512Prefix(meta.Dist.Integrity) {
		return errors.New("SHA-512 integrity required but not found in registry metadata")
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

// filesWithMergedWorkspaceYAML returns a copy of files with the
// pnpm-workspace.yaml entry replaced by freshly merged content (defaults + user
// override), recomputing the merge each call. The original files map is not
// mutated. Production threads a once-per-exec merge through filesWithWorkspaceYAML
// instead; this single-shot form is retained as the reference the cache-key
// regression test pins resolveNodeAppEnvPath against.
func filesWithMergedWorkspaceYAML(files map[string]string) (map[string]string, error) {
	merged, err := buildPNPMWorkspaceForApp(files)
	if err != nil {
		return nil, err
	}
	return filesWithWorkspaceYAML(files, merged), nil
}

// filesWithWorkspaceYAML returns a copy of files with the pnpm-workspace.yaml
// entry set to the already-merged content. Unlike filesWithMergedWorkspaceYAML it
// does not recompute the merge, so a caller that merged once per exec can reuse
// the result for the cache key. The original files map is not mutated.
func filesWithWorkspaceYAML(files map[string]string, mergedYAML string) map[string]string {
	out := make(map[string]string, len(files)+1)
	maps.Copy(out, files)
	out["pnpm-workspace.yaml"] = mergedYAML
	return out
}

func buildPNPMInstallArgs(pnpmCjsPath string, hasLockFile bool) []string {
	// --reporter=ndjson emits a machine-readable event stream on stdout that the
	// installer parses for live progress (and errors) instead of letting pnpm's
	// human reporter write raw output over the shared progress display.
	args := []string{pnpmCjsPath, "install", "--reporter=ndjson"}
	if hasLockFile {
		args = append(args, "--frozen-lockfile")
	}
	return args
}

// mergePNPMWorkspaceConfig shallow-merges parsed user YAML on top of the
// base defaults map. Top-level user keys win; unset keys keep their default.
// An empty userYAML returns a copy of base unchanged.
func mergePNPMWorkspaceConfig(base map[string]any, userYAML string) (map[string]any, error) {
	merged := make(map[string]any, len(base))
	maps.Copy(merged, base)

	if strings.TrimSpace(userYAML) == "" {
		return merged, nil
	}

	var user map[string]any
	if err := yaml.Unmarshal([]byte(userYAML), &user); err != nil {
		return nil, fmt.Errorf("failed to parse user pnpm-workspace.yaml: %w", err)
	}

	maps.Copy(merged, user)
	return merged, nil
}

// filesWithoutWorkspaceYAML returns a copy of files with the pnpm-workspace.yaml
// entry removed: that entry is consumed by the merge and written separately via
// writeAppWorkspaceFile (which the caller MUST invoke AFTER any archive
// extraction so archives cannot overwrite the secure defaults). The input map is
// not mutated; when it does not contain the workspace entry the same map is
// returned.
func filesWithoutWorkspaceYAML(files map[string]string) map[string]string {
	if _, has := files["pnpm-workspace.yaml"]; !has {
		return files
	}

	filtered := make(map[string]string, len(files)-1)
	for k, v := range files {
		if k == "pnpm-workspace.yaml" {
			continue
		}
		filtered[k] = v
	}
	return filtered
}

// writeAppWorkspaceFile writes mergedYAML to {appEnvPath}/pnpm-workspace.yaml.
// Callers MUST invoke this AFTER any archive extraction so archives cannot
// overwrite the secure defaults.
func writeAppWorkspaceFile(appEnvPath, mergedYAML string) error {
	if err := os.MkdirAll(appEnvPath, 0o755); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	workspacePath := filepath.Join(appEnvPath, "pnpm-workspace.yaml")
	if err := os.WriteFile(workspacePath, []byte(mergedYAML), 0o644); err != nil {
		return fmt.Errorf("failed to write pnpm-workspace.yaml: %w", err)
	}
	return nil
}

// buildPNPMWorkspace is the workspace merge+marshal entry point, routed
// through a var so the (relatively expensive) merge runs once per exec: it is
// invoked a single time by GetCommandInfo/ComputeAppPath and the result is
// threaded into the install and command-info passes rather than recomputed in
// each. Tests swap it to count invocations.
var buildPNPMWorkspace = buildPNPMWorkspaceForApp

// buildPNPMWorkspaceForApp returns the YAML string to write as
// pnpm-workspace.yaml in the app environment. It starts from the recommended
// defaults (internal/pnpmdefaults — the single source shared with the JS engine
// that injects the same map as a global so config.js can publish it via
// sharedStorage["pnpm-workspace-defaults"]) and shallow-merges the user's
// files["pnpm-workspace.yaml"] entry on top. Returns defaults alone when the
// user provides no override.
func buildPNPMWorkspaceForApp(files map[string]string) (string, error) {
	userYAML := ""
	if files != nil {
		userYAML = files["pnpm-workspace.yaml"]
	}

	merged, err := mergePNPMWorkspaceConfig(pnpmdefaults.Defaults(), userYAML)
	if err != nil {
		return "", err
	}

	// Pin the content-addressable store inside the datamitsu store so it lives
	// under GetStorePath() (and `datamitsu store clear` actually removes it).
	// pnpm 11 ignores npm_config_store_dir / .npmrc store-dir; the workspace
	// storeDir key is the mechanism it honors. Forced after the user merge —
	// datamitsu owns the store location, a user config must not relocate it.
	merged["storeDir"] = env.GetPNPMStorePath()

	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("failed to marshal pnpm-workspace.yaml: %w", err)
	}
	return string(out), nil
}

// buildPNPMWorkspaceHashForm is the cache-key form of the merged workspace:
// identical to buildPNPMWorkspaceForApp EXCEPT the storeDir pin, which is an
// absolute path under the store root. Folding it into the app hash would make
// node app store paths root-dependent — breaking the relocatability contract
// the OCI bundle demand matching relies on (a layer hashed under /dm/store
// could never match a host store). storeDir only ever changes together with
// the store root itself, under which no cached content exists anyway, so
// excluding it loses no invalidation.
func buildPNPMWorkspaceHashForm(files map[string]string) (string, error) {
	userYAML := ""
	if files != nil {
		userYAML = files["pnpm-workspace.yaml"]
	}

	merged, err := mergePNPMWorkspaceConfig(pnpmdefaults.Defaults(), userYAML)
	if err != nil {
		return "", err
	}

	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("failed to marshal pnpm-workspace.yaml hash form: %w", err)
	}
	return string(out), nil
}

func buildPackageJSON(packageName string, version string, deps map[string]string) ([]byte, error) {
	allDeps := make(map[string]string, len(deps)+1)
	allDeps[packageName] = version
	maps.Copy(allDeps, deps)

	pkg := map[string]any{
		"name":         "datamitsu-app-" + strings.NewReplacer("@", "", "/", "-").Replace(packageName),
		"version":      "0.0.0",
		"private":      true,
		"dependencies": allDeps,
		"type":         "module",
	}

	out, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal package.json: %w", err)
	}
	return out, nil
}
