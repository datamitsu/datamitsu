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
	"github.com/datamitsu/datamitsu/internal/nodekeys"
	"github.com/datamitsu/datamitsu/internal/registry"
	"github.com/datamitsu/datamitsu/internal/syslist"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/spf13/cobra"
)

var (
	pullRuntimesUpdateFlag  bool
	pullRuntimesDryRunFlag  bool
	pullRuntimesRuntimeFlag string
)

var validRuntimeNames = []string{"uv", "jvm", "node"}

func init() {
	devtoolsCmd.AddCommand(pullRuntimesCmd)
	pullRuntimesCmd.Flags().BoolVar(&pullRuntimesUpdateFlag, "update", false,
		"Fetch latest versions from upstream before updating")
	pullRuntimesCmd.Flags().BoolVar(&pullRuntimesDryRunFlag, "dry-run", false,
		"Show what would be updated without writing files")
	pullRuntimesCmd.Flags().StringVar(&pullRuntimesRuntimeFlag, "runtime", "",
		"Update only the specified runtime (uv, jvm, or node)")
}

var pullRuntimesCmd = &cobra.Command{
	Use:   "pull-runtimes <file>",
	Short: "Pull runtime configurations from upstream releases",
	Long: `Pull runtime configurations (UV, JVM, Node) with latest versions from upstream.

Fetches latest releases from GitHub, computes SHA-256 hashes, and writes
the result to the specified file. The Node runtime is pulled as a direct
archive (url + hash) from nodejs.org (glibc/darwin/windows, GPG-verified) and
unofficial-builds.nodejs.org (musl, unsigned).

Requires --update flag to fetch releases (safety guard).
With --runtime: updates only the specified runtime (uv, jvm, or node)
With --dry-run: shows what would be updated without writing

Example:
  datamitsu devtools pull-runtimes --update config/src/runtimes.json
  datamitsu devtools pull-runtimes --update --runtime uv config/src/runtimes.json
  datamitsu devtools pull-runtimes --update --runtime node config/src/runtimes.json
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
		case "node":
			var data *NodeRuntimeData
			var binaries binmanager.MapOfBinaries
			data, binaries, updateErr = pullNodeRuntime()
			if updateErr == nil {
				runtimeJSON = buildNodeRuntimeJSON(data, binaries)
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
	if r.UV != nil {
		parts = append(parts, fmt.Sprintf("python=%s", r.UV.PythonVersion))
	}
	if r.JVM != nil {
		parts = append(parts, fmt.Sprintf("java=%s", r.JVM.JavaVersion))
	}
	if r.Node != nil {
		parts = append(parts, fmt.Sprintf("node=%s,pnpm=%s", r.Node.NodeVersion, r.Node.PNPMVersion))
	}
	if r.Go != nil {
		parts = append(parts, fmt.Sprintf("go=%s", r.Go.GoVersion))
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
	UV      *UVConfigJSON       `json:"uv,omitempty"`
	JVM     *JVMConfigJSON      `json:"jvm,omitempty"`
	Node    *NodeConfigJSON     `json:"node,omitempty"`
	Go      *GoConfigJSON       `json:"go,omitempty"`
}

// RuntimeManagedJSON holds the managed binary configuration for a runtime.
type RuntimeManagedJSON struct {
	Binaries binmanager.MapOfBinaries `json:"binaries"`
}

// UVConfigJSON holds UV-specific configuration in the JSON output.
type UVConfigJSON struct {
	PythonVersion string `json:"pythonVersion"`
}

// JVMConfigJSON holds JVM-specific configuration in the JSON output.
type JVMConfigJSON struct {
	JavaVersion string `json:"javaVersion"`
}

// NodeConfigJSON holds Node-specific configuration in the JSON output.
type NodeConfigJSON struct {
	NodeVersion string `json:"nodeVersion"`
	PNPMVersion string `json:"pnpmVersion"`
	PNPMHash    string `json:"pnpmHash"`
}

// GoConfigJSON holds Go-specific configuration in the JSON output.
type GoConfigJSON struct {
	GoVersion string `json:"goVersion"`
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

// buildNodeRuntimeJSON constructs a RuntimeJSON from Node updater results.
func buildNodeRuntimeJSON(data *NodeRuntimeData, binaries binmanager.MapOfBinaries) *RuntimeJSON {
	return &RuntimeJSON{
		Kind: "node",
		Mode: "managed",
		Managed: &RuntimeManagedJSON{
			Binaries: binaries,
		},
		Node: &NodeConfigJSON{
			NodeVersion: data.NodeVersion,
			PNPMVersion: data.PNPMVersion,
			PNPMHash:    data.PNPMHash,
		},
	}
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

// filterJDKAssets returns only assets that are JDK product type, identified
// by the "-jdk_" pattern in Temurin's naming convention:
// OpenJDK{ver}U-{type}_{arch}_{os}_hotspot_{version}.{ext}
// This rejects jre, debugimage, static-libs, static-libs-glibc, jmods, sbom,
// sources, and other non-runtime asset types.
func filterJDKAssets(assets []github.Asset) []github.Asset {
	filtered := make([]github.Asset, 0, len(assets))
	for _, asset := range assets {
		if strings.Contains(strings.ToLower(asset.Name), "-jdk_") {
			filtered = append(filtered, asset)
		}
	}
	return filtered
}

// detectJVMBinaries detects JDK binaries from a Temurin release.
// Sets ExtractDir=true and computes OS-specific binaryPath for each entry.
func detectJVMBinaries(release *github.Release) (binmanager.MapOfBinaries, error) {
	platforms := buildPlatformTuples()
	binaries := make(binmanager.MapOfBinaries)

	jdkAssets := filterJDKAssets(release.Assets)

	type seenAsset struct {
		url  string
		hash string
	}
	seenByOsArch := make(map[syslist.OsType]map[syslist.ArchType]*seenAsset)

	successCount := 0
	deduplicatedCount := 0

	for _, platform := range platforms {
		asset, err := detector.DetectBinary(jdkAssets, platform.os, platform.arch, platform.libc)
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

// Node archive registry sources. Node is acquired as a direct, hash-pinned
// archive (jvm-style):
//   - glibc / darwin / windows archives live on nodejs.org, whose
//     SHASUMS256.txt manifest is GPG-signed by the Node release team.
//   - musl archives live on unofficial-builds.nodejs.org, whose SHASUMS256.txt
//     is unsigned (no Node release signature — same trust model as node:alpine).
const (
	nodeDistBaseURL = "https://nodejs.org/dist"
	nodeMuslBaseURL = "https://unofficial-builds.nodejs.org/download/release"

	// maxShasumsSize caps SHASUMS manifest downloads (real manifests are a few KiB).
	maxShasumsSize = 1 << 20 // 1 MiB
)

// NodeRuntimeData holds the Node-specific runtime configuration data.
type NodeRuntimeData struct {
	NodeVersion string
	PNPMVersion string
	PNPMHash    string
}

// nodePullConfig configures Node binary detection so tests can inject mock
// SHASUMS hosts and a test keyring instead of reaching nodejs.org.
type nodePullConfig struct {
	version     string
	distBaseURL string
	muslBaseURL string
	keyring     openpgp.KeyRing
	client      *http.Client
}

// nodeArchiveSpec describes one os/arch/libc Node archive tuple.
type nodeArchiveSpec struct {
	os          syslist.OsType
	arch        syslist.ArchType
	libc        string
	filename    string
	contentType binmanager.BinContentType
	musl        bool
}

// nodeArchSuffix maps a datamitsu arch to Node's release filename arch token.
func nodeArchSuffix(arch syslist.ArchType) string {
	if arch == syslist.ArchTypeAmd64 {
		return "x64"
	}
	return string(arch) // arm64
}

// nodeArchiveSpecs enumerates the os/arch/libc tuples datamitsu ships Node for,
// with the upstream archive filename for each (see the plan's naming table).
func nodeArchiveSpecs(version string) []nodeArchiveSpec {
	base := "node-v" + version
	amd64, arm64 := nodeArchSuffix(syslist.ArchTypeAmd64), nodeArchSuffix(syslist.ArchTypeArm64)
	return []nodeArchiveSpec{
		{syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", base + "-linux-" + amd64 + ".tar.xz", binmanager.BinContentTypeTarXz, false},
		{syslist.OsTypeLinux, syslist.ArchTypeAmd64, "musl", base + "-linux-" + amd64 + "-musl.tar.xz", binmanager.BinContentTypeTarXz, true},
		{syslist.OsTypeLinux, syslist.ArchTypeArm64, "glibc", base + "-linux-" + arm64 + ".tar.xz", binmanager.BinContentTypeTarXz, false},
		{syslist.OsTypeLinux, syslist.ArchTypeArm64, "musl", base + "-linux-" + arm64 + "-musl.tar.xz", binmanager.BinContentTypeTarXz, true},
		{syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown", base + "-darwin-" + amd64 + ".tar.xz", binmanager.BinContentTypeTarXz, false},
		{syslist.OsTypeDarwin, syslist.ArchTypeArm64, "unknown", base + "-darwin-" + arm64 + ".tar.xz", binmanager.BinContentTypeTarXz, false},
		{syslist.OsTypeWindows, syslist.ArchTypeAmd64, "unknown", base + "-win-" + amd64 + ".zip", binmanager.BinContentTypeZip, false},
		{syslist.OsTypeWindows, syslist.ArchTypeArm64, "unknown", base + "-win-" + arm64 + ".zip", binmanager.BinContentTypeZip, false},
	}
}

// nodeBinaryPath returns the path to the node executable within the extracted
// archive tree (extractDir layout): "<dir>/bin/node" for tar.xz archives,
// "<dir>/node.exe" for the windows zip.
func nodeBinaryPath(spec nodeArchiveSpec) string {
	if spec.contentType == binmanager.BinContentTypeZip {
		return strings.TrimSuffix(spec.filename, ".zip") + "/node.exe"
	}
	return strings.TrimSuffix(spec.filename, ".tar.xz") + "/bin/node"
}

// parseSHASUMS parses a SHASUMS256.txt manifest ("<hex>  <filename>" per line,
// the filename optionally prefixed with "*" for binary mode) into a
// filename→hash map.
func parseSHASUMS(content string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		out[name] = fields[0]
	}
	return out
}

// httpGetLimited GETs url and returns up to maxSize bytes of the body.
func httpGetLimited(client *http.Client, url string, maxSize int64) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("%s exceeds maximum size of %d bytes", url, maxSize)
	}
	return data, nil
}

// fetchVerifiedShasums downloads the clearsigned SHASUMS256.txt.asc, verifies
// its GPG signature against keyring, and parses the verified plaintext.
func fetchVerifiedShasums(client *http.Client, baseURL, version string, keyring openpgp.KeyRing) (map[string]string, error) {
	url := fmt.Sprintf("%s/v%s/SHASUMS256.txt.asc", baseURL, version)
	body, err := httpGetLimited(client, url, maxShasumsSize)
	if err != nil {
		return nil, err
	}
	plain, err := nodekeys.VerifyClearsigned(body, keyring)
	if err != nil {
		return nil, fmt.Errorf("provenance check failed for %s: %w", url, err)
	}
	return parseSHASUMS(string(plain)), nil
}

// fetchPlainShasums downloads an unsigned SHASUMS256.txt and parses it.
func fetchPlainShasums(client *http.Client, baseURL, version string) (map[string]string, error) {
	url := fmt.Sprintf("%s/v%s/SHASUMS256.txt", baseURL, version)
	body, err := httpGetLimited(client, url, maxShasumsSize)
	if err != nil {
		return nil, err
	}
	return parseSHASUMS(string(body)), nil
}

// isSHA256Hex reports whether s is a 64-character lowercase-or-uppercase hex
// string (a SHA-256 digest).
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// buildNodeBinaries assembles the MapOfBinaries for every Node archive tuple,
// looking each archive's SHA-256 up in the appropriate (verified) SHASUMS map
// and recording extractDir entries with computed binaryPaths. A missing or
// malformed hash is a hard error (security policy: no hash, no entry).
func buildNodeBinaries(cfg nodePullConfig, distShasums, muslShasums map[string]string) (binmanager.MapOfBinaries, error) {
	binaries := make(binmanager.MapOfBinaries)
	for _, spec := range nodeArchiveSpecs(cfg.version) {
		baseURL, shasums := cfg.distBaseURL, distShasums
		if spec.musl {
			baseURL, shasums = cfg.muslBaseURL, muslShasums
		}

		hash, ok := shasums[spec.filename]
		if !ok {
			return nil, fmt.Errorf("node %s: SHA-256 hash not found for %s in %s SHASUMS",
				cfg.version, spec.filename, libcLabel(spec))
		}
		// Normalize to lowercase so the recorded hash passes config validation
		// (config.isValidSHA256Hex requires 64 lowercase hex chars). The hex/length
		// guard below still rejects malformed values regardless of case.
		hash = strings.ToLower(hash)
		if !isSHA256Hex(hash) {
			return nil, fmt.Errorf("node %s: invalid SHA-256 hash %q for %s", cfg.version, hash, spec.filename)
		}

		bp := nodeBinaryPath(spec)
		binInfo := binmanager.BinaryOsArchInfo{
			URL:         fmt.Sprintf("%s/v%s/%s", baseURL, cfg.version, spec.filename),
			Hash:        hash,
			ContentType: spec.contentType,
			BinaryPath:  &bp,
			ExtractDir:  true,
		}

		if binaries[spec.os] == nil {
			binaries[spec.os] = make(map[syslist.ArchType]map[string]binmanager.BinaryOsArchInfo)
		}
		if binaries[spec.os][spec.arch] == nil {
			binaries[spec.os][spec.arch] = make(map[string]binmanager.BinaryOsArchInfo)
		}
		binaries[spec.os][spec.arch][spec.libc] = binInfo
	}
	return binaries, nil
}

// libcLabel describes which SHASUMS source a spec comes from, for error text.
func libcLabel(spec nodeArchiveSpec) string {
	if spec.musl {
		return "musl (unofficial-builds)"
	}
	return "dist (nodejs.org)"
}

// detectNodeBinaries fetches and verifies the Node release manifests and builds
// the per-tuple archive map. glibc/darwin/windows hashes come from the
// GPG-verified nodejs.org manifest; musl hashes come from the unsigned
// unofficial-builds manifest (logged as such).
func detectNodeBinaries(cfg nodePullConfig) (binmanager.MapOfBinaries, error) {
	distShasums, err := fetchVerifiedShasums(cfg.client, cfg.distBaseURL, cfg.version, cfg.keyring)
	if err != nil {
		return nil, fmt.Errorf("nodejs.org dist manifest: %w", err)
	}

	muslShasums, err := fetchPlainShasums(cfg.client, cfg.muslBaseURL, cfg.version)
	if err != nil {
		return nil, fmt.Errorf("unofficial-builds musl manifest: %w", err)
	}
	fmt.Printf("  node: musl SHASUMS from unofficial-builds.nodejs.org is unsigned " +
		"(no Node release signature; pinned by sha256 in git, matching node:alpine)\n")

	binaries, err := buildNodeBinaries(cfg, distShasums, muslShasums)
	if err != nil {
		return nil, err
	}

	count := 0
	for _, archMap := range binaries {
		for _, libcMap := range archMap {
			count += len(libcMap)
		}
	}
	fmt.Printf("  node: %d archives detected (glibc verified via GPG, musl unsigned)\n", count)
	return binaries, nil
}

// pullNodeRuntime resolves the latest Node version + pnpm, then fetches and
// verifies the Node release manifests to build the archive registry entry.
func pullNodeRuntime() (*NodeRuntimeData, binmanager.MapOfBinaries, error) {
	data := &NodeRuntimeData{}

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

	keyring, err := nodekeys.ReleaseKeyring()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load Node release keys: %w", err)
	}

	fmt.Printf("Node version: v%s (pnpm %s)\n", data.NodeVersion, data.PNPMVersion)

	binaries, err := detectNodeBinaries(nodePullConfig{
		version:     data.NodeVersion,
		distBaseURL: nodeDistBaseURL,
		muslBaseURL: nodeMuslBaseURL,
		keyring:     keyring,
		client:      pnpmHTTPClient,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to detect Node binaries: %w", err)
	}

	return data, binaries, nil
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
