package cmd

import (
	"github.com/datamitsu/datamitsu/internal/appstate"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/detector"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	updateFlag           bool
	verifyExtractionFlag bool
)

var devtoolsCmd = &cobra.Command{
	Use:   "devtools",
	Short: "Development tools for maintaining datamitsu",
	Long:  `Development tools for maintaining datamitsu binary configurations`,
}

var pullGithubCmd = &cobra.Command{
	Use:   "pull-github <file>",
	Short: "Update binary configurations from GitHub releases",
	Long: `Update binary configurations from GitHub releases using auto-detection.

Requires a file argument pointing to the GitHub apps JSON file.
If the file does not exist, an empty one will be created.

Without --update: refreshes binaries using current tags
With --update: fetches latest release tags and updates binaries

Example:
  datamitsu devtools pull-github config/src/githubApps.json
  datamitsu devtools pull-github config/src/githubApps.json --update`,
	Args: cobra.ExactArgs(1),
	RunE: runPullGithub,
}

func init() {
	rootCmd.AddCommand(devtoolsCmd)
	devtoolsCmd.AddCommand(pullGithubCmd)
	devtoolsCmd.AddCommand(packInlineArchiveCmd)
	pullGithubCmd.Flags().BoolVar(&updateFlag, "update", false,
		"Fetch latest release tags before updating binaries")
	pullGithubCmd.Flags().BoolVar(&verifyExtractionFlag, "verify-extraction", false,
		"Download and verify binary extraction for all platforms before saving")
}

func ensureGitHubAppsJSONExists(path string) error {
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking file: %w", err)
	}
	if os.IsNotExist(err) {
		emptyState := []byte("{\"apps\":{},\"binaries\":{}}\n")
		tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
		if err != nil {
			return fmt.Errorf("failed to create temp file: %w", err)
		}
		tmpPath := tmpFile.Name()
		if _, err := tmpFile.Write(emptyState); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to write temp file: %w", err)
		}
		if err := tmpFile.Close(); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to close temp file: %w", err)
		}
		if err := os.Chmod(tmpPath, 0644); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to chmod temp file: %w", err)
		}
		if err := os.Rename(tmpPath, path); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to rename temp file: %w", err)
		}
	}
	return nil
}

func runPullGithub(cmd *cobra.Command, args []string) error {
	// Get file path from positional argument
	githubAppsPath := args[0]

	// Create file if it doesn't exist
	if err := ensureGitHubAppsJSONExists(githubAppsPath); err != nil {
		return fmt.Errorf("failed to ensure file exists: %w", err)
	}

	// Load configuration file
	fmt.Printf("Loading %s...\n", githubAppsPath)
	state, err := appstate.Load(githubAppsPath)
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", githubAppsPath, err)
	}

	if len(state.Apps) == 0 {
		fmt.Printf("No apps found in %s\n", githubAppsPath)
		return nil
	}

	// Create GitHub client
	client := github.NewClient()

	// Process each app
	for appName, metadata := range state.Apps {
		fmt.Printf("\n=== Processing %s ===\n", appName)

		// Validate metadata
		if err := appstate.Validate(appName, metadata); err != nil {
			fmt.Fprintf(os.Stderr, "Skipping %s: %v\n", appName, err)
			continue
		}

		fmt.Printf("App: %s (%s/%s)\n", appName, metadata.Owner, metadata.Repo)
		fmt.Printf("Current tag: %s\n", metadata.Tag)

		// If --update flag is set, fetch latest release first
		var release *github.Release
		effectiveTag := metadata.Tag
		if updateFlag {
			fmt.Printf("Fetching latest release...\n")
			release, err = client.GetLatestRelease(metadata.Owner, metadata.Repo)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error fetching latest release: %v\n", err)
				continue
			}
			if release.TagName != metadata.Tag {
				fmt.Printf("Latest release: %s (updating from %s)\n", release.TagName, metadata.Tag)
				effectiveTag = release.TagName
			} else {
				fmt.Printf("Latest release: %s (already up to date)\n", release.TagName)
			}
		}

		// Compute config hash using effective tag (not yet committed to state)
		hashMetadata := &appstate.AppMetadata{
			Owner: metadata.Owner,
			Repo:  metadata.Repo,
			Tag:   effectiveTag,
		}
		currentHash := appstate.ComputeConfigHash(hashMetadata)

		// Check if binaries already exist and config hasn't changed
		if state.Binaries[appName] != nil && state.Binaries[appName].ConfigHash == currentHash {
			fmt.Printf("Config unchanged (hash: %s), skipping binary detection\n", currentHash[:8])
			continue
		}

		// Fetch release if not already fetched
		if release == nil {
			fmt.Printf("Fetching release %s...\n", effectiveTag)
			release, err = client.GetRelease(metadata.Owner, metadata.Repo, effectiveTag)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error fetching release: %v\n", err)
				continue
			}
		}

		// Build binaries into a temporary entry to avoid mutating shared state on failure
		binariesEntry, err := buildBinariesForApp(appName, release, currentHash, state)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error updating binaries for %s: %v\n", appName, err)
			continue
		}

		// Fetch repository description (matches FNM/UV pattern: use fetched if non-empty, else preserve existing)
		desc := ""
		repoInfo, err := client.GetRepository(metadata.Owner, metadata.Repo)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to fetch repository description for %s: %v\n", appName, err)
		} else if repoInfo != nil {
			desc = repoInfo.Description
		}
		if desc == "" {
			if existing := state.Binaries[appName]; existing != nil {
				desc = existing.Description
			}
		}
		binariesEntry.Description = desc

		// Commit changes to state only after full success
		metadata.Tag = effectiveTag
		state.Binaries[appName] = binariesEntry

		// Save immediately after each app update to prevent data loss
		fmt.Printf("Saving %s...\n", githubAppsPath)
		if err := appstate.Save(githubAppsPath, state); err != nil {
			return fmt.Errorf("failed to save after %s: %w", appName, err)
		}
	}

	// Final summary
	fmt.Printf("\n✓ Processed %d apps\n", len(state.Apps))
	fmt.Printf("✓ Configuration saved to %s\n", githubAppsPath)
	return nil
}

type platformTuple struct {
	os   syslist.OsType
	arch syslist.ArchType
	libc string
}

func buildPlatformTuples() []platformTuple {
	baseArches := []syslist.ArchType{syslist.ArchTypeAmd64, syslist.ArchTypeArm64}
	nonLinuxOSes := []syslist.OsType{syslist.OsTypeDarwin, syslist.OsTypeWindows, syslist.OsTypeFreebsd, syslist.OsTypeOpenbsd}
	linuxLibcs := []string{"glibc", "musl"}

	var tuples []platformTuple

	for _, osType := range nonLinuxOSes {
		for _, arch := range baseArches {
			tuples = append(tuples, platformTuple{os: osType, arch: arch, libc: "unknown"})
		}
	}

	for _, arch := range baseArches {
		for _, libc := range linuxLibcs {
			tuples = append(tuples, platformTuple{os: syslist.OsTypeLinux, arch: arch, libc: libc})
		}
	}

	return tuples
}

type detectionResult struct {
	os          syslist.OsType
	arch        syslist.ArchType
	libc        string
	status      string
	assetName   string
	contentType binmanager.BinContentType
	binaryPath  *string
	err         error
}

func buildBinariesForApp(appName string, release *github.Release, configHash string, state *appstate.State) (*appstate.BinariesEntry, error) {
	fmt.Printf("\nDetecting binaries:\n")

	// Build into a fresh entry to avoid mutating shared state on failure
	entry := &appstate.BinariesEntry{
		Binaries: make(binmanager.MapOfBinaries),
	}

	// Use existing binaries for historical learning only (read-only)
	var historicalBinaries binmanager.MapOfBinaries
	if state.Binaries[appName] != nil && state.Binaries[appName].Binaries != nil {
		historicalBinaries = state.Binaries[appName].Binaries
	}

	platforms := buildPlatformTuples()

	// Track seen assets per OS/arch for deduplication.
	// When the same binary (URL+hash) is detected for both glibc and musl,
	// we keep only the glibc entry — the resolver handles fallback.
	type seenAsset struct {
		url  string
		hash string
	}
	seenByOsArch := make(map[syslist.OsType]map[syslist.ArchType]*seenAsset)

	var results []detectionResult
	successCount := 0
	notAvailableCount := 0
	noHashCount := 0
	verificationFailedCount := 0
	deduplicatedCount := 0

	for _, platform := range platforms {
		asset, err := detector.DetectBinary(release.Assets, platform.os, platform.arch, platform.libc)
		if err != nil {
			results = append(results, detectionResult{
				os:     platform.os,
				arch:   platform.arch,
				libc:   platform.libc,
				status: "not_available",
				err:    err,
			})
			notAvailableCount++
			continue
		}

		// Reject libc mismatches: the detected libc of the asset must not conflict
		// with the requested libc. This prevents storing e.g. a musl-only asset
		// under the glibc key.
		if platform.libc != "unknown" {
			detectedLibc := detector.DetectLibcFromFilename(asset.Name)
			if detectedLibc != "" && detectedLibc != platform.libc {
				results = append(results, detectionResult{
					os:     platform.os,
					arch:   platform.arch,
					libc:   platform.libc,
					status: "not_available",
					err:    fmt.Errorf("asset %q is %s, not %s", asset.Name, detectedLibc, platform.libc),
				})
				notAvailableCount++
				continue
			}
		}

		// Detect content type
		contentType := detector.DetectContentType(asset.Name)

		// Detect binary path using historical learning (read-only access)
		binaryPath := detector.DetectBinaryPathWithHistory(
			appName,
			asset.Name,
			contentType,
			platform.os,
			historicalBinaries,
		)

		// Extract SHA256 hash from digest
		hash, err := extractHashFromDigest(asset.Digest)
		if err != nil {
			results = append(results, detectionResult{
				os:        platform.os,
				arch:      platform.arch,
				libc:      platform.libc,
				status:    "no_hash",
				assetName: asset.Name,
				err:       err,
			})
			noHashCount++
			continue
		}

		// Deduplicate: if this is a musl tuple and the glibc entry for the same
		// OS/arch has the same URL+hash, skip to avoid duplicate entries.
		if platform.libc == "musl" {
			if osMap, ok := seenByOsArch[platform.os]; ok {
				if seen, ok := osMap[platform.arch]; ok {
					if seen.url == asset.BrowserDownloadURL && seen.hash == hash {
						fmt.Printf("  Skipping musl for %s/%s: same binary as glibc\n", platform.os, platform.arch)
						results = append(results, detectionResult{
							os:     platform.os,
							arch:   platform.arch,
							libc:   platform.libc,
							status: "deduplicated",
						})
						deduplicatedCount++
						continue
					}
				}
			}
		}

		// Verify extraction if flag is enabled
		if verifyExtractionFlag {
			hashType := binmanager.BinHashTypeSHA256
			if err := binmanager.VerifyBinaryExtraction(
				asset.BrowserDownloadURL,
				hash,
				hashType,
				contentType,
				binaryPath,
			); err != nil {
				results = append(results, detectionResult{
					os:          platform.os,
					arch:        platform.arch,
					libc:        platform.libc,
					status:      "verification_failed",
					assetName:   asset.Name,
					contentType: contentType,
					binaryPath:  binaryPath,
					err:         err,
				})
				verificationFailedCount++
				continue
			}
		}

		// Create binary info
		binInfo := binmanager.BinaryOsArchInfo{
			URL:         asset.BrowserDownloadURL,
			Hash:        hash,
			ContentType: contentType,
			BinaryPath:  binaryPath,
		}

		// Ensure OS, arch, and libc maps exist in the new entry
		if entry.Binaries[platform.os] == nil {
			entry.Binaries[platform.os] = make(map[syslist.ArchType]map[string]binmanager.BinaryOsArchInfo)
		}
		if entry.Binaries[platform.os][platform.arch] == nil {
			entry.Binaries[platform.os][platform.arch] = make(map[string]binmanager.BinaryOsArchInfo)
		}

		entry.Binaries[platform.os][platform.arch][platform.libc] = binInfo

		// Track for deduplication (glibc is processed before musl)
		if platform.libc == "glibc" {
			if seenByOsArch[platform.os] == nil {
				seenByOsArch[platform.os] = make(map[syslist.ArchType]*seenAsset)
			}
			seenByOsArch[platform.os][platform.arch] = &seenAsset{
				url:  asset.BrowserDownloadURL,
				hash: hash,
			}
		}

		results = append(results, detectionResult{
			os:          platform.os,
			arch:        platform.arch,
			libc:        platform.libc,
			status:      "success",
			assetName:   asset.Name,
			contentType: contentType,
			binaryPath:  binaryPath,
		})
		successCount++
	}

	printDetectionResults(results, verifyExtractionFlag)

	if successCount == 0 {
		return nil, fmt.Errorf("no binaries were detected")
	}

	if noHashCount > 0 {
		return nil, fmt.Errorf("%d platform(s) missing SHA-256 hash (mandatory per security policy)", noHashCount)
	}

	entry.ConfigHash = configHash

	if verifyExtractionFlag && verificationFailedCount > 0 {
		fmt.Printf("\nSummary: %d detected, %d not available, %d deduplicated, %d verification failed\n",
			successCount, notAvailableCount, deduplicatedCount, verificationFailedCount)
	} else if deduplicatedCount > 0 {
		fmt.Printf("\nSummary: %d detected, %d not available, %d deduplicated\n",
			successCount, notAvailableCount, deduplicatedCount)
	} else {
		fmt.Printf("\nSummary: %d detected, %d not available\n", successCount, notAvailableCount)
	}
	return entry, nil
}

func formatPlatformLabel(r detectionResult) string {
	if r.libc != "" && r.libc != "unknown" {
		return fmt.Sprintf("%s/%s/%s", r.os, r.arch, r.libc)
	}
	return fmt.Sprintf("%s/%s", r.os, r.arch)
}

func printDetectionResults(results []detectionResult, verifyMode bool) {
	const (
		colorGreen  = "\033[32m"
		colorYellow = "\033[33m"
		colorRed    = "\033[31m"
		colorReset  = "\033[0m"
	)

	var successful []detectionResult
	var notAvailable []detectionResult
	var verificationFailed []detectionResult
	var noHash []detectionResult
	var deduplicated []detectionResult

	for _, r := range results {
		switch r.status {
		case "success":
			successful = append(successful, r)
		case "not_available":
			notAvailable = append(notAvailable, r)
		case "verification_failed":
			verificationFailed = append(verificationFailed, r)
		case "no_hash":
			noHash = append(noHash, r)
		case "deduplicated":
			deduplicated = append(deduplicated, r)
		}
	}

	if len(successful) > 0 {
		fmt.Printf("\nSuccessfully detected:\n")
		for _, r := range successful {
			binaryPathStr := "nil"
			if r.binaryPath != nil {
				binaryPathStr = *r.binaryPath
			}
			verifiedStr := ""
			if verifyMode {
				verifiedStr = " (verified)"
			}
			fmt.Printf("  %s✓%s %s: %s (contentType: %s, binaryPath: %s)%s\n",
				colorGreen, colorReset, formatPlatformLabel(r), r.assetName, r.contentType, binaryPathStr, verifiedStr)
		}
	}

	if len(verificationFailed) > 0 {
		fmt.Printf("\nVerification failed:\n")
		for _, r := range verificationFailed {
			fmt.Printf("  %s✗%s %s: %s - %v\n",
				colorRed, colorReset, formatPlatformLabel(r), r.assetName, r.err)
		}
	}

	if len(noHash) > 0 {
		fmt.Printf("\nNo SHA-256 hash available:\n")
		for _, r := range noHash {
			fmt.Printf("  %s✗%s %s: %s - %v\n",
				colorRed, colorReset, formatPlatformLabel(r), r.assetName, r.err)
		}
	}

	if len(deduplicated) > 0 {
		fmt.Printf("\nDeduplicated (same binary as glibc):\n")
		for _, r := range deduplicated {
			fmt.Printf("  %s⚠%s %s: skipped, same binary as glibc\n",
				colorYellow, colorReset, formatPlatformLabel(r))
		}
	}

	if len(notAvailable) > 0 {
		fmt.Printf("\nNot available:\n")
		for _, r := range notAvailable {
			fmt.Printf("  %s⚠%s %s: no matching binary found\n",
				colorYellow, colorReset, formatPlatformLabel(r))
		}
	}
}

// extractHashFromDigest extracts the SHA-256 hash value from GitHub digest field.
// Only accepts "sha256:<64 hex chars>" format. Returns error for invalid formats.
func extractHashFromDigest(digest string) (string, error) {
	if digest == "" {
		return "", fmt.Errorf("empty digest")
	}

	parts := strings.SplitN(digest, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid digest format %q: missing algorithm prefix", digest)
	}

	if parts[0] != "sha256" {
		return "", fmt.Errorf("unsupported digest algorithm %q (only sha256 supported)", parts[0])
	}

	hashValue := parts[1]
	if len(hashValue) != 64 {
		return "", fmt.Errorf("invalid SHA-256 hash length %d (expected 64) in digest %q", len(hashValue), digest)
	}

	if _, err := hex.DecodeString(hashValue); err != nil {
		return "", fmt.Errorf("invalid hex in SHA-256 hash %q: %w", hashValue, err)
	}

	return hashValue, nil
}
