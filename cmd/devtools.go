package cmd

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/datamitsu/datamitsu/internal/appstate"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/detector"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/syslist"

	"github.com/spf13/cobra"
)

var (
	updateFlag           bool
	verifyExtractionFlag bool
	pullGithubMinAge     *int
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
	pullGithubMinAge = addMinAgeFlag(pullGithubCmd)
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
		if err := os.Chmod(tmpPath, 0o644); err != nil {
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
	ctx := commandContext(cmd)

	// Get file path from positional argument
	githubAppsPath := args[0]

	// Resolve the effective minimum release age from runtime config + flag.
	eff, err := runtimeconfig.Get()
	if err != nil {
		return fmt.Errorf("failed to read runtime config: %w", err)
	}
	minAge := resolveMinAge(*pullGithubMinAge, eff)

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

	fmt.Printf("Minimum release age: %s\n", minAgeBanner(minAge))

	client := newGitHubClient()
	enableRetryNotices()

	// Every app is attempted; a failure is recorded and reported at the end,
	// and the run exits non-zero. Sorted so two runs read the same way.
	var failures []pullFailure
	names := slices.Sorted(maps.Keys(state.Apps))
	for i, appName := range names {
		fmt.Printf("\n=== Processing %s [%d/%d] ===\n", appName, i+1, len(names))
		for _, failure := range pullGithubApp(ctx, client, state, githubAppsPath, appName, minAge) {
			fmt.Fprintf(os.Stderr, "✗ %s: %s: %v\n", appName, failure.stage, failure.err)
			if failure.fatal {
				fmt.Fprintf(os.Stderr, "The run stopped at %s; the apps after it were not attempted.\n", appName)
				return failure.err
			}
			failures = append(failures, failure)
		}
	}

	return reportPullGithub(githubAppsPath, len(state.Apps), failures)
}

// pullFailure is one app pull-github could not finish: the stage that failed
// and why. A fatal failure — the file cannot be written — ends the run.
type pullFailure struct {
	app   string
	stage string
	err   error
	fatal bool
}

// pullGithubApp brings one app up to date and saves the file when it did. It
// returns nothing on success; on failure it leaves the app's entry as it was
// and returns what failed — one entry, or one per platform whose asset could
// not be verified.
func pullGithubApp(ctx context.Context, client *github.Client, state *appstate.State, githubAppsPath, appName string, minAge int) []pullFailure {
	metadata := state.Apps[appName]
	fail := func(stage string, err error) []pullFailure {
		return []pullFailure{{app: appName, stage: stage, err: err}}
	}

	if err := appstate.Validate(appName, metadata); err != nil {
		return fail("metadata", err)
	}

	fmt.Printf("App: %s (%s/%s)\n", appName, metadata.Owner, metadata.Repo)
	fmt.Printf("Current tag: %s\n", metadata.Tag)

	// If --update flag is set, fetch latest release first
	var release *github.Release
	effectiveTag := metadata.Tag
	if updateFlag {
		if minAge > 0 {
			fmt.Printf("Fetching latest release at least %d minutes old...\n", minAge)
		} else {
			fmt.Printf("Fetching latest release...\n")
		}
		// GetLatestReleaseWithMinAge falls through to GetLatestRelease when
		// minAge <= 0, so a nil release only happens under an active cutoff.
		var err error
		release, err = client.GetLatestReleaseWithMinAge(ctx, metadata.Owner, metadata.Repo, minAge)
		if err != nil {
			return fail("latest release", err)
		}
		switch {
		case release == nil:
			// No release is old enough under the active min-age cutoff.
			if state.Binaries[appName] == nil {
				// New app: nothing safe to install.
				return fail("latest release", noReleaseOldEnoughErr(appName, minAge))
			}
			// Existing app: warn and keep the current tag.
			fmt.Fprintf(os.Stderr,
				"Warning: no release for %s is at least %d minutes old; keeping current tag %s\n",
				appName, minAge, metadata.Tag)
		case release.TagName != metadata.Tag:
			fmt.Printf("Latest release: %s (updating from %s)\n", release.TagName, metadata.Tag)
			effectiveTag = release.TagName
		default:
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
		return nil
	}

	// Fetch release if not already fetched
	if release == nil {
		fmt.Printf("Fetching release %s...\n", effectiveTag)
		var err error
		release, err = client.GetRelease(ctx, metadata.Owner, metadata.Repo, effectiveTag)
		if err != nil {
			return fail("release "+effectiveTag, err)
		}
	}

	// Build binaries into a temporary entry to avoid mutating shared state on failure
	binariesEntry, err := buildBinariesForApp(ctx, appName, release, currentHash, state)
	if err != nil {
		if unverified, ok := errors.AsType[*verificationError](err); ok {
			failures := make([]pullFailure, 0, len(unverified.platforms))
			for _, p := range unverified.platforms {
				failures = append(failures, pullFailure{app: appName, stage: "verify " + p.platform, err: p.err})
			}
			return failures
		}
		return fail("binaries for "+release.TagName, err)
	}

	// Fetch repository description (matches node/UV pattern: use fetched if non-empty, else preserve existing)
	desc := ""
	repoInfo, err := client.GetRepository(ctx, metadata.Owner, metadata.Repo)
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
		return []pullFailure{{app: appName, stage: "save", err: fmt.Errorf("failed to save after %s: %w", appName, err), fatal: true}}
	}
	return nil
}

// verificationError is an app whose binaries were detected but could not all
// be verified: the platforms whose asset failed, each with the error named
// after the asset it belongs to. The app is not recorded, since an entry
// without those platforms would silently lose what the previous one had.
type verificationError struct {
	platforms []platformVerification
}

type platformVerification struct {
	platform string
	err      error
}

func (e *verificationError) Error() string {
	names := make([]string, 0, len(e.platforms))
	for _, p := range e.platforms {
		names = append(names, p.platform)
	}
	return fmt.Sprintf("verification failed for %d platform(s): %s", len(e.platforms), strings.Join(names, ", "))
}

// reportPullGithub prints the run's outcome. With failures it lists each app,
// the stage and the error, so nobody reads the log for them, and returns an
// error so the command exits non-zero. Every failed app is left as it was.
func reportPullGithub(githubAppsPath string, total int, failures []pullFailure) error {
	if len(failures) == 0 {
		fmt.Printf("\n✓ Processed %d apps\n", total)
		fmt.Printf("✓ Configuration saved to %s\n", githubAppsPath)
		return nil
	}

	// An app can fail on several platforms at once; it is still one app.
	failedApps := make(map[string]bool, len(failures))
	rateLimited := false
	for _, f := range failures {
		failedApps[f.app] = true
		rateLimited = rateLimited || isRateLimited(f.err)
	}
	fmt.Fprintf(os.Stderr, "\n✗ %d of %d apps failed and are left as they were in %s:\n", len(failedApps), total, githubAppsPath)
	for _, f := range failures {
		fmt.Fprintf(os.Stderr, "  %s (%s): %v\n", f.app, f.stage, f.err)
	}
	if rateLimited {
		fmt.Fprintf(os.Stderr, "Hint: set GITHUB_TOKEN to raise the GitHub API rate limit, then run the command again.\n")
	}
	return fmt.Errorf("%d of %d apps failed", len(failedApps), total)
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

	tuples := make([]platformTuple, 0, len(nonLinuxOSes)*len(baseArches)+len(baseArches)*len(linuxLibcs))

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

func buildBinariesForApp(ctx context.Context, appName string, release *github.Release, configHash string, state *appstate.State) (*appstate.BinariesEntry, error) {
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

	// When extraction is verified, wrap the binmanager verifier so
	// pickBinaryForPlatform can retry the next-ranked candidate on failure —
	// e.g. fall back to a raw binary when a preferred archive's guessed
	// in-archive path is wrong. Left nil (no verification) when the flag is off.
	var verify extractionVerifier
	if verifyExtractionFlag {
		verify = memoizedVerifier(func(ctx context.Context, url, hash string, contentType binmanager.BinContentType, binaryPath *string) error {
			return binmanager.VerifyBinaryExtraction(ctx, url, hash, binmanager.BinHashTypeSHA256, contentType, binaryPath)
		})
	}

	var results []detectionResult
	successCount := 0
	notAvailableCount := 0
	noHashCount := 0
	verificationFailedCount := 0
	deduplicatedCount := 0

	for _, platform := range platforms {
		candidates, err := detector.DetectBinaryCandidates(appName, release.Assets, platform.os, platform.arch, platform.libc)
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

		// Walk the ranked candidates and take the first that satisfies libc,
		// hash, and (when enabled) extraction verification. A raw binary can thus
		// rescue a platform whose higher-ranked archive fails to extract.
		pick, status, pickErr := pickBinaryForPlatform(ctx, appName, candidates, platform, historicalBinaries, verify)
		switch status {
		case "success":
			// fall through to dedup + store below
		case "no_hash":
			results = append(results, detectionResult{
				os: platform.os, arch: platform.arch, libc: platform.libc,
				status: "no_hash", assetName: candidates[0].Name, err: pickErr,
			})
			noHashCount++
			continue
		case "verification_failed":
			result := detectionResult{
				os: platform.os, arch: platform.arch, libc: platform.libc,
				status: "verification_failed", assetName: candidates[0].Name, err: pickErr,
			}
			if failure, ok := errors.AsType[*candidateError](pickErr); ok {
				result.assetName, result.err = failure.asset, failure.err
			}
			results = append(results, result)
			verificationFailedCount++
			continue
		default: // "not_available" — e.g. every candidate was a libc mismatch
			results = append(results, detectionResult{
				os: platform.os, arch: platform.arch, libc: platform.libc,
				status: "not_available", err: pickErr,
			})
			notAvailableCount++
			continue
		}

		// Deduplicate: if this is a musl tuple and the glibc entry for the same
		// OS/arch has the same URL+hash, skip to avoid duplicate entries.
		if platform.libc == "musl" {
			if osMap, ok := seenByOsArch[platform.os]; ok {
				if seen, ok := osMap[platform.arch]; ok {
					if seen.url == pick.asset.BrowserDownloadURL && seen.hash == pick.hash {
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

		// Create binary info
		binInfo := binmanager.BinaryOsArchInfo{
			URL:         pick.asset.BrowserDownloadURL,
			Hash:        pick.hash,
			ContentType: pick.contentType,
			BinaryPath:  pick.binaryPath,
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
				url:  pick.asset.BrowserDownloadURL,
				hash: pick.hash,
			}
		}

		results = append(results, detectionResult{
			os:          platform.os,
			arch:        platform.arch,
			libc:        platform.libc,
			status:      "success",
			assetName:   pick.asset.Name,
			contentType: pick.contentType,
			binaryPath:  pick.binaryPath,
		})
		successCount++
	}

	printDetectionResults(results, verifyExtractionFlag)

	// A platform whose asset could not be verified fails the app: recording
	// the others would drop that platform from the entry without a word.
	if verificationFailedCount > 0 {
		fmt.Printf("\nSummary: %d detected, %d not available, %d deduplicated, %d verification failed\n",
			successCount, notAvailableCount, deduplicatedCount, verificationFailedCount)
		unverified := &verificationError{}
		for _, r := range results {
			if r.status == "verification_failed" {
				unverified.platforms = append(unverified.platforms, platformVerification{
					platform: formatPlatformLabel(r),
					err:      fmt.Errorf("%s: %w", r.assetName, r.err),
				})
			}
		}
		return nil, unverified
	}

	switch {
	case noHashCount > 0 && successCount == 0:
		// The assets were found; what they lack is the digest the registry needs.
		return nil, fmt.Errorf("assets were detected for %d platform(s) but none carries a SHA-256 digest (mandatory per security policy)", noHashCount)
	case noHashCount > 0:
		return nil, fmt.Errorf("%d platform(s) missing SHA-256 hash (mandatory per security policy)", noHashCount)
	case successCount == 0:
		return nil, errors.New("no binaries were detected")
	}

	entry.ConfigHash = configHash

	switch {
	case deduplicatedCount > 0:
		fmt.Printf("\nSummary: %d detected, %d not available, %d deduplicated\n",
			successCount, notAvailableCount, deduplicatedCount)
	default:
		fmt.Printf("\nSummary: %d detected, %d not available\n", successCount, notAvailableCount)
	}
	return entry, nil
}

// extractionVerifier downloads an asset and confirms its hash and (for
// archives) that binaryPath extracts to a non-empty file. It is injected so the
// candidate-selection loop is unit-testable without network access; nil means
// verification is disabled.
type extractionVerifier func(ctx context.Context, url, hash string, contentType binmanager.BinContentType, binaryPath *string) error

// memoizedVerifier verifies each asset once per run. The glibc and musl tuples
// of one architecture usually pick the same archive, and the platforms of one
// asset must agree: a second download of an asset that already verified could
// fail on the network and take the app down with it.
func memoizedVerifier(verify extractionVerifier) extractionVerifier {
	results := map[string]error{}
	return func(ctx context.Context, url, hash string, contentType binmanager.BinContentType, binaryPath *string) error {
		key := url + "\x00" + hash + "\x00" + string(contentType)
		if binaryPath != nil {
			key += "\x00" + *binaryPath
		}
		if err, seen := results[key]; seen {
			return err
		}
		err := verify(ctx, url, hash, contentType, binaryPath)
		results[key] = err
		return err
	}
}

// candidateError is the verification error of one candidate, named after the
// asset it belongs to.
type candidateError struct {
	asset string
	err   error
}

func (e *candidateError) Error() string { return e.asset + ": " + e.err.Error() }

// Unwrap exposes the wrapped error for errors.Is/As chains.
func (e *candidateError) Unwrap() error { return e.err }

// candidatePick is the asset chosen for one platform plus the derived metadata
// needed to record it.
type candidatePick struct {
	asset       github.Asset
	contentType binmanager.BinContentType
	binaryPath  *string
	hash        string
}

// pickBinaryForPlatform walks the ranked candidate assets for one platform and
// returns the first that satisfies libc, hash, and (when verify != nil)
// extraction requirements. This lets a lower-ranked raw binary rescue a
// platform whose preferred archive fails extraction verification instead of the
// platform being dropped.
//
// On failure it returns a nil pick and a status describing the most relevant
// reason across all candidates — "verification_failed" (some candidate
// downloaded but did not extract), "no_hash" (a matching candidate lacked a
// SHA-256 digest), or "not_available" (only libc mismatches / no usable
// candidate) — together with the corresponding error for reporting.
func pickBinaryForPlatform(
	ctx context.Context,
	appName string,
	candidates []github.Asset,
	platform platformTuple,
	historical binmanager.MapOfBinaries,
	verify extractionVerifier,
) (*candidatePick, string, error) {
	var verifyErr, hashErr, libcErr error

	for i := range candidates {
		asset := candidates[i]

		// Reject libc mismatches: the detected libc of the asset must not
		// conflict with the requested libc (e.g. a musl-only asset under glibc).
		if platform.libc != "unknown" {
			if detectedLibc := detector.DetectLibcFromFilename(asset.Name); detectedLibc != "" && detectedLibc != platform.libc {
				libcErr = fmt.Errorf("asset %q is %s, not %s", asset.Name, detectedLibc, platform.libc)
				continue
			}
		}

		contentType := detector.DetectContentType(asset.Name)
		binaryPath := detector.DetectBinaryPathWithHistory(appName, asset.Name, contentType, platform.os, platform.arch, platform.libc, historical)

		hash, err := extractHashFromDigest(asset.Digest)
		if err != nil {
			hashErr = err
			continue
		}

		if verify != nil {
			if err := verify(ctx, asset.BrowserDownloadURL, hash, contentType, binaryPath); err != nil {
				verifyErr = &candidateError{asset: asset.Name, err: err}
				if binmanager.IsDownloadError(err) {
					// The asset was never seen, so the candidate is not what failed;
					// trying the next one would hand the platform to whatever ranks
					// below it — an attestation file, or a variant that happens to verify.
					return nil, "verification_failed", verifyErr
				}
				continue
			}
		}

		return &candidatePick{asset: asset, contentType: contentType, binaryPath: binaryPath, hash: hash}, "success", nil
	}

	// Precedence favours the most informative failure: a download/extract
	// failure over a missing hash over a plain libc mismatch.
	switch {
	case verifyErr != nil:
		return nil, "verification_failed", verifyErr
	case hashErr != nil:
		return nil, "no_hash", hashErr
	default:
		return nil, "not_available", libcErr
	}
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
		return "", errors.New("empty digest")
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
