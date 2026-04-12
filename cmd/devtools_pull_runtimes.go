package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/detector"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/registry"
	"github.com/datamitsu/datamitsu/internal/syslist"

	"github.com/spf13/cobra"
)

var (
	pullRuntimesUpdateFlag  bool
	pullRuntimesDryRunFlag  bool
	pullRuntimesRuntimeFlag string
)

var validRuntimeNames = []string{"fnm", "uv", "jvm"}

func init() {
	devtoolsCmd.AddCommand(pullRuntimesCmd)
	pullRuntimesCmd.Flags().BoolVar(&pullRuntimesUpdateFlag, "update", false,
		"Fetch latest versions from upstream before updating")
	pullRuntimesCmd.Flags().BoolVar(&pullRuntimesDryRunFlag, "dry-run", false,
		"Show what would be updated without writing files")
	pullRuntimesCmd.Flags().StringVar(&pullRuntimesRuntimeFlag, "runtime", "",
		"Update only the specified runtime (fnm, uv, or jvm)")
}

var pullRuntimesCmd = &cobra.Command{
	Use:   "pull-runtimes <file>",
	Short: "Pull runtime configurations from upstream releases",
	Long: `Pull runtime configurations (FNM, UV, JVM) with latest versions from upstream.

Fetches latest releases from GitHub, computes SHA-256 hashes, and writes
the result to the specified file.

Requires --update flag to fetch releases (safety guard).
With --runtime: updates only the specified runtime (fnm, uv, or jvm)
With --dry-run: shows what would be updated without writing

Example:
  datamitsu devtools pull-runtimes --update config/src/runtimes.json
  datamitsu devtools pull-runtimes --update --runtime uv config/src/runtimes.json
  datamitsu devtools pull-runtimes --update --dry-run config/src/runtimes.json`,
	Args: cobra.ExactArgs(1),
	RunE: runPullRuntimes,
}

type runtimePullResult struct {
	name       string
	oldVersion string
	newVersion string
	updated    bool
	err        error
}

func runPullRuntimes(cmd *cobra.Command, args []string) error {
	if !pullRuntimesUpdateFlag {
		return fmt.Errorf("--update flag is required to fetch releases from upstream")
	}

	runtimeFilter := pullRuntimesRuntimeFlag
	if runtimeFilter != "" {
		if !isValidRuntime(runtimeFilter) {
			return fmt.Errorf("invalid runtime %q: must be one of %s", runtimeFilter, strings.Join(validRuntimeNames, ", "))
		}
	}

	outputPath := args[0]

	existing, err := readRuntimesJSON(outputPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to read existing %s: %w", outputPath, err)
		}
		existing = make(RuntimesJSON)
	}

	runtimes := make(RuntimesJSON)
	for k, v := range existing {
		runtimes[k] = v
	}

	runtimesToUpdate := validRuntimeNames
	if runtimeFilter != "" {
		runtimesToUpdate = []string{runtimeFilter}
	}

	var results []runtimePullResult

	for _, name := range runtimesToUpdate {
		fmt.Printf("\n=== Updating %s ===\n", name)

		var runtimeJSON *RuntimeJSON
		var updateErr error

		switch name {
		case "fnm":
			var data *FNMRuntimeData
			var binaries binmanager.MapOfBinaries
			data, binaries, updateErr = pullFNMRuntime()
			if updateErr == nil {
				runtimeJSON = buildFNMRuntimeJSON(data, binaries)
			}
		case "uv":
			var data *UVRuntimeData
			var binaries binmanager.MapOfBinaries
			data, binaries, updateErr = pullUVRuntime()
			if updateErr == nil {
				runtimeJSON = buildUVRuntimeJSON(data, binaries)
			}
		case "jvm":
			var data *JVMRuntimeData
			var binaries binmanager.MapOfBinaries
			data, binaries, updateErr = pullJVMRuntime()
			if updateErr == nil {
				runtimeJSON = buildJVMRuntimeJSON(data, binaries)
			}
		}

		result := runtimePullResult{name: name}
		if updateErr != nil {
			result.err = updateErr
			fmt.Fprintf(os.Stderr, "Error updating %s: %v\n", name, updateErr)
			if strings.Contains(updateErr.Error(), "rate limit") || strings.Contains(updateErr.Error(), "403") {
				fmt.Fprintf(os.Stderr, "Hint: set GITHUB_TOKEN env var to increase rate limits\n")
			}
			results = append(results, result)
			continue
		}

		result.newVersion = runtimeVersion(runtimeJSON)
		if old, ok := runtimes[name]; ok {
			result.oldVersion = runtimeVersion(old)
		}
		result.updated = result.oldVersion != result.newVersion

		runtimes[name] = runtimeJSON
		results = append(results, result)
	}

	printPullSummary(results)

	for _, r := range results {
		if r.err != nil {
			return fmt.Errorf("some runtimes failed to update")
		}
	}

	if !pullRuntimesDryRunFlag {
		if err := writeRuntimesJSON(outputPath, runtimes); err != nil {
			return fmt.Errorf("failed to write %s: %w", outputPath, err)
		}
		fmt.Printf("\nWritten to %s\n", outputPath)
	} else {
		fmt.Printf("\nDry run - no files written\n")
	}

	return nil
}

func isValidRuntime(name string) bool {
	for _, v := range validRuntimeNames {
		if v == name {
			return true
		}
	}
	return false
}

func runtimeVersion(r *RuntimeJSON) string {
	if r == nil {
		return ""
	}
	var parts []string
	if r.FNM != nil {
		parts = append(parts, fmt.Sprintf("node=%s,pnpm=%s", r.FNM.NodeVersion, r.FNM.PNPMVersion))
	}
	if r.UV != nil {
		parts = append(parts, fmt.Sprintf("python=%s", r.UV.PythonVersion))
	}
	if r.JVM != nil {
		parts = append(parts, fmt.Sprintf("java=%s", r.JVM.JavaVersion))
	}
	if r.Managed != nil {
		binCount := 0
		for _, archMap := range r.Managed.Binaries {
			for _, libcMap := range archMap {
				binCount += len(libcMap)
			}
		}
		parts = append(parts, fmt.Sprintf("binaries=%d", binCount))
	}
	return strings.Join(parts, ",")
}

func printPullSummary(results []runtimePullResult) {
	fmt.Printf("\n--- Summary ---\n")
	for _, r := range results {
		if r.err != nil {
			fmt.Printf("  %s: FAILED (%v)\n", r.name, r.err)
		} else if r.updated {
			if r.oldVersion != "" {
				fmt.Printf("  %s: updated (%s -> %s)\n", r.name, r.oldVersion, r.newVersion)
			} else {
				fmt.Printf("  %s: added (%s)\n", r.name, r.newVersion)
			}
		} else {
			fmt.Printf("  %s: unchanged\n", r.name)
		}
	}
}

func readRuntimesJSON(path string) (RuntimesJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var runtimes RuntimesJSON
	if err := json.Unmarshal(data, &runtimes); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return runtimes, nil
}

// RuntimeJSON represents a single runtime entry in the JSON output.
type RuntimeJSON struct {
	Kind    string              `json:"kind"`
	Mode    string              `json:"mode"`
	Managed *RuntimeManagedJSON `json:"managed,omitempty"`
	FNM     *FNMConfigJSON      `json:"fnm,omitempty"`
	UV      *UVConfigJSON       `json:"uv,omitempty"`
	JVM     *JVMConfigJSON      `json:"jvm,omitempty"`
}

// RuntimeManagedJSON holds the managed binary configuration for a runtime.
type RuntimeManagedJSON struct {
	Binaries binmanager.MapOfBinaries `json:"binaries"`
}

// FNMConfigJSON holds FNM-specific configuration in the JSON output.
type FNMConfigJSON struct {
	NodeVersion string `json:"nodeVersion"`
	PNPMVersion string `json:"pnpmVersion"`
	PNPMHash    string `json:"pnpmHash"`
}

// UVConfigJSON holds UV-specific configuration in the JSON output.
type UVConfigJSON struct {
	PythonVersion string `json:"pythonVersion"`
}

// JVMConfigJSON holds JVM-specific configuration in the JSON output.
type JVMConfigJSON struct {
	JavaVersion string `json:"javaVersion"`
}

// RuntimesJSON is the top-level structure for runtimes.json.
type RuntimesJSON map[string]*RuntimeJSON

// writeRuntimesJSON marshals the runtimes map to JSON with 2-space indentation
// and writes it atomically (temp file + rename) to the given path.
func writeRuntimesJSON(path string, runtimes RuntimesJSON) error {
	data, err := json.MarshalIndent(runtimes, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling runtimes JSON: %w", err)
	}
	data = append(data, '\n')

	tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// buildFNMRuntimeJSON constructs a RuntimeJSON from FNM updater results.
func buildFNMRuntimeJSON(data *FNMRuntimeData, binaries binmanager.MapOfBinaries) *RuntimeJSON {
	return &RuntimeJSON{
		Kind: "fnm",
		Mode: "managed",
		Managed: &RuntimeManagedJSON{
			Binaries: binaries,
		},
		FNM: &FNMConfigJSON{
			NodeVersion: data.NodeVersion,
			PNPMVersion: data.PNPMVersion,
			PNPMHash:    data.PNPMHash,
		},
	}
}

// buildUVRuntimeJSON constructs a RuntimeJSON from UV updater results.
func buildUVRuntimeJSON(data *UVRuntimeData, binaries binmanager.MapOfBinaries) *RuntimeJSON {
	return &RuntimeJSON{
		Kind: "uv",
		Mode: "managed",
		Managed: &RuntimeManagedJSON{
			Binaries: binaries,
		},
		UV: &UVConfigJSON{
			PythonVersion: data.PythonVersion,
		},
	}
}

// buildJVMRuntimeJSON constructs a RuntimeJSON from JVM updater results.
func buildJVMRuntimeJSON(data *JVMRuntimeData, binaries binmanager.MapOfBinaries) *RuntimeJSON {
	return &RuntimeJSON{
		Kind: "jvm",
		Mode: "managed",
		Managed: &RuntimeManagedJSON{
			Binaries: binaries,
		},
		JVM: &JVMConfigJSON{
			JavaVersion: data.JavaVersion,
		},
	}
}

// FNMRuntimeData holds the FNM-specific runtime configuration data.
type FNMRuntimeData struct {
	NodeVersion string
	PNPMVersion string
	PNPMHash    string
}

var pnpmHTTPClient = &http.Client{
	Timeout: 2 * time.Minute,
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

type npmVersionMetaForPull struct {
	Dist struct {
		Tarball string `json:"tarball"`
	} `json:"dist"`
}

func pullFNMRuntime() (*FNMRuntimeData, binmanager.MapOfBinaries, error) {
	data := &FNMRuntimeData{}

	nodeVersion, err := registry.GetLatestNodeLTSVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v (using fallback)\n", err)
	}
	data.NodeVersion = nodeVersion

	pnpmInfo, err := registry.GetNPMPackageInfo("pnpm")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch PNPM version: %w", err)
	}
	data.PNPMVersion = pnpmInfo.Version

	pnpmHash, err := fetchPNPMTarballHash(pnpmInfo.Version)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to compute PNPM hash: %w", err)
	}
	data.PNPMHash = pnpmHash

	client := github.NewClient()
	release, err := client.GetLatestRelease("Schniz", "fnm")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch FNM release: %w", err)
	}

	fmt.Printf("FNM release: %s (%d assets)\n", release.TagName, len(release.Assets))

	// Detect binaries using same deduplication logic as pull-github
	binaries, err := detectRuntimeBinaries("fnm", release)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to detect FNM binaries: %w", err)
	}

	return data, binaries, nil
}

// fetchPNPMTarballHash downloads the PNPM tarball for the given version and
// computes its SHA-256 hash without writing to permanent storage.
func fetchPNPMTarballHash(version string) (string, error) {
	metaURL := fmt.Sprintf("https://registry.npmjs.org/pnpm/%s", version)
	resp, err := pnpmHTTPClient.Get(metaURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch PNPM metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("npm registry returned status %d for pnpm@%s", resp.StatusCode, version)
	}

	var meta npmVersionMetaForPull
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&meta); err != nil {
		return "", fmt.Errorf("failed to decode PNPM metadata: %w", err)
	}

	if meta.Dist.Tarball == "" {
		return "", fmt.Errorf("no tarball URL found for pnpm@%s", version)
	}

	if !strings.HasPrefix(meta.Dist.Tarball, "https://") {
		return "", fmt.Errorf("pnpm tarball URL is not HTTPS: %s", meta.Dist.Tarball)
	}

	tarResp, err := pnpmHTTPClient.Get(meta.Dist.Tarball)
	if err != nil {
		return "", fmt.Errorf("failed to download PNPM tarball: %w", err)
	}
	defer func() { _ = tarResp.Body.Close() }()

	if tarResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pnpm tarball download returned status %d", tarResp.StatusCode)
	}

	const maxSize = 100 * 1024 * 1024 // 100 MiB
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(tarResp.Body, maxSize+1))
	if err != nil {
		return "", fmt.Errorf("failed to read PNPM tarball: %w", err)
	}
	if written > maxSize {
		return "", fmt.Errorf("pnpm tarball exceeds maximum size of %d bytes", maxSize)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// UVRuntimeData holds the UV-specific runtime configuration data.
type UVRuntimeData struct {
	PythonVersion string
}

func pullUVRuntime() (*UVRuntimeData, binmanager.MapOfBinaries, error) {
	data := &UVRuntimeData{}

	pythonVersion, err := registry.GetLatestPythonStableVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v (using fallback)\n", err)
	}
	data.PythonVersion = pythonVersion

	client := github.NewClient()
	release, err := client.GetLatestRelease("astral-sh", "uv")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch UV release: %w", err)
	}

	fmt.Printf("UV release: %s (%d assets)\n", release.TagName, len(release.Assets))

	binaries, err := detectRuntimeBinaries("uv", release)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to detect UV binaries: %w", err)
	}

	return data, binaries, nil
}

// JVMRuntimeData holds the JVM-specific runtime configuration data.
type JVMRuntimeData struct {
	JavaVersion string
}

func pullJVMRuntime() (*JVMRuntimeData, binmanager.MapOfBinaries, error) {
	data := &JVMRuntimeData{}

	javaVersion, err := registry.GetLatestTemurinMajorVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v (using fallback)\n", err)
	}
	data.JavaVersion = javaVersion

	client := github.NewClient()

	repo := fmt.Sprintf("temurin%s-binaries", data.JavaVersion)
	release, err := client.GetLatestRelease("adoptium", repo)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch JVM release from adoptium/%s: %w", repo, err)
	}

	fmt.Printf("JVM release: %s (%d assets)\n", release.TagName, len(release.Assets))

	binaries, err := detectJVMBinaries(release)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to detect JVM binaries: %w", err)
	}

	return data, binaries, nil
}

// jvmBinaryPath returns the path to the java binary within the extracted JDK tree.
// macOS: {tag}/Contents/Home/bin/java, Linux/others: {tag}/bin/java
func jvmBinaryPath(tagName string, osType syslist.OsType) string {
	if osType == syslist.OsTypeDarwin {
		return tagName + "/Contents/Home/bin/java"
	}
	if osType == syslist.OsTypeWindows {
		return tagName + "/bin/java.exe"
	}
	return tagName + "/bin/java"
}

// detectJVMBinaries detects JDK binaries from a Temurin release.
// Sets ExtractDir=true and computes OS-specific binaryPath for each entry.
func detectJVMBinaries(release *github.Release) (binmanager.MapOfBinaries, error) {
	platforms := buildPlatformTuples()
	binaries := make(binmanager.MapOfBinaries)

	type seenAsset struct {
		url  string
		hash string
	}
	seenByOsArch := make(map[syslist.OsType]map[syslist.ArchType]*seenAsset)

	successCount := 0
	deduplicatedCount := 0

	for _, platform := range platforms {
		asset, err := detector.DetectBinary(release.Assets, platform.os, platform.arch, platform.libc)
		if err != nil {
			continue
		}

		if platform.libc != "unknown" {
			detectedLibc := detector.DetectLibcFromFilename(asset.Name)
			if detectedLibc != "" && detectedLibc != platform.libc {
				continue
			}
		}

		contentType := detector.DetectContentType(asset.Name)

		hash, err := extractHashFromDigest(asset.Digest)
		if err != nil {
			return nil, fmt.Errorf("platform %s/%s/%s: %w", platform.os, platform.arch, platform.libc, err)
		}

		if platform.libc == "musl" {
			if osMap, ok := seenByOsArch[platform.os]; ok {
				if seen, ok := osMap[platform.arch]; ok {
					if seen.url == asset.BrowserDownloadURL && seen.hash == hash {
						fmt.Printf("  Skipping musl for %s/%s: same binary as glibc\n", platform.os, platform.arch)
						deduplicatedCount++
						continue
					}
				}
			}
		}

		bp := jvmBinaryPath(release.TagName, platform.os)
		binInfo := binmanager.BinaryOsArchInfo{
			URL:         asset.BrowserDownloadURL,
			Hash:        hash,
			ContentType: contentType,
			BinaryPath:  &bp,
			ExtractDir:  true,
		}

		if binaries[platform.os] == nil {
			binaries[platform.os] = make(map[syslist.ArchType]map[string]binmanager.BinaryOsArchInfo)
		}
		if binaries[platform.os][platform.arch] == nil {
			binaries[platform.os][platform.arch] = make(map[string]binmanager.BinaryOsArchInfo)
		}
		binaries[platform.os][platform.arch][platform.libc] = binInfo

		if platform.libc == "glibc" {
			if seenByOsArch[platform.os] == nil {
				seenByOsArch[platform.os] = make(map[syslist.ArchType]*seenAsset)
			}
			seenByOsArch[platform.os][platform.arch] = &seenAsset{
				url:  asset.BrowserDownloadURL,
				hash: hash,
			}
		}

		successCount++
	}

	if successCount == 0 {
		return nil, fmt.Errorf("no JDK binaries were detected")
	}

	if deduplicatedCount > 0 {
		fmt.Printf("  jvm: %d detected, %d deduplicated\n", successCount, deduplicatedCount)
	} else {
		fmt.Printf("  jvm: %d detected\n", successCount)
	}

	return binaries, nil
}

// detectRuntimeBinaries detects binaries from a GitHub release for a runtime,
// using the same platform tuples and deduplication logic as pull-github.
func detectRuntimeBinaries(name string, release *github.Release) (binmanager.MapOfBinaries, error) {
	platforms := buildPlatformTuples()
	binaries := make(binmanager.MapOfBinaries)

	type seenAsset struct {
		url  string
		hash string
	}
	seenByOsArch := make(map[syslist.OsType]map[syslist.ArchType]*seenAsset)

	successCount := 0
	deduplicatedCount := 0

	for _, platform := range platforms {
		asset, err := detector.DetectBinary(release.Assets, platform.os, platform.arch, platform.libc)
		if err != nil {
			continue
		}

		// Reject libc mismatches
		if platform.libc != "unknown" {
			detectedLibc := detector.DetectLibcFromFilename(asset.Name)
			if detectedLibc != "" && detectedLibc != platform.libc {
				continue
			}
		}

		contentType := detector.DetectContentType(asset.Name)

		binaryPath := detector.DetectBinaryPathWithHistory(
			name,
			asset.Name,
			contentType,
			platform.os,
			nil,
		)

		hash, err := extractHashFromDigest(asset.Digest)
		if err != nil {
			return nil, fmt.Errorf("platform %s/%s/%s: %w", platform.os, platform.arch, platform.libc, err)
		}

		// Deduplicate: musl with same URL+hash as glibc → skip
		if platform.libc == "musl" {
			if osMap, ok := seenByOsArch[platform.os]; ok {
				if seen, ok := osMap[platform.arch]; ok {
					if seen.url == asset.BrowserDownloadURL && seen.hash == hash {
						fmt.Printf("  Skipping musl for %s/%s: same binary as glibc\n", platform.os, platform.arch)
						deduplicatedCount++
						continue
					}
				}
			}
		}

		binInfo := binmanager.BinaryOsArchInfo{
			URL:         asset.BrowserDownloadURL,
			Hash:        hash,
			ContentType: contentType,
			BinaryPath:  binaryPath,
		}

		if binaries[platform.os] == nil {
			binaries[platform.os] = make(map[syslist.ArchType]map[string]binmanager.BinaryOsArchInfo)
		}
		if binaries[platform.os][platform.arch] == nil {
			binaries[platform.os][platform.arch] = make(map[string]binmanager.BinaryOsArchInfo)
		}
		binaries[platform.os][platform.arch][platform.libc] = binInfo

		if platform.libc == "glibc" {
			if seenByOsArch[platform.os] == nil {
				seenByOsArch[platform.os] = make(map[syslist.ArchType]*seenAsset)
			}
			seenByOsArch[platform.os][platform.arch] = &seenAsset{
				url:  asset.BrowserDownloadURL,
				hash: hash,
			}
		}

		successCount++
	}

	if successCount == 0 {
		return nil, fmt.Errorf("no binaries were detected for %s", name)
	}

	if deduplicatedCount > 0 {
		fmt.Printf("  %s: %d detected, %d deduplicated\n", name, successCount, deduplicatedCount)
	} else {
		fmt.Printf("  %s: %d detected\n", name, successCount)
	}

	return binaries, nil
}
