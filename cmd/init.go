package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/managedconfig"
	"github.com/datamitsu/datamitsu/internal/project"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"github.com/datamitsu/datamitsu/internal/ui"

	"github.com/spf13/cobra"
)

var (
	initDryRun            bool
	initAll               bool
	initSkipDownload      bool
	initFailOnDownloadErr bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize project - download binaries and run init commands",
	Long: `Downloads required binaries and runs initialization commands (like npm install, lefthook install, etc.)
By default, downloads only Required binaries with concurrency of 3 (configurable via DATAMITSU_CONCURRENCY env var).`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().BoolVar(&initDryRun, "dry-run", false, "Show what would be done without making changes")
	initCmd.Flags().BoolVar(&initAll, "all", false, "Download all binaries (both required and optional)")
	initCmd.Flags().BoolVar(&initSkipDownload, "skip-download", false, "Skip binary downloads")
	initCmd.Flags().BoolVar(&initFailOnDownloadErr, "fail-on-download-error", false, "Stop init process if any binary download fails")
	rootCmd.AddCommand(initCmd)
}

func runInit(_ *cobra.Command, _ []string) error {
	ctx := context.Background()
	start := time.Now()

	// Activate the shared display so downloads (binaries, runtimes, JARs) render
	// as bars in one container on a terminal, or throttled lines under CI. The
	// whole run is framed as one "init" bracket; section bodies print safely
	// above any active bars.
	disp := ui.New(term.DetectMode())
	restore := ui.Activate(disp)
	defer func() {
		disp.Close()
		restore()
	}()

	disp.Banner(ldflags.PackageName, ldflags.Version)

	cwdPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get cwd: %w", err)
	}

	rootPath, err := traverser.GetGitRoot(ctx, cwdPath)
	if err != nil {
		return fmt.Errorf("failed to get git root: %w", err)
	}

	if err := checkInitGitRoot(cwdPath, rootPath); err != nil {
		return err
	}

	cfg, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	detector := project.NewDetector(rootPath, cfg.ProjectTypes)
	projectTypes, err := detector.DetectAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to detect project types: %w", err)
	}

	// Open the single init frame: bold bracket header + dimmed project types.
	disp.PhaseOpen("init")
	if len(projectTypes) == 0 {
		disp.PhaseBody(clr.Yellow("no project types detected"))
	} else {
		shortTypes := make([]string, len(projectTypes))
		for i, pt := range projectTypes {
			shortTypes[i] = shortInitType(pt)
		}
		disp.PhaseBody(clr.Faint(strings.Join(shortTypes, " · ")))
	}
	if initDryRun {
		disp.PhaseBody("")
		disp.PhaseBody(clr.Cyan("dry-run") + clr.Faint(" — no changes will be made"))
	}

	rm := runtimemanager.New(cfg.Runtimes)
	binMgr := binmanager.New(cfg.Apps, cfg.Bundles, rm)

	var runtimeCount, toolCount, bundleCount, linkCount, failed int

	if !initSkipDownload && !initDryRun {
		n, f, rErr := reportRuntimes(ctx, disp, rm, cfg, initAll)
		if rErr != nil {
			return fmt.Errorf("failed to install runtimes: %w", rErr)
		}
		runtimeCount, failed = n, failed+f

		n, f, rErr = reportBinaries(ctx, disp, binMgr)
		if rErr != nil {
			return fmt.Errorf("failed to download binaries: %w", rErr)
		}
		toolCount, failed = n, failed+f

		// Runtime-managed apps (node/UV) that provide config links must be
		// installed before CreateDatamitsuLinks can resolve their roots. Their
		// install feedback is the shared download bars; no extra section here.
		if err := installRuntimeAppsWithLinks(ctx, binMgr, cfg, initAll); err != nil {
			return fmt.Errorf("failed to install runtime apps with links: %w", err)
		}
	}

	if !initDryRun {
		n, f, bErr := reportBundles(ctx, disp, binMgr, initSkipDownload)
		if bErr != nil {
			return fmt.Errorf("failed to install bundles: %w", bErr)
		}
		bundleCount, failed = n, failed+f
	}

	linkCount, err = reportConfigLinks(disp, rootPath, cfg, binMgr, initDryRun)
	if err != nil {
		return fmt.Errorf("failed to set up config links: %w", err)
	}

	hookErr := reportInitCommands(ctx, disp, rootPath, projectTypes, cfg, binMgr, initDryRun)

	printInitFooter(disp, initFooterCounts{
		types:    len(projectTypes),
		runtimes: runtimeCount,
		tools:    toolCount,
		bundles:  bundleCount,
		links:    linkCount,
		failed:   failed,
		dur:      ui.FormatDurationShort(time.Since(start).Milliseconds()),
		dryRun:   initDryRun,
		hadError: hookErr != nil,
	})

	if hookErr != nil {
		return hookErr
	}

	return nil
}

// initRow is one item line in an install sub-section (runtimes/tools/bundles).
type initRow struct {
	sym    string // colored glyph
	name   string
	status string // already-colored status text
}

// okRows builds success rows (green ✓, faint status) for a list of names.
func okRows(names []string, status string) []initRow {
	rows := make([]initRow, 0, len(names))
	for _, name := range names {
		rows = append(rows, initRow{sym: clr.Green("✓"), name: name, status: clr.Faint(status)})
	}
	return rows
}

// skipRows builds skipped rows (faint glyph and status) for a list of names.
func skipRows(names []string) []initRow {
	rows := make([]initRow, 0, len(names))
	for _, name := range names {
		rows = append(rows, initRow{sym: clr.Faint("−"), name: name, status: clr.Faint("skipped")})
	}
	return rows
}

// failRow builds a failure row (red ✗ and red message).
func failRow(name string, err error) initRow {
	msg := "failed"
	if err != nil {
		msg = "failed: " + err.Error()
	}
	return initRow{sym: clr.Red("✗"), name: name, status: clr.Red(msg)}
}

// initSection renders one "┃ <title>" sub-section with item rows, aligning the
// status column to the widest name. Nothing is printed for an empty section.
func initSection(disp *ui.Display, title string, rows []initRow) {
	if len(rows) == 0 {
		return
	}
	disp.PhaseBody("")
	disp.PhaseBody(clr.Bold(title))

	width := 0
	for _, r := range rows {
		if n := utf8.RuneCountInString(r.name); n > width {
			width = n
		}
	}
	for _, r := range rows {
		pad := strings.Repeat(" ", width-utf8.RuneCountInString(r.name)+2)
		disp.PhaseBody("  " + r.sym + " " + r.name + pad + r.status)
	}
}

// initLabelWidth aligns the compact "links"/"hooks" label lines.
const initLabelWidth = 7

// initLabelLine renders a compact "┃ <label>   <value>" line (preceded by a
// spacer), used for the links and hooks summaries.
func initLabelLine(disp *ui.Display, label, value string) {
	disp.PhaseBody("")
	pad := strings.Repeat(" ", max(initLabelWidth-utf8.RuneCountInString(label), 1))
	disp.PhaseBody(clr.Bold(label) + pad + value)
}

func reportRuntimes(ctx context.Context, disp *ui.Display, rm *runtimemanager.RuntimeManager, cfg *config.Config, includeAll bool) (count, failed int, err error) {
	names := runtimemanager.CollectRequiredRuntimes(cfg.Apps, cfg.Runtimes, includeAll)
	if len(names) == 0 {
		return 0, 0, nil
	}

	stats, err := rm.InstallRuntimes(ctx, names, env.GetConcurrency())
	if err != nil {
		return 0, 0, err
	}

	rows := okRows(stats.Downloaded, "downloaded")
	rows = append(rows, okRows(stats.AlreadyCached, "cached")...)
	rows = append(rows, skipRows(stats.Skipped)...)
	for _, f := range stats.Failed {
		rows = append(rows, failRow(f.Name, f.Error))
	}
	initSection(disp, "runtimes", rows)

	if initFailOnDownloadErr && len(stats.Failed) > 0 {
		return 0, 0, fmt.Errorf("failed to download %d runtime(s)", len(stats.Failed))
	}
	return len(stats.Downloaded) + len(stats.AlreadyCached), len(stats.Failed), nil
}

func reportBinaries(ctx context.Context, disp *ui.Display, binMgr *binmanager.BinManager) (count, failed int, err error) {
	stats, err := binMgr.InstallWithConcurrency(ctx, initAll, env.GetConcurrency(), initFailOnDownloadErr)
	if err != nil {
		return 0, 0, err
	}

	rows := okRows(stats.Downloaded, "downloaded")
	rows = append(rows, okRows(stats.AlreadyCached, "cached")...)
	rows = append(rows, skipRows(stats.Skipped)...)
	for _, f := range stats.Failed {
		rows = append(rows, failRow(f.Name, f.Error))
	}
	initSection(disp, "tools", rows)

	return len(stats.Downloaded) + len(stats.AlreadyCached), len(stats.Failed), nil
}

func reportBundles(ctx context.Context, disp *ui.Display, binMgr *binmanager.BinManager, skipDownload bool) (count, failed int, err error) {
	stats, err := binMgr.InstallBundles(ctx, skipDownload)

	rows := okRows(stats.Installed, "installed")
	rows = append(rows, okRows(stats.AlreadyCached, "cached")...)
	rows = append(rows, skipRows(stats.Skipped)...)
	for _, f := range stats.Failed {
		rows = append(rows, failRow(f.Name, f.Error))
	}
	initSection(disp, "bundles", rows)

	if err != nil {
		return 0, 0, err
	}
	return len(stats.Installed) + len(stats.AlreadyCached), len(stats.Failed), nil
}

func reportConfigLinks(disp *ui.Display, rootPath string, cfg *config.Config, binMgr *binmanager.BinManager, dryRun bool) (int, error) {
	if !hasAnyLinks(cfg.Apps, cfg.Bundles) {
		// Even without links, create .datamitsu/ with type definitions so that
		// /// <reference path=".datamitsu/datamitsu.config.d.ts" /> works.
		if err := managedconfig.CreateDatamitsuTypeDefinitions(rootPath, dryRun); err != nil {
			return 0, err
		}
		return 0, nil
	}

	var bundleResolver managedconfig.InstallRootResolver
	if len(cfg.Bundles) > 0 {
		bundleResolver = &bundleRootResolver{bm: binMgr}
	}

	createdLinks, err := managedconfig.CreateDatamitsuLinks(rootPath, cfg.Apps, binMgr, cfg.Bundles, bundleResolver, dryRun)
	if err != nil {
		return 0, err
	}

	n := len(createdLinks)
	if n > 0 {
		value := fmt.Sprintf("%d linked", n)
		if dryRun {
			value = fmt.Sprintf("would link %d", n)
		}
		initLabelLine(disp, "links", value+clr.Faint(" → .datamitsu/"))
	}
	return n, nil
}

func reportInitCommands(ctx context.Context, disp *ui.Display, rootPath string, projectTypes []string, cfg *config.Config, binMgr *binmanager.BinManager, dryRun bool) error {
	names := make([]string, 0, len(cfg.InitCommands))
	for name := range cfg.InitCommands {
		names = append(names, name)
	}
	sort.Strings(names)

	type hookResult struct {
		name string
		ok   bool
	}
	var results []hookResult
	var failOutput string
	var hookErr error

	for _, name := range names {
		ic := cfg.InitCommands[name]

		if !isApplicableInitCommand(ic, projectTypes) {
			continue
		}
		if ic.When != "" {
			if _, statErr := os.Stat(filepath.Join(rootPath, ic.When)); os.IsNotExist(statErr) {
				continue
			}
		}

		if dryRun {
			results = append(results, hookResult{name: name, ok: true})
			continue
		}

		// Capture output and surface it only on failure, so a clean run shows just
		// the hook name instead of the tool's own chatter (e.g. lefthook sync).
		out, err := binMgr.ExecCaptured(ctx, ic.Command, ic.Args)
		if err != nil {
			results = append(results, hookResult{name: name, ok: false})
			failOutput = out
			hookErr = fmt.Errorf("failed to run %s: %w", name, err)
			break
		}
		results = append(results, hookResult{name: name, ok: true})
	}

	if len(results) > 0 {
		parts := make([]string, 0, len(results))
		for _, r := range results {
			if r.ok {
				parts = append(parts, clr.Green("✓")+" "+r.name)
			} else {
				parts = append(parts, clr.Red("✗")+" "+r.name)
			}
		}
		initLabelLine(disp, "hooks", strings.Join(parts, "  "))
	}

	if hookErr != nil {
		for line := range strings.SplitSeq(strings.TrimRight(failOutput, "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			disp.PhaseBody("  " + clr.Red(line))
		}
	}

	return hookErr
}

// initFooterCounts carries the tallies shown in the closing bracket rule.
type initFooterCounts struct {
	types, runtimes, tools, bundles, links, failed int
	dur                                            string
	dryRun, hadError                               bool
}

// printInitFooter renders the closing "┗━ … ━" rule summarizing the run.
func printInitFooter(disp *ui.Display, c initFooterCounts) {
	add := func(parts []string, n int, singular, plural string) []string {
		if n <= 0 {
			return parts
		}
		word := plural
		if n == 1 {
			word = singular
		}
		return append(parts, fmt.Sprintf("%d %s", n, word))
	}

	var parts []string
	parts = add(parts, c.types, "type", "types")
	parts = add(parts, c.runtimes, "runtime", "runtimes")
	parts = add(parts, c.tools, "tool", "tools")
	parts = add(parts, c.bundles, "bundle", "bundles")
	parts = add(parts, c.links, "link", "links")
	parts = append(parts, c.dur)
	body := strings.Join(parts, " · ")

	prefix := "ready"
	switch {
	case c.hadError:
		prefix = "failed"
	case c.dryRun:
		prefix = "dry-run"
	}

	plain := prefix + " · " + body
	coloredPrefix := clr.Bold(prefix)
	if c.hadError {
		coloredPrefix = clr.Bold(clr.Red(prefix))
	}
	colored := coloredPrefix + clr.Faint(" · "+body)
	if c.failed > 0 {
		fp := fmt.Sprintf("%d failed", c.failed)
		plain += " · " + fp
		colored += clr.Faint(" · ") + clr.Red(fp)
	}

	disp.PhaseClose(plain, colored)
}

// shortInitType trims the redundant "-package"/"-project" suffix from a detected
// project type for the compact header line (e.g. "golang-package" → "golang").
func shortInitType(s string) string {
	s = strings.TrimSuffix(s, "-package")
	s = strings.TrimSuffix(s, "-project")
	return s
}

// commandInfoGetter abstracts GetCommandInfo for testability.
type commandInfoGetter interface {
	GetCommandInfo(ctx context.Context, appName string) (*binmanager.CommandInfo, error)
}

func installRuntimeAppsWithLinks(ctx context.Context, binMgr *binmanager.BinManager, cfg *config.Config, installAll bool) error {
	var appsToInstall []string
	if installAll {
		appsToInstall = filterAppsForSmartInit(cfg.Apps, allAppNames(cfg.Apps))
	} else {
		referencedApps := scanReferencedApps(cfg)
		appsToInstall = filterAppsForSmartInit(cfg.Apps, referencedApps)

		// Also include any runtime-managed app that has Links defined,
		// even if not directly referenced by tool operations. Apps with
		// Links may only be referenced via tools.Config.linkPath() in
		// ConfigSetup sections, which scanReferencedApps does not inspect.
		linkApps := allRuntimeAppsWithLinks(cfg.Apps)
		appsToInstall = mergeUnique(appsToInstall, linkApps)
	}
	return installSmartInitApps(ctx, binMgr, appsToInstall)
}

// allAppNames returns all app names from the config (for --all mode).
func allAppNames(apps binmanager.MapOfApps) []string {
	result := make([]string, 0, len(apps))
	for name := range apps {
		result = append(result, name)
	}
	return result
}

// scanReferencedApps collects unique app names from all tool operations.
func scanReferencedApps(cfg *config.Config) []string {
	seen := make(map[string]bool)
	for _, tool := range cfg.Tools {
		for _, op := range tool.Operations {
			if op.App != "" {
				seen[op.App] = true
			}
		}
	}

	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// filterAppsForSmartInit returns runtime-managed app names that are both
// referenced by tools and have Links defined.
func filterAppsForSmartInit(apps binmanager.MapOfApps, referencedApps []string) []string {
	refSet := make(map[string]bool, len(referencedApps))
	for _, name := range referencedApps {
		refSet[name] = true
	}

	var result []string
	for name, app := range apps {
		if !refSet[name] {
			continue
		}
		if len(app.Links) == 0 {
			continue
		}
		if app.Binary != nil || app.Shell != nil || app.Jvm != nil {
			continue
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// allRuntimeAppsWithLinks returns names of all runtime-managed (UV/node) apps
// that have Links defined. These apps provide config files for symlinking
// and should always be installed during init.
func allRuntimeAppsWithLinks(apps binmanager.MapOfApps) []string {
	var result []string
	for name, app := range apps {
		if len(app.Links) == 0 {
			continue
		}
		if app.Binary != nil || app.Shell != nil || app.Jvm != nil {
			continue
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// mergeUnique merges two sorted string slices and returns a sorted, deduplicated result.
func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		seen[s] = true
	}
	result := make([]string, 0, len(seen))
	for s := range seen {
		result = append(result, s)
	}
	sort.Strings(result)
	return result
}

// installSmartInitApps installs the given list of runtime-managed apps. Per-app
// download feedback is emitted by the runtime installers through the shared ui
// display, so nothing is printed here.
func installSmartInitApps(ctx context.Context, getter commandInfoGetter, appsToInstall []string) error {
	sort.Strings(appsToInstall)
	for _, name := range appsToInstall {
		if _, err := getter.GetCommandInfo(ctx, name); err != nil {
			return fmt.Errorf("failed to install %s: %w", name, err)
		}
	}
	return nil
}

// bundleRootResolver adapts BinManager.GetBundleRoot to the InstallRootResolver interface.
type bundleRootResolver struct {
	bm *binmanager.BinManager
}

func (r *bundleRootResolver) GetInstallRoot(name string) (string, error) {
	return r.bm.GetBundleRoot(name)
}

func hasAnyLinks(apps binmanager.MapOfApps, bundles binmanager.MapOfBundles) bool {
	for _, app := range apps {
		if len(app.Links) > 0 {
			return true
		}
	}
	for _, bundle := range bundles {
		if bundle != nil && len(bundle.Links) > 0 {
			return true
		}
	}
	return false
}

func checkInitGitRoot(cwdPath, rootPath string) error {
	resolvedCwd, errCwd := filepath.EvalSymlinks(cwdPath)
	resolvedRoot, errRoot := filepath.EvalSymlinks(rootPath)
	if errCwd != nil || errRoot != nil {
		resolvedCwd = filepath.Clean(cwdPath)
		resolvedRoot = filepath.Clean(rootPath)
	}
	if resolvedCwd != resolvedRoot {
		return fmt.Errorf("init must be run from git root: currently in %s, git root is %s", cwdPath, rootPath)
	}
	return nil
}

func isApplicableInitCommand(initCmd config.InitCommand, projectTypes []string) bool {
	// If no project types specified, applies to all
	if len(initCmd.ProjectTypes) == 0 {
		return true
	}

	// Check if any project type matches
	for _, cmdType := range initCmd.ProjectTypes {
		if slices.Contains(projectTypes, cmdType) {
			return true
		}
	}

	return false
}
