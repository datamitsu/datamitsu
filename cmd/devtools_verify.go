package cmd

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
	"github.com/datamitsu/datamitsu/internal/verifycache"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	verifyNoVersionCheckFlag bool
	verifyConcurrencyFlag    int
	verifyJSONFlag           bool
	verifySkipPassedFlag     bool
	verifyNoRemoteFlag       bool
)

var verifyAllCmd = &cobra.Command{
	Use:   "verify-all",
	Short: "Verify all apps and runtimes across all configured platforms",
	Long: `Downloads and hash-verifies binary apps and managed runtimes for all
configured platforms. Installs runtime-managed apps (UV/FNM/JVM) on the
current platform. Optionally runs each app's version command and compares
output against the configured version.

Results are persisted to a state file after each check completes. Use
--skip-passed to skip entries whose config fingerprint is unchanged and
whose last status was "ok". Skipped entries appear as "cached" in output.

Data source: the same aggregated config used at runtime (loadConfig()).`,
	Args: cobra.NoArgs,
	Run:  runVerifyAll,
}

func init() {
	devtoolsCmd.AddCommand(verifyAllCmd)
	verifyAllCmd.Flags().BoolVar(&verifyNoVersionCheckFlag, "no-version-check", false,
		"Skip version command execution and comparison")
	verifyAllCmd.Flags().IntVar(&verifyConcurrencyFlag, "concurrency", 0,
		"Concurrent download workers (default: DATAMITSU_CONCURRENCY env var or 3)")
	verifyAllCmd.Flags().BoolVar(&verifyJSONFlag, "json", false,
		"Output machine-readable JSON (progress suppressed, only final JSON to stdout)")
	verifyAllCmd.Flags().BoolVar(&verifySkipPassedFlag, "skip-passed", false,
		"Skip checks whose config is unchanged and passed last run (may use stale data)")
	verifyAllCmd.Flags().BoolVar(&verifyNoRemoteFlag, "no-remote", false,
		"Skip loading remote configs declared via getRemoteConfigs()")
}

// Color helpers
var (
	clrGreen    = color.New(color.FgGreen).SprintFunc()
	clrRed      = color.New(color.FgRed, color.Bold).SprintFunc()
	clrBold     = color.New(color.Bold).SprintFunc()
	clrFaint    = color.New(color.Faint).SprintFunc()
	clrRedPlain = color.New(color.FgRed).SprintFunc()
)

// --- Result types ---

type binaryVerifyResult struct {
	AppName  string
	Version  string
	Os       syslist.OsType
	Arch     syslist.ArchType
	Libc     string
	Status   string // "ok", "failed"
	ErrorMsg string
}

type runtimeVerifyResult struct {
	RuntimeName string
	Os          syslist.OsType
	Arch        syslist.ArchType
	Libc        string
	Status      string // "ok", "failed"
	ErrorMsg    string
}

type runtimeAppResult struct {
	AppName  string
	Kind     string // "uv", "fnm", "jvm"
	Version  string
	Status   string // "ok", "failed"
	ErrorMsg string
}

type bundleVerifyResult struct {
	BundleName string
	Version    string
	Status     string // "ok", "failed"
	ErrorMsg   string
}

type versionCheckResult struct {
	AppName  string
	Args     []string
	Expected string
	Actual   string
	Status   string // "ok", "mismatch", "skipped", "exec_failed"
	ErrorMsg string
}

// --- JSON output types ---

type jsonOutput struct {
	CurrentPlatform jsonPlatform         `json:"currentPlatform"`
	BinaryApps      []jsonBinaryApp      `json:"binaryApps"`
	ManagedRuntimes []jsonManagedRuntime `json:"managedRuntimes"`
	RuntimeApps     []jsonRuntimeApp     `json:"runtimeApps"`
	Bundles         []jsonBundle         `json:"bundles"`
	VersionChecks   []jsonVersionCheck   `json:"versionChecks"`
	Summary         jsonSummary          `json:"summary"`
	OverallStatus   string               `json:"overallStatus"`
}

type jsonPlatform struct {
	Os   string `json:"os"`
	Arch string `json:"arch"`
}

type jsonBinaryApp struct {
	Name      string             `json:"name"`
	Version   string             `json:"version"`
	Platforms []jsonPlatformResult `json:"platforms"`
}

type jsonPlatformResult struct {
	Os     string `json:"os"`
	Arch   string `json:"arch"`
	Libc   string `json:"libc,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type jsonManagedRuntime struct {
	Name      string             `json:"name"`
	Platforms []jsonPlatformResult `json:"platforms"`
}

type jsonRuntimeApp struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

type jsonBundle struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

type jsonVersionCheck struct {
	Name     string   `json:"name"`
	Args     []string `json:"args"`
	Expected string   `json:"expected,omitempty"`
	Actual   string   `json:"actual,omitempty"`
	Status   string   `json:"status"`
	Error    string   `json:"error,omitempty"`
}

type jsonSummary struct {
	BinaryDownloads  jsonCountOkFailed      `json:"binaryDownloads"`
	RuntimeDownloads jsonCountOkFailed      `json:"runtimeDownloads"`
	RuntimeInstalls  jsonCountOkFailed      `json:"runtimeInstalls"`
	BundleInstalls   jsonCountOkFailed      `json:"bundleInstalls"`
	VersionChecks    jsonCountVersionChecks `json:"versionChecks"`
}

type jsonCountOkFailed struct {
	Ok     int `json:"ok"`
	Failed int `json:"failed"`
	Cached int `json:"cached"`
}

type jsonCountVersionChecks struct {
	Ok          int `json:"ok"`
	Mismatch    int `json:"mismatch"`
	Skipped     int `json:"skipped"`
	ExecFailed  int `json:"execFailed"`
	ParseFailed int `json:"parseFailed"`
	Cached      int `json:"cached"`
}

// --- Job types ---

type binaryVerifyJob struct {
	appName string
	version string
	os      syslist.OsType
	arch    syslist.ArchType
	libc    string
	info    binmanager.BinaryOsArchInfo
}

type runtimeVerifyJob struct {
	runtimeName string
	os          syslist.OsType
	arch        syslist.ArchType
	libc        string
	info        binmanager.BinaryOsArchInfo
}

type runtimeAppEntry struct {
	name string
	app  binmanager.App
	kind string
}

type versionCheckEntry struct {
	name string
	app  binmanager.App
}

// --- Fingerprint bridge functions ---

func binaryJobKeyAndFP(j binaryVerifyJob) (string, string) {
	binaryPath := ""
	if j.info.BinaryPath != nil {
		binaryPath = *j.info.BinaryPath
	}
	hashType := string(binmanager.BinHashTypeSHA256)
	if j.info.HashType != nil {
		hashType = string(*j.info.HashType)
	}
	key := verifycache.BinaryEntryKey(j.appName, string(j.os), string(j.arch), j.libc)
	fp := verifycache.FingerprintBinary(j.info.URL, j.info.Hash, hashType, string(j.info.ContentType), binaryPath, j.info.ExtractDir, string(j.os), string(j.arch), j.libc)
	return key, fp
}

func runtimeJobKeyAndFP(j runtimeVerifyJob) (string, string) {
	binaryPath := ""
	if j.info.BinaryPath != nil {
		binaryPath = *j.info.BinaryPath
	}
	hashType := string(binmanager.BinHashTypeSHA256)
	if j.info.HashType != nil {
		hashType = string(*j.info.HashType)
	}
	key := verifycache.RuntimeEntryKey(j.runtimeName, string(j.os), string(j.arch), j.libc)
	fp := verifycache.FingerprintRuntime(j.info.URL, j.info.Hash, hashType, string(j.info.ContentType), binaryPath, j.info.ExtractDir, string(j.os), string(j.arch), j.libc)
	return key, fp
}

func runtimeAppKeyAndFP(e runtimeAppEntry, runtimes config.MapOfRuntimes, currentOs, currentArch string) (string, string) {
	key := verifycache.RuntimeAppEntryKey(e.name, currentOs, currentArch)

	var appJSON, rtJSON []byte
	var runtimeName string
	switch e.kind {
	case "uv":
		if e.app.Uv != nil {
			appJSON, _ = json.Marshal(e.app.Uv)
			runtimeName = e.app.Uv.Runtime
		}
	case "fnm":
		if e.app.Fnm != nil {
			appJSON, _ = json.Marshal(e.app.Fnm)
			runtimeName = e.app.Fnm.Runtime
		}
	case "jvm":
		if e.app.Jvm != nil {
			appJSON, _ = json.Marshal(e.app.Jvm)
			runtimeName = e.app.Jvm.Runtime
		}
	}

	if runtimeName == "" {
		runtimeName = resolveDefaultRuntimeName(runtimes, e.kind)
	}

	if rt, ok := runtimes[runtimeName]; ok {
		rtJSON, _ = json.Marshal(rt)
	}

	var filesJSON, archivesJSON []byte
	if len(e.app.Files) > 0 {
		filesJSON, _ = json.Marshal(e.app.Files)
	}
	if len(e.app.Archives) > 0 {
		archivesJSON, _ = json.Marshal(e.app.Archives)
	}

	fp := verifycache.FingerprintRuntimeApp(string(appJSON), string(rtJSON), string(filesJSON), string(archivesJSON), currentOs, currentArch)
	return key, fp
}

func resolveDefaultRuntimeName(runtimes config.MapOfRuntimes, kind string) string {
	var runtimeKind config.RuntimeKind
	switch kind {
	case "uv":
		runtimeKind = config.RuntimeKindUV
	case "fnm":
		runtimeKind = config.RuntimeKindFNM
	case "jvm":
		runtimeKind = config.RuntimeKindJVM
	default:
		return ""
	}
	names := make([]string, 0, len(runtimes))
	for name := range runtimes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if runtimes[name].Kind == runtimeKind {
			return name
		}
	}
	return ""
}

type bundleVerifyEntry struct {
	name   string
	bundle *binmanager.Bundle
}

func bundleKeyAndFP(e bundleVerifyEntry) (string, string) {
	key := verifycache.BundleEntryKey(e.name)

	var filesJSON, archivesJSON string
	if len(e.bundle.Files) > 0 {
		data, _ := json.Marshal(e.bundle.Files)
		filesJSON = string(data)
	}
	if len(e.bundle.Archives) > 0 {
		data, _ := json.Marshal(e.bundle.Archives)
		archivesJSON = string(data)
	}

	fp := verifycache.FingerprintBundle(e.bundle.Version, filesJSON, archivesJSON)
	return key, fp
}

func filterSkippedBundleEntries(entries []bundleVerifyEntry, sm *verifycache.StateManager) (toRun, skipped []bundleVerifyEntry) {
	for _, e := range entries {
		key, fp := bundleKeyAndFP(e)
		if sm.ShouldSkip(key, fp) {
			skipped = append(skipped, e)
		} else {
			toRun = append(toRun, e)
		}
	}
	return
}

func versionCheckKeyAndFP(e versionCheckEntry, currentOs, currentArch, currentLibc string) (string, string) {
	key := verifycache.VersionCheckEntryKey(e.name, currentOs, currentArch)

	version := getAppVersion(e.app)
	versionArgs := "--version"
	if e.app.VersionCheck != nil && e.app.VersionCheck.Args != nil {
		versionArgs = strings.Join(e.app.VersionCheck.Args, " ")
	}

	fp := verifycache.FingerprintVersionCheck(version, versionArgs, currentOs, currentArch, currentLibc)
	return key, fp
}

// --- Filter functions ---

func filterSkippedBinaryJobs(jobs []binaryVerifyJob, sm *verifycache.StateManager) (toRun, skipped []binaryVerifyJob) {
	for _, j := range jobs {
		key, fp := binaryJobKeyAndFP(j)
		if sm.ShouldSkip(key, fp) {
			skipped = append(skipped, j)
		} else {
			toRun = append(toRun, j)
		}
	}
	return
}

func filterSkippedRuntimeJobs(jobs []runtimeVerifyJob, sm *verifycache.StateManager) (toRun, skipped []runtimeVerifyJob) {
	for _, j := range jobs {
		key, fp := runtimeJobKeyAndFP(j)
		if sm.ShouldSkip(key, fp) {
			skipped = append(skipped, j)
		} else {
			toRun = append(toRun, j)
		}
	}
	return
}

func filterSkippedRuntimeAppEntries(entries []runtimeAppEntry, sm *verifycache.StateManager, runtimes config.MapOfRuntimes, currentOs, currentArch string) (toRun, skipped []runtimeAppEntry) {
	for _, e := range entries {
		key, fp := runtimeAppKeyAndFP(e, runtimes, currentOs, currentArch)
		if sm.ShouldSkip(key, fp) {
			skipped = append(skipped, e)
		} else {
			toRun = append(toRun, e)
		}
	}
	return
}

func filterSkippedVersionCheckEntries(entries []versionCheckEntry, sm *verifycache.StateManager, currentOs, currentArch, currentLibc string) (toRun, skipped []versionCheckEntry) {
	for _, e := range entries {
		if e.app.VersionCheck != nil && e.app.VersionCheck.Disabled {
			toRun = append(toRun, e)
			continue
		}
		key, fp := versionCheckKeyAndFP(e, currentOs, currentArch, currentLibc)
		if sm.ShouldSkip(key, fp) {
			skipped = append(skipped, e)
		} else {
			toRun = append(toRun, e)
		}
	}
	return
}

func runVerifyAll(cmd *cobra.Command, args []string) {
	SkipRemoteConfig = verifyNoRemoteFlag
	defer func() { SkipRemoteConfig = false }()
	cfg, _, _, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	concurrency := verifyConcurrencyFlag
	if concurrency <= 0 {
		concurrency = env.GetConcurrency()
	}

	currentOs := runtime.GOOS
	currentArch := runtime.GOARCH
	currentLibc := string(target.DetectHost().Libc)

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}
	statePath := verifycache.StatePath(env.GetCachePath(), cwd)

	var state *verifycache.VerifyState
	if verifySkipPassedFlag {
		loaded, loadErr := verifycache.LoadState(statePath)
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load verify state: %v\n", loadErr)
			state = &verifycache.VerifyState{
				Version: 1,
				CWD:     cwd,
				Entries: map[string]verifycache.VerifyEntry{},
			}
		} else {
			state = loaded
			state.CWD = cwd
		}
	} else {
		state = &verifycache.VerifyState{
			Version: 1,
			CWD:     cwd,
			Entries: map[string]verifycache.VerifyEntry{},
		}
	}

	sm := verifycache.NewStateManager(state, statePath)
	if !verifySkipPassedFlag {
		if resetErr := sm.Reset(); resetErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to reset verify state: %v\n", resetErr)
		}
	}

	if !verifyJSONFlag {
		fmt.Println(clrBold("datamitsu devtools verify-all"))
		if verifySkipPassedFlag {
			fmt.Println(clrFaint("⚠ Using --skip-passed: cached results may be stale. Run without this flag for accurate results."))
		}
		resolvedRemoteURLsMu.Lock()
		urls := make([]string, len(resolvedRemoteURLs))
		copy(urls, resolvedRemoteURLs)
		resolvedRemoteURLsMu.Unlock()
		for _, url := range urls {
			fmt.Printf("  remote: %s\n", url)
		}
		fmt.Println()
	}

	// Phase 1: Binary apps — all platforms
	binaryResults := runPhase1BinaryApps(cfg, concurrency, !verifyJSONFlag, sm, verifySkipPassedFlag)

	// Phase 2: Managed runtimes — all platforms
	runtimeResults := runPhase2ManagedRuntimes(cfg, concurrency, !verifyJSONFlag, sm, verifySkipPassedFlag)

	// Phase 3: Runtime-managed apps — current platform
	runtimeAppResults := runPhase3RuntimeApps(cfg, currentOs, currentArch, !verifyJSONFlag, sm, verifySkipPassedFlag)

	// Phase 4: Bundle installs — platform-independent
	bundleResults := runPhaseBundleInstalls(cfg, !verifyJSONFlag, sm, verifySkipPassedFlag)

	// Phase 5: Version checks — current platform
	var versionResults []versionCheckResult
	if !verifyNoVersionCheckFlag {
		rm := runtimemanager.New(cfg.Runtimes)
		bm := binmanager.New(cfg.Apps, cfg.Bundles, rm)
		versionResults = runPhase5VersionChecks(cfg, bm, currentOs, currentArch, currentLibc, !verifyJSONFlag, sm, verifySkipPassedFlag)
	}

	// Compute summary
	summary := computeSummary(binaryResults, runtimeResults, runtimeAppResults, bundleResults, versionResults)
	overallStatus := "ok"
	if summary.BinaryDownloads.Failed > 0 || summary.RuntimeDownloads.Failed > 0 ||
		summary.RuntimeInstalls.Failed > 0 || summary.BundleInstalls.Failed > 0 ||
		summary.VersionChecks.Mismatch > 0 ||
		summary.VersionChecks.ExecFailed > 0 || summary.VersionChecks.ParseFailed > 0 {
		overallStatus = "failed"
	}

	if verifyJSONFlag {
		output := buildJSONOutput(currentOs, currentArch, binaryResults, runtimeResults, runtimeAppResults, bundleResults, versionResults, summary, overallStatus)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(output); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON output: %v\n", err)
			os.Exit(1)
		}
	} else {
		printSummary(summary)
	}

	if overallStatus == "failed" {
		os.Exit(1)
	}
}

// --- Phase 1: Binary apps ---

func runPhase1BinaryApps(cfg *config.Config, concurrency int, showProgress bool, sm *verifycache.StateManager, skipPassed bool) []binaryVerifyResult {
	var allJobs []binaryVerifyJob
	for name, app := range cfg.Apps {
		if app.Binary == nil {
			continue
		}
		for osType, archMap := range app.Binary.Binaries {
			for archType, libcMap := range archMap {
				for libc, info := range libcMap {
					allJobs = append(allJobs, binaryVerifyJob{
						appName: name,
						version: app.Binary.Version,
						os:      osType,
						arch:    archType,
						libc:    libc,
						info:    info,
					})
				}
			}
		}
	}

	sort.Slice(allJobs, func(i, j int) bool {
		if allJobs[i].appName != allJobs[j].appName {
			return allJobs[i].appName < allJobs[j].appName
		}
		if allJobs[i].os != allJobs[j].os {
			return allJobs[i].os < allJobs[j].os
		}
		if allJobs[i].arch != allJobs[j].arch {
			return allJobs[i].arch < allJobs[j].arch
		}
		return allJobs[i].libc < allJobs[j].libc
	})

	if showProgress {
		fmt.Println(clrBold("Binary app downloads — all platforms"))
	}

	var results []binaryVerifyResult
	jobsToRun := allJobs

	if skipPassed {
		var skippedJobs []binaryVerifyJob
		jobsToRun, skippedJobs = filterSkippedBinaryJobs(allJobs, sm)
		for _, j := range skippedJobs {
			r := binaryVerifyResult{
				AppName: j.appName,
				Version: j.version,
				Os:      j.os,
				Arch:    j.arch,
				Libc:    j.libc,
				Status:  "cached",
			}
			results = append(results, r)
			if showProgress {
				printBinaryResult(r)
			}
		}
	}

	workerResults := make([]binaryVerifyResult, len(jobsToRun))
	resultCh := make(chan struct {
		idx    int
		result binaryVerifyResult
	}, len(jobsToRun))

	jobCh := make(chan struct {
		idx int
		job binaryVerifyJob
	}, len(jobsToRun))

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobCh {
				j := item.job
				err := verifyBinaryOrDir(j.info)
				r := binaryVerifyResult{
					AppName: j.appName,
					Version: j.version,
					Os:      j.os,
					Arch:    j.arch,
					Libc:    j.libc,
					Status:  "ok",
				}
				if err != nil {
					r.Status = "failed"
					r.ErrorMsg = err.Error()
				}
				resultCh <- struct {
					idx    int
					result binaryVerifyResult
				}{idx: item.idx, result: r}
			}
		}()
	}

	for i, j := range jobsToRun {
		jobCh <- struct {
			idx int
			job binaryVerifyJob
		}{idx: i, job: j}
	}
	close(jobCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	for item := range resultCh {
		workerResults[item.idx] = item.result
		r := item.result
		j := jobsToRun[item.idx]
		key, fp := binaryJobKeyAndFP(j)
		if err := sm.Record(key, fp, r.Status, r.ErrorMsg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record verify state for %s: %v\n", key, err)
		}
		if showProgress {
			printBinaryResult(r)
		}
	}

	results = append(results, workerResults...)

	if showProgress {
		fmt.Println()
	}

	return results
}

func verifyBinaryOrDir(info binmanager.BinaryOsArchInfo) error {
	hashType := binmanager.BinHashTypeSHA256
	if info.HashType != nil {
		hashType = *info.HashType
	}

	if info.ExtractDir {
		return verifyExtractDir(info.URL, info.Hash, hashType, info.ContentType)
	}

	return binmanager.VerifyBinaryExtraction(info.URL, info.Hash, hashType, info.ContentType, info.BinaryPath)
}

func verifyExtractDir(url, hash string, hashType binmanager.BinHashType, contentType binmanager.BinContentType) error {
	tempDir, err := os.MkdirTemp("", "datamitsu-verify-dir-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	downloadedPath, err := binmanager.DownloadFileForVerify(url, tempDir)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if hash == "" {
		return fmt.Errorf("hash is empty: verification requires a non-empty hash")
	}
	if err := binmanager.VerifyFileHashPublic(downloadedPath, hash, hashType); err != nil {
		return fmt.Errorf("hash verification failed: %w", err)
	}

	extractedDir, err := binmanager.ExtractDirForVerify(downloadedPath, contentType, tempDir)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	entries, err := os.ReadDir(extractedDir)
	if err != nil {
		return fmt.Errorf("failed to read extracted directory: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("extracted directory is empty")
	}

	return nil
}

func printBinaryResult(r binaryVerifyResult) {
	platform := fmt.Sprintf("%s/%s/%s", r.Os, r.Arch, r.Libc)
	switch r.Status {
	case "ok":
		fmt.Printf("  %s  %-20s %-16s %-10s %s\n",
			clrGreen("✓"), clrBold(r.AppName), platform, r.Version, clrGreen("OK"))
	case "cached":
		fmt.Printf("  %s  %s  %s  %s\n",
			clrFaint("≡"), clrFaint(r.AppName), clrFaint(platform), clrFaint("CACHED"))
	default:
		fmt.Printf("  %s  %-20s %-16s %-10s %s\n",
			clrRed("✗"), clrBold(r.AppName), platform, r.Version, clrRed("FAILED"))
		fmt.Printf("     %s\n", clrRedPlain(r.ErrorMsg))
	}
}

// --- Phase 2: Managed runtimes ---

func runPhase2ManagedRuntimes(cfg *config.Config, concurrency int, showProgress bool, sm *verifycache.StateManager, skipPassed bool) []runtimeVerifyResult {
	var allJobs []runtimeVerifyJob
	for name, rt := range cfg.Runtimes {
		if rt.Mode != config.RuntimeModeManaged || rt.Managed == nil {
			continue
		}
		for osType, archMap := range rt.Managed.Binaries {
			for archType, libcMap := range archMap {
				for libc, info := range libcMap {
					allJobs = append(allJobs, runtimeVerifyJob{
						runtimeName: name,
						os:          osType,
						arch:        archType,
						libc:        libc,
						info:        info,
					})
				}
			}
		}
	}

	sort.Slice(allJobs, func(i, j int) bool {
		if allJobs[i].runtimeName != allJobs[j].runtimeName {
			return allJobs[i].runtimeName < allJobs[j].runtimeName
		}
		if allJobs[i].os != allJobs[j].os {
			return allJobs[i].os < allJobs[j].os
		}
		if allJobs[i].arch != allJobs[j].arch {
			return allJobs[i].arch < allJobs[j].arch
		}
		return allJobs[i].libc < allJobs[j].libc
	})

	if showProgress {
		fmt.Println(clrBold("Managed runtime downloads — all platforms"))
	}

	var results []runtimeVerifyResult
	jobsToRun := allJobs

	if skipPassed {
		var skippedJobs []runtimeVerifyJob
		jobsToRun, skippedJobs = filterSkippedRuntimeJobs(allJobs, sm)
		for _, j := range skippedJobs {
			r := runtimeVerifyResult{
				RuntimeName: j.runtimeName,
				Os:          j.os,
				Arch:        j.arch,
				Libc:        j.libc,
				Status:      "cached",
			}
			results = append(results, r)
			if showProgress {
				printRuntimeResult(r)
			}
		}
	}

	workerResults := make([]runtimeVerifyResult, len(jobsToRun))
	resultCh := make(chan struct {
		idx    int
		result runtimeVerifyResult
	}, len(jobsToRun))

	jobCh := make(chan struct {
		idx int
		job runtimeVerifyJob
	}, len(jobsToRun))

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobCh {
				j := item.job
				err := verifyBinaryOrDir(j.info)
				r := runtimeVerifyResult{
					RuntimeName: j.runtimeName,
					Os:          j.os,
					Arch:        j.arch,
					Libc:        j.libc,
					Status:      "ok",
				}
				if err != nil {
					r.Status = "failed"
					r.ErrorMsg = err.Error()
				}
				resultCh <- struct {
					idx    int
					result runtimeVerifyResult
				}{idx: item.idx, result: r}
			}
		}()
	}

	for i, j := range jobsToRun {
		jobCh <- struct {
			idx int
			job runtimeVerifyJob
		}{idx: i, job: j}
	}
	close(jobCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	for item := range resultCh {
		workerResults[item.idx] = item.result
		r := item.result
		j := jobsToRun[item.idx]
		key, fp := runtimeJobKeyAndFP(j)
		if err := sm.Record(key, fp, r.Status, r.ErrorMsg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record verify state for %s: %v\n", key, err)
		}
		if showProgress {
			printRuntimeResult(r)
		}
	}

	results = append(results, workerResults...)

	if showProgress {
		fmt.Println()
	}

	return results
}

func printRuntimeResult(r runtimeVerifyResult) {
	platform := fmt.Sprintf("%s/%s/%s", r.Os, r.Arch, r.Libc)
	switch r.Status {
	case "ok":
		fmt.Printf("  %s  %-20s %-16s %s\n",
			clrGreen("✓"), clrBold(r.RuntimeName), platform, clrGreen("OK"))
	case "cached":
		fmt.Printf("  %s  %s  %s  %s\n",
			clrFaint("≡"), clrFaint(r.RuntimeName), clrFaint(platform), clrFaint("CACHED"))
	default:
		fmt.Printf("  %s  %-20s %-16s %s\n",
			clrRed("✗"), clrBold(r.RuntimeName), platform, clrRed("FAILED"))
		fmt.Printf("     %s\n", clrRedPlain(r.ErrorMsg))
	}
}

// --- Phase 3: Runtime-managed apps ---

func runPhase3RuntimeApps(cfg *config.Config, currentOs, currentArch string, showProgress bool, sm *verifycache.StateManager, skipPassed bool) []runtimeAppResult {
	if showProgress {
		fmt.Printf("%s (%s/%s)\n", clrBold("Runtime app installs — current platform"), currentOs, currentArch)
	}

	rm := runtimemanager.New(cfg.Runtimes)
	bm := binmanager.New(cfg.Apps, cfg.Bundles, rm)

	var entries []runtimeAppEntry
	for name, app := range cfg.Apps {
		if app.Uv != nil {
			entries = append(entries, runtimeAppEntry{name: name, app: app, kind: "uv"})
		} else if app.Fnm != nil {
			entries = append(entries, runtimeAppEntry{name: name, app: app, kind: "fnm"})
		} else if app.Jvm != nil {
			entries = append(entries, runtimeAppEntry{name: name, app: app, kind: "jvm"})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})

	var results []runtimeAppResult
	entriesToRun := entries

	if skipPassed {
		var skippedEntries []runtimeAppEntry
		entriesToRun, skippedEntries = filterSkippedRuntimeAppEntries(entries, sm, cfg.Runtimes, currentOs, currentArch)
		for _, entry := range skippedEntries {
			version := ""
			switch entry.kind {
			case "uv":
				version = entry.app.Uv.Version
			case "fnm":
				version = entry.app.Fnm.Version
			case "jvm":
				version = entry.app.Jvm.Version
			}
			r := runtimeAppResult{
				AppName: entry.name,
				Kind:    entry.kind,
				Version: version,
				Status:  "cached",
			}
			results = append(results, r)
			if showProgress {
				printRuntimeAppResult(r)
			}
		}
	}

	for _, entry := range entriesToRun {
		version := ""
		switch entry.kind {
		case "uv":
			version = entry.app.Uv.Version
		case "fnm":
			version = entry.app.Fnm.Version
		case "jvm":
			version = entry.app.Jvm.Version
		}

		key, fp := runtimeAppKeyAndFP(entry, cfg.Runtimes, currentOs, currentArch)

		_, err := bm.GetCommandInfo(entry.name)
		r := runtimeAppResult{
			AppName: entry.name,
			Kind:    entry.kind,
			Version: version,
			Status:  "ok",
		}
		if err != nil {
			r.Status = "failed"
			r.ErrorMsg = err.Error()
		}
		results = append(results, r)
		if err := sm.Record(key, fp, r.Status, r.ErrorMsg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record verify state for %s: %v\n", key, err)
		}

		if showProgress {
			printRuntimeAppResult(r)
		}
	}

	if showProgress {
		fmt.Println()
	}

	return results
}

func printRuntimeAppResult(r runtimeAppResult) {
	switch r.Status {
	case "ok":
		fmt.Printf("  %s  %-24s %-6s %-10s %s\n",
			clrGreen("✓"), clrBold(r.AppName), r.Kind, r.Version, clrGreen("installed"))
	case "cached":
		fmt.Printf("  %s  %s  %s  %s  %s\n",
			clrFaint("≡"), clrFaint(r.AppName), clrFaint(r.Kind), clrFaint(r.Version), clrFaint("CACHED"))
	default:
		fmt.Printf("  %s  %-24s %-6s %-10s %s\n",
			clrRed("✗"), clrBold(r.AppName), r.Kind, r.Version, clrRed("FAILED: "+r.ErrorMsg))
	}
}

// --- Phase 4: Bundle installs ---

func runPhaseBundleInstalls(cfg *config.Config, showProgress bool, sm *verifycache.StateManager, skipPassed bool) []bundleVerifyResult {
	if showProgress {
		fmt.Println(clrBold("Bundle installs — platform-independent"))
	}

	rm := runtimemanager.New(cfg.Runtimes)
	bm := binmanager.New(cfg.Apps, cfg.Bundles, rm)

	var entries []bundleVerifyEntry
	for name, bundle := range cfg.Bundles {
		if bundle == nil {
			continue
		}
		entries = append(entries, bundleVerifyEntry{name: name, bundle: bundle})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})

	var results []bundleVerifyResult
	entriesToRun := entries

	if skipPassed {
		var skippedEntries []bundleVerifyEntry
		entriesToRun, skippedEntries = filterSkippedBundleEntries(entries, sm)
		for _, e := range skippedEntries {
			r := bundleVerifyResult{
				BundleName: e.name,
				Version:    e.bundle.Version,
				Status:     "cached",
			}
			results = append(results, r)
			if showProgress {
				printBundleResult(r)
			}
		}
	}

	ctx := context.Background()
	for _, entry := range entriesToRun {
		key, fp := bundleKeyAndFP(entry)

		if err := bm.InstallBundleByName(ctx, entry.name); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: bundle install failed for %s: %v\n", entry.name, err)
		}

		_, err := bm.GetBundleRoot(entry.name)

		r := bundleVerifyResult{
			BundleName: entry.name,
			Version:    entry.bundle.Version,
			Status:     "ok",
		}
		if err != nil {
			r.Status = "failed"
			r.ErrorMsg = err.Error()
		}
		results = append(results, r)
		if err := sm.Record(key, fp, r.Status, r.ErrorMsg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record verify state for %s: %v\n", key, err)
		}
		if showProgress {
			printBundleResult(r)
		}
	}

	if showProgress {
		fmt.Println()
	}

	return results
}

func printBundleResult(r bundleVerifyResult) {
	switch r.Status {
	case "ok":
		fmt.Printf("  %s  %-24s %-10s %s\n",
			clrGreen("✓"), clrBold(r.BundleName), r.Version, clrGreen("installed"))
	case "cached":
		fmt.Printf("  %s  %s  %s  %s\n",
			clrFaint("≡"), clrFaint(r.BundleName), clrFaint(r.Version), clrFaint("CACHED"))
	default:
		fmt.Printf("  %s  %-24s %-10s %s\n",
			clrRed("✗"), clrBold(r.BundleName), r.Version, clrRed("FAILED: "+r.ErrorMsg))
	}
}

// --- Phase 5: Version checks ---

var (
	versionRegex          = regexp.MustCompile(`(\d+(?:\.\d+)+)`)
	normalizeVersionRegex = regexp.MustCompile(`-(?:rc|alpha|beta)\d*$`)
)

func runPhase5VersionChecks(cfg *config.Config, bm *binmanager.BinManager, currentOs, currentArch, currentLibc string, showProgress bool, sm *verifycache.StateManager, skipPassed bool) []versionCheckResult {
	if showProgress {
		fmt.Printf("%s (%s/%s)\n", clrBold("Version checks — current platform"), currentOs, currentArch)
	}

	osType := syslist.OsType(currentOs)
	archType := syslist.ArchType(currentArch)

	var entries []versionCheckEntry
	for name, app := range cfg.Apps {
		if app.Shell != nil {
			continue
		}
		if app.Binary != nil {
			archMap, osOk := app.Binary.Binaries[osType]
			if !osOk {
				continue
			}
			libcMap, archOk := archMap[archType]
			if !archOk {
				continue
			}
			hasLibc := false
			for range libcMap {
				hasLibc = true
				break
			}
			if !hasLibc {
				continue
			}
		}
		entries = append(entries, versionCheckEntry{name: name, app: app})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})

	var results []versionCheckResult
	entriesToRun := entries

	if skipPassed {
		var skippedEntries []versionCheckEntry
		entriesToRun, skippedEntries = filterSkippedVersionCheckEntries(entries, sm, currentOs, currentArch, currentLibc)
		for _, entry := range skippedEntries {
			versionArgs := []string{"--version"}
			if entry.app.VersionCheck != nil && entry.app.VersionCheck.Args != nil {
				versionArgs = entry.app.VersionCheck.Args
			}
			r := versionCheckResult{
				AppName: entry.name,
				Args:    versionArgs,
				Status:  "cached",
			}
			results = append(results, r)
			if showProgress {
				printVersionCheckResult(r)
			}
		}
	}

	for _, entry := range entriesToRun {
		versionArgs := []string{"--version"}
		if entry.app.VersionCheck != nil {
			if entry.app.VersionCheck.Disabled {
				r := versionCheckResult{
					AppName: entry.name,
					Args:    versionArgs,
					Status:  "skipped",
				}
				if entry.app.VersionCheck.Args != nil {
					r.Args = entry.app.VersionCheck.Args
				}
				results = append(results, r)
				key, fp := versionCheckKeyAndFP(entry, currentOs, currentArch, currentLibc)
				if err := sm.Record(key, fp, r.Status, r.ErrorMsg); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to record verify state for %s: %v\n", entry.name, err)
				}
				if showProgress {
					printVersionCheckResult(r)
				}
				continue
			}
			if entry.app.VersionCheck.Args != nil {
				versionArgs = entry.app.VersionCheck.Args
			}
		}

		key, fp := versionCheckKeyAndFP(entry, currentOs, currentArch, currentLibc)

		expectedVersion := getAppVersion(entry.app)

		execCmd, err := bm.GetExecCmd(entry.name, versionArgs)
		if err != nil || execCmd == nil {
			r := versionCheckResult{
				AppName:  entry.name,
				Args:     versionArgs,
				Expected: expectedVersion,
				Status:   "exec_failed",
				ErrorMsg: "failed to get exec cmd",
			}
			if err != nil {
				r.ErrorMsg = err.Error()
			}
			results = append(results, r)
			if err := sm.Record(key, fp, r.Status, r.ErrorMsg); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to record verify state for %s: %v\n", key, err)
			}
			if showProgress {
				printVersionCheckResult(r)
			}
			continue
		}

		output, err := execCmd.CombinedOutput()
		if err != nil {
			r := versionCheckResult{
				AppName:  entry.name,
				Args:     versionArgs,
				Expected: expectedVersion,
				Actual:   extractVersion(string(output)),
				Status:   "exec_failed",
				ErrorMsg: err.Error(),
			}
			results = append(results, r)
			if err := sm.Record(key, fp, r.Status, r.ErrorMsg); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to record verify state for %s: %v\n", key, err)
			}
			if showProgress {
				printVersionCheckResult(r)
			}
			continue
		}

		actual := extractVersion(string(output))
		normalizedExpected := normalizeVersion(expectedVersion)
		normalizedActual := normalizeVersion(actual)

		status := "ok"
		if normalizedExpected != "" && normalizedActual == "" {
			status = "parse_failed"
		} else if normalizedExpected != "" && normalizedActual != "" && normalizedExpected != normalizedActual {
			status = "mismatch"
		}

		r := versionCheckResult{
			AppName:  entry.name,
			Args:     versionArgs,
			Expected: expectedVersion,
			Actual:   actual,
			Status:   status,
		}
		results = append(results, r)
		if err := sm.Record(key, fp, r.Status, r.ErrorMsg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record verify state for %s: %v\n", key, err)
		}
		if showProgress {
			printVersionCheckResult(r)
		}
	}

	if showProgress {
		fmt.Println()
	}

	return results
}

func printVersionCheckResult(r versionCheckResult) {
	argsStr := strings.Join(r.Args, " ")
	switch r.Status {
	case "ok":
		fmt.Printf("  %s  %-20s %-12s %-10s %s %-10s %s\n",
			clrGreen("✓"), clrBold(r.AppName), argsStr, r.Expected, "→", r.Actual, clrGreen("OK"))
	case "mismatch":
		fmt.Printf("  %s  %-20s %-12s %-10s %s %-10s %s\n",
			clrRed("✗"), clrBold(r.AppName), argsStr, r.Expected, "→", r.Actual, clrRed("MISMATCH"))
	case "skipped":
		fmt.Printf("  %s  %-20s %-12s %s\n",
			clrFaint("-"), clrBold(r.AppName), argsStr, clrFaint("SKIPPED"))
	case "cached":
		fmt.Printf("  %s  %s  %s  %s\n",
			clrFaint("≡"), clrFaint(r.AppName), clrFaint(argsStr), clrFaint("CACHED"))
	case "parse_failed":
		fmt.Printf("  %s  %-20s %-12s %-10s %s %-10s %s\n",
			clrRed("✗"), clrBold(r.AppName), argsStr, r.Expected, "→", r.Actual, clrRed("PARSE_FAILED"))
	case "exec_failed":
		fmt.Printf("  %s  %-20s %-12s %s\n",
			clrRed("✗"), clrBold(r.AppName), argsStr, clrRed("EXEC_FAILED"))
		if r.ErrorMsg != "" {
			fmt.Printf("     %s\n", clrRedPlain(r.ErrorMsg))
		}
	}
}

// --- Version helpers ---

func extractVersion(output string) string {
	match := versionRegex.FindString(output)
	return match
}

func normalizeVersion(version string) string {
	v := strings.TrimPrefix(version, "v")
	v = normalizeVersionRegex.ReplaceAllString(v, "")
	return v
}

func getAppVersion(app binmanager.App) string {
	if app.Binary != nil {
		return app.Binary.Version
	}
	if app.Uv != nil {
		return app.Uv.Version
	}
	if app.Fnm != nil {
		return app.Fnm.Version
	}
	if app.Jvm != nil {
		return app.Jvm.Version
	}
	return ""
}

// --- Summary ---

func computeSummary(
	binaryResults []binaryVerifyResult,
	runtimeResults []runtimeVerifyResult,
	runtimeAppResults []runtimeAppResult,
	bundleResults []bundleVerifyResult,
	versionResults []versionCheckResult,
) jsonSummary {
	s := jsonSummary{}

	for _, r := range binaryResults {
		switch r.Status {
		case "ok":
			s.BinaryDownloads.Ok++
		case "cached":
			s.BinaryDownloads.Cached++
		default:
			s.BinaryDownloads.Failed++
		}
	}

	for _, r := range runtimeResults {
		switch r.Status {
		case "ok":
			s.RuntimeDownloads.Ok++
		case "cached":
			s.RuntimeDownloads.Cached++
		default:
			s.RuntimeDownloads.Failed++
		}
	}

	for _, r := range runtimeAppResults {
		switch r.Status {
		case "ok":
			s.RuntimeInstalls.Ok++
		case "cached":
			s.RuntimeInstalls.Cached++
		default:
			s.RuntimeInstalls.Failed++
		}
	}

	for _, r := range bundleResults {
		switch r.Status {
		case "ok":
			s.BundleInstalls.Ok++
		case "cached":
			s.BundleInstalls.Cached++
		default:
			s.BundleInstalls.Failed++
		}
	}

	for _, r := range versionResults {
		switch r.Status {
		case "ok":
			s.VersionChecks.Ok++
		case "mismatch":
			s.VersionChecks.Mismatch++
		case "skipped":
			s.VersionChecks.Skipped++
		case "exec_failed":
			s.VersionChecks.ExecFailed++
		case "parse_failed":
			s.VersionChecks.ParseFailed++
		case "cached":
			s.VersionChecks.Cached++
		}
	}

	return s
}

func formatCachedSuffix(cached int) string {
	if cached > 0 {
		return fmt.Sprintf("  (%d cached)", cached)
	}
	return ""
}

func printSummary(s jsonSummary) {
	sep := clrFaint(strings.Repeat("─", 49))
	fmt.Println(sep)
	fmt.Println(clrBold("Summary"))
	fmt.Printf("  Binary downloads    %3d ok   %d failed%s\n", s.BinaryDownloads.Ok, s.BinaryDownloads.Failed, formatCachedSuffix(s.BinaryDownloads.Cached))
	fmt.Printf("  Runtime downloads   %3d ok   %d failed%s\n", s.RuntimeDownloads.Ok, s.RuntimeDownloads.Failed, formatCachedSuffix(s.RuntimeDownloads.Cached))
	fmt.Printf("  Runtime installs    %3d ok   %d failed%s\n", s.RuntimeInstalls.Ok, s.RuntimeInstalls.Failed, formatCachedSuffix(s.RuntimeInstalls.Cached))
	fmt.Printf("  Bundle installs     %3d ok   %d failed%s\n", s.BundleInstalls.Ok, s.BundleInstalls.Failed, formatCachedSuffix(s.BundleInstalls.Cached))
	fmt.Printf("  Version checks      %3d ok   %d mismatch   %d exec_failed   %d parse_failed   %d skipped%s\n",
		s.VersionChecks.Ok, s.VersionChecks.Mismatch, s.VersionChecks.ExecFailed, s.VersionChecks.ParseFailed, s.VersionChecks.Skipped, formatCachedSuffix(s.VersionChecks.Cached))
	fmt.Println(sep)
}

// --- JSON output ---

func buildJSONOutput(
	currentOs, currentArch string,
	binaryResults []binaryVerifyResult,
	runtimeResults []runtimeVerifyResult,
	runtimeAppResults []runtimeAppResult,
	bundleResults []bundleVerifyResult,
	versionResults []versionCheckResult,
	summary jsonSummary,
	overallStatus string,
) jsonOutput {
	// Group binary results by app name
	binaryByApp := make(map[string]*jsonBinaryApp)
	var binaryAppOrder []string
	for _, r := range binaryResults {
		if _, ok := binaryByApp[r.AppName]; !ok {
			binaryByApp[r.AppName] = &jsonBinaryApp{
				Name:    r.AppName,
				Version: r.Version,
			}
			binaryAppOrder = append(binaryAppOrder, r.AppName)
		}
		pr := jsonPlatformResult{
			Os:     string(r.Os),
			Arch:   string(r.Arch),
			Libc:   r.Libc,
			Status: r.Status,
		}
		if r.ErrorMsg != "" {
			pr.Error = r.ErrorMsg
		}
		binaryByApp[r.AppName].Platforms = append(binaryByApp[r.AppName].Platforms, pr)
	}
	sort.Strings(binaryAppOrder)
	binaryApps := make([]jsonBinaryApp, 0, len(binaryAppOrder))
	for _, name := range binaryAppOrder {
		binaryApps = append(binaryApps, *binaryByApp[name])
	}

	// Group runtime results by runtime name
	runtimeByName := make(map[string]*jsonManagedRuntime)
	var runtimeOrder []string
	for _, r := range runtimeResults {
		if _, ok := runtimeByName[r.RuntimeName]; !ok {
			runtimeByName[r.RuntimeName] = &jsonManagedRuntime{
				Name: r.RuntimeName,
			}
			runtimeOrder = append(runtimeOrder, r.RuntimeName)
		}
		pr := jsonPlatformResult{
			Os:     string(r.Os),
			Arch:   string(r.Arch),
			Libc:   r.Libc,
			Status: r.Status,
		}
		if r.ErrorMsg != "" {
			pr.Error = r.ErrorMsg
		}
		runtimeByName[r.RuntimeName].Platforms = append(runtimeByName[r.RuntimeName].Platforms, pr)
	}
	sort.Strings(runtimeOrder)
	managedRuntimes := make([]jsonManagedRuntime, 0, len(runtimeOrder))
	for _, name := range runtimeOrder {
		managedRuntimes = append(managedRuntimes, *runtimeByName[name])
	}

	// Runtime apps
	runtimeApps := make([]jsonRuntimeApp, 0, len(runtimeAppResults))
	for _, r := range runtimeAppResults {
		ra := jsonRuntimeApp{
			Name:    r.AppName,
			Kind:    r.Kind,
			Version: r.Version,
			Status:  r.Status,
		}
		if r.ErrorMsg != "" {
			ra.Error = r.ErrorMsg
		}
		runtimeApps = append(runtimeApps, ra)
	}

	// Bundles
	bundles := make([]jsonBundle, 0, len(bundleResults))
	for _, r := range bundleResults {
		jb := jsonBundle{
			Name:    r.BundleName,
			Version: r.Version,
			Status:  r.Status,
		}
		if r.ErrorMsg != "" {
			jb.Error = r.ErrorMsg
		}
		bundles = append(bundles, jb)
	}

	// Version checks
	versionChecks := make([]jsonVersionCheck, 0, len(versionResults))
	for _, r := range versionResults {
		vc := jsonVersionCheck{
			Name:   r.AppName,
			Args:   r.Args,
			Status: r.Status,
		}
		if r.Expected != "" {
			vc.Expected = r.Expected
		}
		if r.Actual != "" {
			vc.Actual = r.Actual
		}
		if r.ErrorMsg != "" {
			vc.Error = r.ErrorMsg
		}
		versionChecks = append(versionChecks, vc)
	}

	return jsonOutput{
		CurrentPlatform: jsonPlatform{Os: currentOs, Arch: currentArch},
		BinaryApps:      binaryApps,
		ManagedRuntimes: managedRuntimes,
		RuntimeApps:     runtimeApps,
		Bundles:         bundles,
		VersionChecks:   versionChecks,
		Summary:         summary,
		OverallStatus:   overallStatus,
	}
}
