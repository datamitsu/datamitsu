package runtimemanager

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/pnpmdefaults"
	"github.com/datamitsu/datamitsu/internal/trace"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/goccy/go-yaml"
	"go.uber.org/zap"
)

// pnpmReporter parses pnpm's --reporter=ndjson stream into live progress for a
// ui.Spinner and collects the failure text. pnpm 12 writes the events to
// stderr and reports a failure as plain text among them (error code, cause and
// hint) rather than as an event, so non-JSON lines are kept for the error
// message. ndjson error events (level "error") are still honored.
type pnpmReporter struct {
	sp *ui.Spinner

	resolved   int
	downloaded int
	added      int
	errs       []string
	text       []string
}

// maxPNPMTextLines bounds the plain-text output kept for an error message.
const maxPNPMTextLines = 200

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
		p.addText(string(b))
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

func (p *pnpmReporter) addText(line string) {
	if len(p.text) == maxPNPMTextLines {
		p.text = p.text[1:]
	}
	p.text = append(p.text, line)
}

// errorOutput returns the best human-readable failure text: pnpm's ndjson error
// events when present, otherwise its plain-text output, otherwise fallback.
func (p *pnpmReporter) errorOutput(fallback string) string {
	if len(p.errs) > 0 {
		return strings.Join(p.errs, "\n")
	}
	if text := strings.TrimSpace(strings.Join(p.text, "\n")); text != "" {
		return text
	}
	return fallback
}

// pnpm.go holds the npm-app installation path shared by the Node and Bun
// runtimes. Since pnpm 12 there is no JavaScript implementation: pnpm is a
// native binary published per platform, so it is a runtime of its own (kind
// "pnpm") that Node and Bun runtimes name with pnpmRuntime, acquired like any
// managed runtime and run directly. The app runtime is only reached as `node`
// by lifecycle scripts, so a Bun app never acquires Node merely to install
// dependencies.

// getPNPMEnvVars returns the per-app npm/pnpm environment shared by Node and
// Bun apps.
//
// NOTE: pnpm does NOT read store-dir / virtual-store-dir from these
// npm_config_* env vars (nor from .npmrc) — it only honors the workspace
// storeDir key, which buildPNPMWorkspaceForApp pins to GetPNPMStorePath(). The
// store_dir entry here is retained so the installer can pre-create that
// directory; virtual_store_dir already matches pnpm's default (node_modules/
// .pnpm under the cwd, which is appEnvPath).
func getPNPMEnvVars(appEnvPath string) map[string]string {
	storePath := env.GetPNPMStorePath()
	return map[string]string{
		"npm_config_store_dir":         storePath,
		"npm_config_virtual_store_dir": filepath.Join(appEnvPath, "node_modules", ".pnpm"),
		"npm_config_global_dir":        filepath.Join(appEnvPath, "global"),
	}
}

type pnpmAppInstallSpec struct {
	appName        string
	packageName    string
	version        string
	binPath        string
	lockFile       string
	dependencies   map[string]string
	runtimeKind    string
	runtimeName    string
	runtimeVersion string
	pnpmRuntime    string
	appEnvPath     string
}

func (rm *RuntimeManager) installPNPMAppOnce(ctx context.Context, spec pnpmAppInstallSpec, customEnv map[string]string, files map[string]string, archives map[string]*binmanager.ArchiveSpec, mergedWorkspaceYAML string) error {
	defer trace.Start(trace.CatInstall, spec.runtimeKind+".installApp").EndWith(trace.A("app", spec.appName))

	if err := validateRelativePath(spec.binPath); err != nil {
		return fmt.Errorf("app %q: unsafe binPath: %w", spec.appName, err)
	}

	appBinPath := filepath.Join(spec.appEnvPath, spec.binPath)
	appModulePkg := filepath.Join(spec.appEnvPath, "node_modules", spec.packageName, "package.json")
	if _, err := os.Stat(appBinPath); err == nil {
		healthPaths := []string{appModulePkg}
		if spec.runtimeKind == "bun" {
			// Stat resolves the alias link, so an app whose runtime moved or was
			// collected reinstalls instead of running with a dangling `node`.
			healthPaths = append(healthPaths, bunNodeAliasPath(spec.appEnvPath))
		}
		installHealthy := true
		for _, path := range healthPaths {
			if _, statErr := os.Stat(path); statErr != nil {
				installHealthy = false
				break
			}
		}
		if installHealthy {
			log.Debug(spec.runtimeKind+" app already installed", zap.String("app", spec.appName), zap.String("path", appBinPath))
			return nil
		}
		log.Warn(spec.runtimeKind+" app entrypoint exists but its install is incomplete, reinstalling", zap.String("app", spec.appName))
		if err := rm.removeAll(spec.appEnvPath); err != nil {
			return fmt.Errorf("app %q: failed to remove stale install at %q before reinstall: %w", spec.appName, spec.appEnvPath, err)
		}
	}

	// pnpm install spawns a child process whose network cannot be cut; refuse
	// before acquiring a runtime or starting the installer.
	if err := httpx.GuardOffline(spec.runtimeKind + " app install of " + spec.appName); err != nil {
		return err
	}

	runtimeBinPath, err := rm.getRuntimePath(ctx, spec.runtimeName)
	if err != nil {
		return fmt.Errorf("failed to acquire %s runtime %q: %w", spec.runtimeKind, spec.runtimeName, err)
	}

	pnpmRuntimeName, _, err := rm.ResolveRuntime(spec.pnpmRuntime, config.RuntimeKindPNPM)
	if err != nil {
		return fmt.Errorf("failed to resolve the pnpm runtime of %s runtime %q: %w", spec.runtimeKind, spec.runtimeName, err)
	}
	pnpmBinPath, err := rm.getRuntimePath(ctx, pnpmRuntimeName)
	if err != nil {
		return fmt.Errorf("failed to acquire pnpm runtime %q: %w", pnpmRuntimeName, err)
	}

	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(spec.appEnvPath)
		}
	}()
	if spec.runtimeKind == "bun" {
		if err := writeBunNodeAlias(spec.appEnvPath, runtimeBinPath); err != nil {
			return fmt.Errorf("failed to create Bun node alias for %q: %w", spec.appName, err)
		}
	}

	filesToWrite := filesWithoutWorkspaceYAML(files)
	if len(filesToWrite) > 0 || len(archives) > 0 {
		if err := binmanager.WriteAppFiles(ctx, spec.appEnvPath, filesToWrite, archives); err != nil {
			return fmt.Errorf("failed to write app files/archives for %q: %w", spec.appName, err)
		}
	}

	// Write pnpm-workspace.yaml after archives so the security defaults always
	// win over content an archive might place at this path.
	if err := writeAppWorkspaceFile(spec.appEnvPath, mergedWorkspaceYAML); err != nil {
		return fmt.Errorf("failed to write pnpm-workspace.yaml for %q: %w", spec.appName, err)
	}

	packageJSON, err := buildPackageJSON(spec.packageName, spec.version, spec.dependencies)
	if err != nil {
		return fmt.Errorf("failed to build package.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(spec.appEnvPath, "package.json"), packageJSON, 0o644); err != nil {
		return fmt.Errorf("failed to write package.json: %w", err)
	}

	if spec.lockFile != "" {
		lockContent, decErr := DecompressLockFile(spec.lockFile)
		if decErr != nil {
			return fmt.Errorf("failed to decompress lock file for %q: %w", spec.appName, decErr)
		}
		if err := os.WriteFile(filepath.Join(spec.appEnvPath, "pnpm-lock.yaml"), []byte(lockContent), 0o644); err != nil {
			return fmt.Errorf("failed to write pnpm-lock.yaml for %q: %w", spec.appName, err)
		}
	}

	envVars := getPNPMEnvVars(spec.appEnvPath)
	for _, dir := range []string{envVars["npm_config_store_dir"], envVars["npm_config_global_dir"]} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", dir, err)
		}
	}

	// Lifecycle scripts reach the app runtime as `node` through PATH. For Bun
	// that is the alias written above, so a script's `node` resolves to Bun.
	inheritedPath := os.Getenv("PATH") //nolint:forbidigo // standard PATH for child process env, not a datamitsu env var
	pathParts := make([]string, 0, 3)
	if spec.runtimeKind == "bun" {
		// Lifecycle scripts reach Bun as `node` through the alias; BUN_OPTIONS
		// gives those processes the installer's ambient-input guards too.
		pathParts = append(pathParts, filepath.Dir(bunNodeAliasPath(spec.appEnvPath)))
		envVars["BUN_OPTIONS"] = bunGuardOptions
	}
	if runtimeBinDir := filepath.Dir(runtimeBinPath); filepath.IsAbs(runtimeBinDir) {
		pathParts = append(pathParts, runtimeBinDir)
	}
	pathParts = append(pathParts, inheritedPath)
	envVars["PATH"] = strings.Join(pathParts, string(os.PathListSeparator))
	envVars = mergeInstallEnv(envVars, customEnv, spec.appEnvPath)

	cmd := exec.CommandContext(ctx, pnpmBinPath, buildPNPMInstallArgs(spec.lockFile != "")...) //nolint:gosec // pnpmBinPath comes from the configured pnpm runtime and the args are fixed
	cmd.Dir = spec.appEnvPath
	cmd.Env = buildEnvWithOverrides(os.Environ(), envVars)

	log.Debug("installing "+spec.runtimeKind+" app",
		zap.String("app", spec.appName),
		zap.String("package", spec.packageName),
		zap.String(spec.runtimeKind, spec.runtimeVersion),
		zap.String("pnpm", pnpmRuntimeName),
	)

	sp := ui.Current().Spinner("Installing " + spec.appName)
	rep := newPNPMReporter(sp)
	stdout, _, err := runInstallCmdStreamingStderr(ctx, cmd, func(line string) { rep.line([]byte(line)) })
	if err != nil {
		sp.Fail()
		ui.Current().Errorln(rep.errorOutput(stdout))
		return fmt.Errorf("failed to install %s app %q: %w", spec.runtimeKind, spec.appName, err)
	}

	sp.Done("Installed " + spec.appName)
	cleanupOnError = false
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

func buildPNPMInstallArgs(hasLockFile bool) []string {
	// --reporter=ndjson emits a machine-readable event stream (on stderr) that
	// the installer parses for live progress instead of letting pnpm's human
	// reporter write raw output over the shared progress display.
	args := []string{"install", "--reporter=ndjson"}
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
	// pnpm ignores npm_config_store_dir / .npmrc store-dir; the workspace
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
