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
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/installstate"
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
	sp       *ui.Spinner
	progress func(string, int64, int64, string)

	resolved   int
	downloaded int
	added      int
	errs       []string
	text       []string
}

// maxPNPMTextLines bounds the plain-text output kept for an error message.
const maxPNPMTextLines = 200

func newPNPMReporter(sp *ui.Spinner, progress func(string, int64, int64, string)) *pnpmReporter {
	return &pnpmReporter{sp: sp, progress: progress}
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

	p.sp.SetDetail(fmt.Sprintf("resolved %4d, downloaded %4d, added %4d", p.resolved, p.downloaded, p.added))
	if p.progress != nil {
		p.progress("install", int64(p.downloaded), -1, "packages")
	}
}

func (p *pnpmReporter) addText(line string) {
	if len(p.text) == maxPNPMTextLines {
		p.text = p.text[1:]
	}
	p.text = append(p.text, line)
}

func (p *pnpmReporter) errorOutput(fallback string) string {
	if len(p.errs) > 0 {
		return strings.Join(p.errs, "\n")
	}
	if text := strings.TrimSpace(strings.Join(p.text, "\n")); text != "" {
		return text
	}
	return fallback
}

func getPNPMEnvVars(appEnvPath, storePath, runtimeDataPath string) map[string]string {
	return map[string]string{
		"npm_config_store_dir":         storePath,
		"npm_config_virtual_store_dir": filepath.Join(appEnvPath, "node_modules", ".pnpm"),
		"npm_config_global_dir":        filepath.Join(appEnvPath, "global"),
		"npm_config_cache":             filepath.Join(runtimeDataPath, "npm-cache"),
	}
}

func getNodeEnvVars(appEnvPath, storePath, runtimeDataPath string) map[string]string {
	vars := getPNPMEnvVars(appEnvPath, storePath, runtimeDataPath)
	vars["NODE_COMPILE_CACHE"] = filepath.Join(runtimeDataPath, "node-compile")
	return vars
}

func getBunEnvVars(appEnvPath, storePath, runtimeDataPath string) map[string]string {
	vars := getPNPMEnvVars(appEnvPath, storePath, runtimeDataPath)
	vars["BUN_RUNTIME_TRANSPILER_CACHE_PATH"] = filepath.Join(runtimeDataPath, "bun-cache")
	return vars
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
	runtimeConfig  config.RuntimeConfig
}

func (rm *RuntimeManager) installPNPMAppOnce(ctx context.Context, spec pnpmAppInstallSpec, customEnv map[string]string, files map[string]string, archives map[string]*binmanager.ArchiveSpec, mergedWorkspaceYAML string) error {
	defer trace.Start(trace.CatInstall, spec.runtimeKind+".installApp").EndWith(trace.A("app", spec.appName))
	spec.appEnvPath = lockWorkspaceDestination(ctx, spec.appEnvPath)
	if err := validateRelativePath(spec.binPath); err != nil {
		return fmt.Errorf("app %q: unsafe binPath: %w", spec.appName, err)
	}

	appBinPath := filepath.Join(spec.appEnvPath, spec.binPath)
	appModulePkg := filepath.Join(spec.appEnvPath, "node_modules", spec.packageName, "package.json")
	isHealthy := func() bool {
		if !appReady(spec.appEnvPath) {
			return false
		}
		if _, err := os.Stat(appBinPath); err != nil {
			return false
		}
		if _, err := os.Stat(appModulePkg); err != nil {
			return false
		}
		if spec.runtimeKind == "bun" {
			if _, err := os.Stat(bunNodeAliasPath(spec.appEnvPath)); err != nil {
				return false
			}
		}
		return true
	}
	_, releaseCheck, err := lockAppTarget(ctx, spec.appEnvPath)
	if err != nil {
		return err
	}
	ready := isHealthy()
	releaseCheck()
	if ready {
		return nil
	}

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

	leaseCtx, releaseApp, err := lockAppTarget(ctx, spec.appEnvPath)
	if err != nil {
		return err
	}
	defer releaseApp()
	ctx = leaseCtx
	if isHealthy() {
		return nil
	}
	if _, err := os.Stat(spec.appEnvPath); err == nil {
		if err := rm.removeAll(spec.appEnvPath); err != nil {
			return fmt.Errorf("app %q: failed to remove stale install at %q before reinstall: %w", spec.appName, spec.appEnvPath, err)
		}
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
	_, envVars, err := rm.RuntimeOwnedEnvironment(spec.runtimeName, spec.runtimeConfig, spec.appEnvPath)
	if err != nil {
		return err
	}
	for _, dir := range []string{envVars["npm_config_store_dir"], envVars["npm_config_global_dir"]} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", dir, err)
		}
	}

	inheritedPath := os.Getenv("PATH") //nolint:forbidigo // standard PATH for child process env, not a datamitsu env var
	pathParts := make([]string, 0, 3)
	if spec.runtimeKind == "bun" {
		pathParts = append(pathParts, filepath.Dir(bunNodeAliasPath(spec.appEnvPath)))
		envVars["BUN_OPTIONS"] = bunGuardOptions
	}
	if runtimeBinDir := filepath.Dir(runtimeBinPath); filepath.IsAbs(runtimeBinDir) {
		pathParts = append(pathParts, runtimeBinDir)
	}
	pathParts = append(pathParts, inheritedPath)
	envVars["PATH"] = strings.Join(pathParts, string(os.PathListSeparator))
	envVars = mergeInstallEnv(envVars, customEnv, spec.appEnvPath)
	overridesWorkspace, err := pnpmWorkspaceEnvOverride(mergedWorkspaceYAML)
	if err != nil {
		return fmt.Errorf("app %q: %w", spec.appName, err)
	}
	cmd := exec.CommandContext(context.WithoutCancel(ctx), pnpmBinPath, buildPNPMInstallArgs(spec.lockFile != "")...) //nolint:gosec // configured verified pnpm runtime and fixed arguments
	cmd.Dir = spec.appEnvPath
	cmd.Env = buildEnvWithOverrides(withoutEnv(os.Environ(), overridesWorkspace), envVars)
	log.Debug("installing "+spec.runtimeKind+" app", zap.String("app", spec.appName), zap.String("package", spec.packageName), zap.String(spec.runtimeKind, spec.runtimeVersion), zap.String("pnpm", pnpmRuntimeName))
	sp := ui.Current().Spinner("Installing " + spec.appName)
	rep := newPNPMReporter(sp, func(phase string, completed, total int64, unit string) {
		installstate.Progress(ctx, installstate.Phase(phase), completed, total, unit)
	})
	stdout, _, err := runInstallCmdStreamingStderr(ctx, cmd, func(line string) { rep.line([]byte(line)) })
	if err != nil {
		sp.Fail()
		ui.Current().Errorln(rep.errorOutput(stdout))
		return fmt.Errorf("failed to install %s app %q: %w", spec.runtimeKind, spec.appName, err)
	}
	sp.Done("Installed " + spec.appName)
	if err := markAppReady(spec.appEnvPath); err != nil {
		return err
	}
	cleanupOnError = false
	return nil
}

func filesWithWorkspaceYAML(files map[string]string, mergedYAML string) map[string]string {
	out := make(map[string]string, len(files)+1)
	maps.Copy(out, files)
	out["pnpm-workspace.yaml"] = mergedYAML
	return out
}

func pnpmWorkspaceEnvOverride(mergedYAML string) (func(key string) bool, error) {
	var settings map[string]any
	if err := yaml.Unmarshal([]byte(mergedYAML), &settings); err != nil {
		return nil, fmt.Errorf("failed to parse the merged pnpm-workspace.yaml: %w", err)
	}
	owned := make(map[string]bool, len(settings))
	for key := range settings {
		owned[pnpmEnvSettingName(key)] = true
	}
	return func(key string) bool {
		setting, ok := strings.CutPrefix(strings.ToLower(key), "pnpm_config_")
		return ok && (owned[setting] || strings.HasPrefix(setting, "minimum_release_age"))
	}, nil
}

func pnpmEnvSettingName(key string) string {
	var b strings.Builder
	for i, r := range key {
		switch {
		case r == '-':
			b.WriteByte('_')
		case r >= 'A' && r <= 'Z':
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func buildPNPMInstallArgs(hasLockFile bool) []string {
	args := []string{"install", "--reporter=ndjson"}
	if hasLockFile {
		args = append(args, "--frozen-lockfile")
	}
	return args
}

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

func filesWithoutWorkspaceYAML(files map[string]string) map[string]string {
	if _, has := files["pnpm-workspace.yaml"]; !has {
		return files
	}
	filtered := make(map[string]string, len(files)-1)
	for k, v := range files {
		if k != "pnpm-workspace.yaml" {
			filtered[k] = v
		}
	}
	return filtered
}

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

var buildPNPMWorkspace = buildPNPMWorkspaceForApp

func (rm *RuntimeManager) pnpmWorkspace(runtimeRef string, kind config.RuntimeKind, files map[string]string) (string, error) {
	runtimeName, rc, err := rm.ResolveRuntime(runtimeRef, kind)
	if err != nil {
		return "", err
	}
	storePath, err := rm.pnpmDataPath(runtimeName, rc)
	if err != nil {
		return "", err
	}
	return buildPNPMWorkspace(files, storePath)
}

func buildPNPMWorkspaceForApp(files map[string]string, storePath string) (string, error) {
	userYAML := ""
	if files != nil {
		userYAML = files["pnpm-workspace.yaml"]
	}
	merged, err := mergePNPMWorkspaceConfig(pnpmdefaults.Defaults(), userYAML)
	if err != nil {
		return "", err
	}
	merged["storeDir"] = storePath
	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("failed to marshal pnpm-workspace.yaml: %w", err)
	}
	return string(out), nil
}

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

func buildPackageJSON(packageName, version string, deps map[string]string) ([]byte, error) {
	allDeps := make(map[string]string, len(deps)+1)
	allDeps[packageName] = version
	maps.Copy(allDeps, deps)
	pkg := map[string]any{"name": "datamitsu-app-" + strings.NewReplacer("@", "", "/", "-").Replace(packageName), "version": "0.0.0", "private": true, "dependencies": allDeps, "type": "module"}
	out, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal package.json: %w", err)
	}
	return out, nil
}
