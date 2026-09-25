package runtimemanager

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/pelletier/go-toml/v2"
)

// uvWindow is the release-age window a `uv sync` runs with: a cutoff for every
// package, and per-package cutoffs as "name=value".
type uvWindow struct {
	excludeNewer string
	packages     []string
}

func (w uvWindow) args() []string {
	var args []string
	if w.excludeNewer != "" {
		args = append(args, "--exclude-newer", w.excludeNewer)
	}
	for _, p := range w.packages {
		args = append(args, "--exclude-newer-package", p)
	}
	return args
}

// uvDefaultExcludeNewer is the window a uv app resolves with when it has no
// lock file. It is the compile-time constant rather than the effective
// DATAMITSU_MIN_RELEASE_AGE, because the lock records the window: a lock must
// come out the same whatever environment generated it.
func uvDefaultExcludeNewer() string {
	return uvSpan(runtimeconfig.MinimumReleaseAgeMinutes)
}

// uvSpan spells minutes as the ISO 8601 duration uv stores in the lock, so the
// lock records exactly the value it was given.
func uvSpan(minutes int) string {
	const day = 24 * 60
	switch {
	case minutes <= 0:
		return ""
	case minutes%day == 0:
		return fmt.Sprintf("P%dD", minutes/day)
	default:
		return fmt.Sprintf("PT%dM", minutes)
	}
}

// uvLockWindow reads the window a lock was resolved with. `uv sync --locked`
// accepts a lock only when given the same window again, in the same unit, so an
// install passes back what the lock recorded instead of the current default: a
// lock without [options] installs with no window, and changing the default never
// invalidates a lock that is already published.
func uvLockWindow(lock string) (uvWindow, error) {
	var parsed struct {
		Options struct {
			ExcludeNewer        string         `toml:"exclude-newer"`
			ExcludeNewerSpan    string         `toml:"exclude-newer-span"`
			ExcludeNewerPackage map[string]any `toml:"exclude-newer-package"`
		} `toml:"options"`
	}
	if err := toml.Unmarshal([]byte(lock), &parsed); err != nil {
		return uvWindow{}, fmt.Errorf("failed to read [options] from uv.lock: %w", err)
	}
	opts := parsed.Options

	// With a span, exclude-newer holds a placeholder timestamp that uv ignores.
	w := uvWindow{excludeNewer: opts.ExcludeNewerSpan}
	if w.excludeNewer == "" {
		w.excludeNewer = opts.ExcludeNewer
	}

	names := make([]string, 0, len(opts.ExcludeNewerPackage))
	for name := range opts.ExcludeNewerPackage {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		cutoff, err := uvPackageCutoff(opts.ExcludeNewerPackage[name])
		if err != nil {
			return uvWindow{}, fmt.Errorf("failed to read exclude-newer-package %q from uv.lock: %w", name, err)
		}
		w.packages = append(w.packages, name+"="+cutoff)
	}
	return w, nil
}

// uvPackageCutoff turns one recorded exclude-newer-package value back into the
// command-line form: a timestamp, a { timestamp, span } table for a relative
// cutoff, or false for a package exempt from the window.
func uvPackageCutoff(v any) (string, error) {
	switch v := v.(type) {
	case string:
		if v != "" {
			return v, nil
		}
	case bool:
		if !v {
			return "false", nil
		}
	case map[string]any:
		if span, _ := v["span"].(string); span != "" {
			return span, nil
		}
		if ts, _ := v["timestamp"].(string); ts != "" {
			return ts, nil
		}
	}
	return "", fmt.Errorf("unsupported value %v", v)
}

func uvAppConfigPath(appEnvPath string) (string, error) {
	path := filepath.Join(appEnvPath, "uv.toml")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("failed to inspect uv.toml: %w", err)
	}
	return path, nil
}

// uvResolveWindow is the window for an install without a lock file. A window the
// app's own uv.toml sets wins over the default, as an App.files
// pnpm-workspace.yaml does for pnpm apps; uv reads it from that file.
func uvResolveWindow(configPath string) (uvWindow, error) {
	if configPath == "" {
		return uvWindow{excludeNewer: uvDefaultExcludeNewer()}, nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return uvWindow{}, fmt.Errorf("failed to read uv.toml: %w", err)
	}
	var settings map[string]any
	if err := toml.Unmarshal(data, &settings); err != nil {
		return uvWindow{}, fmt.Errorf("failed to parse uv.toml: %w", err)
	}
	if _, ok := settings["exclude-newer"]; ok {
		return uvWindow{}, nil
	}
	return uvWindow{excludeNewer: uvDefaultExcludeNewer()}, nil
}

// uvInheritedOverride reports the inherited variables that would override what
// datamitsu passes on the uv command line: UV_CONFIG_FILE wins over
// --no-config, and the exclude-newer variables apply whenever no flag is passed,
// which is how a lock without [options] installs.
func uvInheritedOverride(key string) bool {
	for _, owned := range []string{"UV_CONFIG_FILE", "UV_EXCLUDE_NEWER", "UV_EXCLUDE_NEWER_PACKAGE"} {
		if strings.EqualFold(key, owned) {
			return true
		}
	}
	return false
}

// uvReleaseAgeHint explains a resolution the default window refused. uv names
// exclude-newer in its own hint but not where the window came from.
func uvReleaseAgeHint(output string, w uvWindow) string {
	if w.excludeNewer == "" || w.excludeNewer != uvDefaultExcludeNewer() || !strings.Contains(output, "exclude-newer") {
		return ""
	}
	return fmt.Sprintf("uv app lock files resolve with datamitsu's minimum release age of %d minutes "+
		"(runtimeconfig.MinimumReleaseAgeMinutes, passed as --exclude-newer %s), so nothing uploaded more recently can be selected. "+
		"Pin an older version, or exempt a package with exclude-newer-package in the app's uv.toml (App.files)",
		runtimeconfig.MinimumReleaseAgeMinutes, w.excludeNewer)
}
