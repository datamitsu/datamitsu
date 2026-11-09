package runtimemanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

const (
	pythonReadyFilename   = ".datamitsu-python-ready.json"
	pythonCatalogFilename = ".datamitsu-python-downloads.json"
)

type pythonReadyRecord struct {
	Schema     int    `json:"schema"`
	Executable string `json:"executable"`
}

type uvPythonCatalog map[string]uvPythonCatalogEntry

type uvPythonCatalogEntry struct {
	Name       string              `json:"name"`
	Arch       uvPythonCatalogArch `json:"arch"`
	OS         string              `json:"os"`
	LibC       string              `json:"libc"`
	Major      int                 `json:"major"`
	Minor      int                 `json:"minor"`
	Patch      int                 `json:"patch"`
	Prerelease *string             `json:"prerelease"`
	URL        string              `json:"url"`
	Variant    *string             `json:"variant"`
	Build      string              `json:"build"`
	SHA256     string              `json:"sha256"`
}

type uvPythonCatalogArch struct {
	Family  string  `json:"family"`
	Variant *string `json:"variant"`
}

type selectedPythonDownload struct {
	Key   string
	Entry config.PythonDownload
}

// ResolvePython reads the ready record and never runs uv or repairs files.
func (rm *RuntimeManager) ResolvePython(runtimeName string) (string, error) {
	rc, ok := rm.mapOfRuntimes[runtimeName]
	if !ok || rc.Kind != config.RuntimeKindUV || rc.UV == nil {
		return "", fmt.Errorf("runtime %q is not a configured uv runtime", runtimeName)
	}
	identity, err := rm.runtimeDataIdentity(runtimeName, rc)
	if err != nil {
		return "", err
	}
	return resolvePythonReady(env.GetStorePath(), env.GetRuntimeDataPath(runtimeName, identity))
}

// EnsurePython installs the configured hash-pinned CPython when its ready record
// is absent or unhealthy.
func (rm *RuntimeManager) EnsurePython(ctx context.Context, runtimeName string) (string, error) {
	rc, ok := rm.mapOfRuntimes[runtimeName]
	if !ok || rc.Kind != config.RuntimeKindUV || rc.UV == nil {
		return "", fmt.Errorf("runtime %q is not a configured uv runtime", runtimeName)
	}
	selected, err := selectPythonDownload(rm, rc.UV)
	if err != nil {
		return "", fmt.Errorf("runtime %s: %w", runtimeName, err)
	}
	if err := validatePythonDownload(selected.Entry); err != nil {
		return "", fmt.Errorf("runtime %s: invalid cpython catalog entry %q: %w", runtimeName, selected.Key, err)
	}
	identity, err := rm.runtimeDataIdentity(runtimeName, rc)
	if err != nil {
		return "", err
	}
	if err := env.RejectInstallChild("Python provisioning"); err != nil {
		return "", err
	}
	storeRoot := env.GetStorePath()
	leaseCtx, releaseStore, err := storelock.WithStore(ctx, storeRoot)
	if err != nil {
		return "", fmt.Errorf("acquire store lease for Python runtime %q: %w", runtimeName, err)
	}
	defer releaseStore()
	identityRoot := env.GetRuntimeDataPath(runtimeName, identity)
	if executable, err := resolvePythonReady(storeRoot, identityRoot); err == nil {
		return executable, nil
	}
	if err := httpx.GuardOffline("Python installation for runtime " + runtimeName); err != nil {
		return "", err
	}
	uvPath, err := rm.getRuntimePath(leaseCtx, runtimeName)
	if err != nil {
		return "", fmt.Errorf("acquire uv for Python runtime %q: %w", runtimeName, err)
	}
	pythonRoot := filepath.Join(identityRoot, "python")
	leaseCtx, releaseTarget, err := lockAppTarget(leaseCtx, pythonRoot)
	if err != nil {
		return "", err
	}
	defer releaseTarget()
	if executable, err := resolvePythonReady(storeRoot, identityRoot); err == nil {
		return executable, nil
	}
	for _, path := range []string{
		filepath.Join(pythonRoot, pythonReadyFilename),
		filepath.Join(identityRoot, pythonCatalogFilename),
	} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("remove stale Python metadata %q: %w", path, err)
		}
	}
	if err := rm.removeAll(pythonRoot); err != nil {
		return "", fmt.Errorf("remove invalid Python payload for runtime %q: %w", runtimeName, err)
	}
	if err := os.MkdirAll(identityRoot, 0o700); err != nil {
		return "", fmt.Errorf("create runtime data directory: %w", err)
	}
	catalogPath := filepath.Join(identityRoot, pythonCatalogFilename)
	catalog, err := marshalPythonCatalog(selected)
	if err != nil {
		return "", fmt.Errorf("marshal pinned Python catalog: %w", err)
	}
	if err := writePrivateAtomic(catalogPath, catalog); err != nil {
		return "", fmt.Errorf("write pinned Python catalog: %w", err)
	}
	defer func() { _ = os.Remove(catalogPath) }()
	version := pythonDownloadVersion(selected.Entry)
	ownedEnv := getUVEnvVars(identityRoot)
	installArgs := []string{"python", "install", "--no-config", "--managed-python", "--no-bin", "--install-dir", pythonRoot, "--python-downloads-json-url", catalogPath, version}
	install := exec.CommandContext(context.WithoutCancel(leaseCtx), uvPath, installArgs...) //nolint:gosec // uvPath is the configured verified runtime
	install.Dir = identityRoot
	install.Env = pythonProvisionEnv(ownedEnv)
	var installOutput bytes.Buffer
	install.Stdout = &installOutput
	install.Stderr = &installOutput
	if runErr := runInstallCmd(leaseCtx, install); runErr != nil {
		return "", uvPythonCommandError("python install", installOutput.Bytes(), runErr)
	}
	findArgs := []string{"python", "find", "--no-config", "--offline", "--managed-python", "--python-downloads-json-url", catalogPath, version}
	find := exec.CommandContext(context.WithoutCancel(leaseCtx), uvPath, findArgs...) //nolint:gosec // uvPath is the configured verified runtime
	find.Dir = identityRoot
	find.Env = pythonProvisionEnv(ownedEnv)
	var findOutput, findError bytes.Buffer
	find.Stdout = &findOutput
	find.Stderr = &findError
	if err := runInstallCmd(leaseCtx, find); err != nil {
		return "", uvPythonCommandError("python find", append(findOutput.Bytes(), findError.Bytes()...), err)
	}
	output := findOutput.Bytes()
	executable := strings.TrimSpace(string(output))
	if executable == "" {
		return "", errors.New("uv python find returned an empty executable path")
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("resolve uv Python executable: %w", err)
	}
	resolved, err := resolveExecutableWithin(pythonRoot, absolute)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(storeRoot, resolved)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("python executable is not inside the store: %q", resolved)
	}
	marker, err := json.Marshal(pythonReadyRecord{Schema: 1, Executable: filepath.ToSlash(relative)})
	if err != nil {
		return "", fmt.Errorf("marshal Python readiness record: %w", err)
	}
	if err := writePrivateAtomic(filepath.Join(pythonRoot, pythonReadyFilename), marker); err != nil {
		return "", fmt.Errorf("publish Python readiness record: %w", err)
	}
	return resolved, nil
}

func (rm *RuntimeManager) runtimeDataIdentity(runtimeName string, rc config.RuntimeConfig) (string, error) {
	osType, archType, libc, err := rm.hostRuntimePlatform()
	if err != nil {
		return "", err
	}
	identity, err := rm.runtimeIdentityHash(rc, osType, archType, libc)
	if err != nil {
		return "", fmt.Errorf("identify runtime %q data: %w", runtimeName, err)
	}
	return identity, nil
}

func resolvePythonReady(storeRoot, identityRoot string) (string, error) {
	markerPath := filepath.Join(identityRoot, "python", pythonReadyFilename)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return "", fmt.Errorf("read Python readiness record %q: %w", markerPath, err)
	}
	var record pythonReadyRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Schema != 1 {
		return "", fmt.Errorf("invalid Python readiness record %q", markerPath)
	}
	if filepath.IsAbs(record.Executable) {
		return "", fmt.Errorf("python readiness path must be store-relative: %q", record.Executable)
	}
	relative := filepath.Clean(filepath.FromSlash(record.Executable))
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("python readiness path escapes store: %q", record.Executable)
	}
	pythonRoot := filepath.Join(identityRoot, "python")
	lexicalPath := filepath.Join(storeRoot, relative)
	if !withinPath(pythonRoot, lexicalPath) {
		return "", fmt.Errorf("python readiness path is outside its runtime payload: %q", record.Executable)
	}
	resolvedRoot, err := filepath.EvalSymlinks(pythonRoot)
	if err != nil {
		return "", fmt.Errorf("resolve Python install directory: %w", err)
	}
	resolvedPath, err := storelock.ValidateTarget(storeRoot, relative)
	if err != nil {
		return "", fmt.Errorf("validate Python executable path: %w", err)
	}
	resolvedPath, err = filepath.EvalSymlinks(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("resolve Python executable path: %w", err)
	}
	if !withinPath(resolvedRoot, resolvedPath) {
		return "", fmt.Errorf("python executable resolves outside its runtime payload: %q", record.Executable)
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("stat Python executable: %w", err)
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0o111 == 0) {
		return "", fmt.Errorf("python executable is not executable: %q", resolvedPath)
	}
	return resolvedPath, nil
}

func resolveExecutableWithin(root, executable string) (string, error) {
	if !withinPath(root, executable) {
		return "", fmt.Errorf("uv selected Python outside managed install directory: %q", executable)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve Python install directory: %w", err)
	}
	resolvedExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve Python executable: %w", err)
	}
	if !withinPath(resolvedRoot, resolvedExecutable) {
		return "", fmt.Errorf("uv selected Python outside managed install directory: %q", executable)
	}
	info, err := os.Stat(resolvedExecutable)
	if err != nil {
		return "", fmt.Errorf("stat Python executable: %w", err)
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0o111 == 0) {
		return "", fmt.Errorf("uv selected a non-executable Python path: %q", executable)
	}
	return resolvedExecutable, nil
}

func withinPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func selectPythonDownload(rm *RuntimeManager, uv *config.RuntimeConfigUV) (selectedPythonDownload, error) {
	if uv == nil || len(uv.PythonDownloads) == 0 {
		return selectedPythonDownload{}, errors.New("uv.pythonDownloads with SHA-256 pins is required; regenerate with datamitsu devtools pull-runtimes --runtime uv")
	}
	version := uv.PythonVersion
	if !isExactPythonVersion(version) {
		return selectedPythonDownload{}, fmt.Errorf("uv.pythonVersion %q must be an exact major.minor.patch", version)
	}
	hostOS, hostArch, hostLibc, err := rm.hostRuntimePlatform()
	if err != nil {
		return selectedPythonDownload{}, err
	}
	osType, archType, libc, err := pythonCatalogTarget(string(hostOS), string(hostArch), hostLibc)
	if err != nil {
		return selectedPythonDownload{}, err
	}
	keys := make([]string, 0, len(uv.PythonDownloads))
	for key := range uv.PythonDownloads {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var matches []selectedPythonDownload
	for _, key := range keys {
		entry := uv.PythonDownloads[key]
		if entry.Name != "cpython" || entry.Prerelease != "" || entry.Variant != "" || entry.Arch.Variant != "" {
			continue
		}
		if entry.OS == osType && entry.Arch.Family == archType && entry.LibC == libc && pythonDownloadVersion(entry) == version {
			matches = append(matches, selectedPythonDownload{Key: key, Entry: entry})
		}
	}
	if len(matches) != 1 {
		return selectedPythonDownload{}, fmt.Errorf("expected exactly one stable default-GIL CPython download for %s on %s/%s/%s, found %d", version, osType, archType, libc, len(matches))
	}
	return matches[0], nil
}

func pythonCatalogTarget(osType, archType, libc string) (string, string, string, error) {
	switch archType {
	case "amd64":
		archType = "x86_64"
	case "arm64":
		archType = "aarch64"
	default:
		return "", "", "", fmt.Errorf("unsupported CPython architecture %q", archType)
	}
	switch osType {
	case "linux":
		switch libc {
		case "glibc":
			libc = "gnu"
		case "musl":
			libc = "musl"
		default:
			return "", "", "", errors.New("cannot select a pinned CPython build because Linux libc is unknown")
		}
	case "darwin", "windows":
		libc = "none"
	default:
		return "", "", "", fmt.Errorf("unsupported CPython operating system %q", osType)
	}
	return osType, archType, libc, nil
}

func isExactPythonVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func pythonDownloadVersion(d config.PythonDownload) string {
	return fmt.Sprintf("%d.%d.%d", d.Major, d.Minor, d.Patch)
}

func validatePythonDownload(d config.PythonDownload) error {
	if d.Name != "cpython" || d.Prerelease != "" || d.Variant != "" || d.Arch.Variant != "" {
		return errors.New("only stable default-GIL CPython downloads are supported")
	}
	if !isExactPythonVersion(pythonDownloadVersion(d)) || d.Major < 1 || d.Minor < 0 || d.Patch < 0 {
		return errors.New("invalid CPython version components")
	}
	if _, err := digest.ParseSHA256(d.Hash); err != nil {
		return fmt.Errorf("hash must be a canonical SHA-256 digest: %w", err)
	}
	parsedURL, err := url.ParseRequestURI(d.URL)
	if err != nil || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") || parsedURL.Host == "" {
		return errors.New("url must be an absolute http or https URL")
	}
	return nil
}

func marshalPythonCatalog(selected selectedPythonDownload) ([]byte, error) {
	entry := selected.Entry
	record := uvPythonCatalogEntry{
		Name:   entry.Name,
		Arch:   uvPythonCatalogArch{Family: entry.Arch.Family},
		OS:     entry.OS,
		LibC:   entry.LibC,
		Major:  entry.Major,
		Minor:  entry.Minor,
		Patch:  entry.Patch,
		URL:    entry.URL,
		Build:  entry.Build,
		SHA256: strings.TrimPrefix(entry.Hash, "sha256:"),
	}
	if entry.Arch.Variant != "" {
		record.Arch.Variant = &entry.Arch.Variant
	}
	if entry.Prerelease != "" {
		record.Prerelease = &entry.Prerelease
	}
	if entry.Variant != "" {
		record.Variant = &entry.Variant
	}
	data, err := json.Marshal(uvPythonCatalog{selected.Key: record})
	if err != nil {
		return nil, fmt.Errorf("encode uv Python catalog: %w", err)
	}
	return data, nil
}

func pythonProvisionEnv(overrides map[string]string) []string {
	return buildEnvWithOverrides(withoutEnv(os.Environ(), func(key string) bool {
		upper := strings.ToUpper(key)
		return strings.HasPrefix(upper, "UV_PYTHON") || upper == "UV_CONFIG_FILE" || upper == "VIRTUAL_ENV" || upper == "CONDA_PREFIX"
	}), overrides)
}

func uvPythonCommandError(operation string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if strings.Contains(message, "unexpected argument") || strings.Contains(message, "unrecognized option") || strings.Contains(message, "unexpected value") {
		return fmt.Errorf("uv does not support required safe flags for %s; upgrade uv: %w: %s", operation, err, message)
	}
	return fmt.Errorf("uv %s failed: %w: %s", operation, err, message)
}

func writePrivateAtomic(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".datamitsu-python-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", path, err)
	}
	temporaryPath := file.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := file.Chmod(0o600); err != nil {
		return closeTemporaryFile(file, path, "set permissions", err)
	}
	if _, err := file.Write(content); err != nil {
		return closeTemporaryFile(file, path, "write", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary file for %q: %w", path, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish file %q: %w", path, err)
	}
	return nil
}

func closeTemporaryFile(file *os.File, path, operation string, operationErr error) error {
	if err := file.Close(); err != nil {
		return fmt.Errorf("%s temporary file for %q: %w; close also failed: %w", operation, path, operationErr, err)
	}
	return fmt.Errorf("%s temporary file for %q: %w", operation, path, operationErr)
}
