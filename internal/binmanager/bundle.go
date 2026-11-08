package binmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/installstate"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

// MapOfBundles maps a bundle name to its Bundle definition.
type MapOfBundles = map[string]*Bundle

// Bundle is a named collection of files, archives and links installed together.
type Bundle struct {
	Version  string                  `json:"version,omitempty"`
	Files    map[string]string       `json:"files,omitempty"`
	Archives map[string]*ArchiveSpec `json:"archives,omitempty"`
	Links    map[string]string       `json:"links,omitempty"`
}

// HasExternalArchives reports whether the bundle contains any URL-fetched archives.
func (b *Bundle) HasExternalArchives() bool {
	for _, a := range b.Archives {
		if a != nil && a.IsExternal() {
			return true
		}
	}
	return false
}

const bundleReadyMarker = ".datamitsu-bundle-ready"

// BundleInstallStats summarizes the results of installing bundles.
type BundleInstallStats struct {
	AlreadyCached []string
	Installed     []string
	Skipped       []string
	Failed        []DownloadResult
}

func (bm *BinManager) installBundleChanged(ctx context.Context, name string) (bool, error) {
	if err := env.RejectInstallChild("bundle installation"); err != nil {
		return false, err
	}
	bundle, ok := bm.mapOfBundles[name]
	if !ok || bundle == nil {
		return false, fmt.Errorf("bundle %q not found", name)
	}
	path, err := bm.ComputeBundlePath(name)
	if err != nil {
		return false, fmt.Errorf("bundle %q: failed to compute path: %w", name, err)
	}
	storeRoot := env.GetStorePath()
	leaseCtx, releaseLease, err := storelock.WithStore(ctx, storeRoot)
	if err != nil {
		return false, fmt.Errorf("acquire store lease: %w", err)
	}
	defer releaseLease()
	relative, err := filepath.Rel(storeRoot, path)
	if err == nil {
		_, err = storelock.ValidateTarget(storeRoot, relative)
	}
	if err != nil {
		return false, fmt.Errorf("resolve bundle store target: %w", err)
	}
	leaseCtx, releaseTarget, err := installstate.Acquire(leaseCtx, storeRoot, relative, "bundle "+name)
	if err != nil {
		return false, fmt.Errorf("lock bundle store target: %w", err)
	}
	defer releaseTarget()
	if bundleReady(path, bundle) {
		return false, nil
	}
	if err := removeStorePath(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("remove incomplete bundle target %q: %w", path, err)
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return false, fmt.Errorf("create bundle parent: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".bundle-staging-")
	if err != nil {
		return false, fmt.Errorf("stage bundle: %w", err)
	}
	defer func() { _ = removeStorePath(staging) }()
	if err := WriteAppFiles(leaseCtx, staging, bundle.Files, bundle.Archives); err != nil {
		return false, fmt.Errorf("bundle %q: failed to write files: %w", name, err)
	}
	if err := os.WriteFile(filepath.Join(staging, bundleReadyMarker), []byte(bundleReadyValue(bundle)), 0o644); err != nil {
		return false, fmt.Errorf("write bundle readiness marker: %w", err)
	}
	if err := os.Rename(staging, path); err != nil {
		return false, fmt.Errorf("publish bundle %q: %w", name, err)
	}
	return true, nil
}

func (bm *BinManager) installBundle(ctx context.Context, name string) error {
	_, err := bm.installBundleChanged(ctx, name)
	return err
}

func bundleReady(path string, bundle *Bundle) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	marker := filepath.Join(path, bundleReadyMarker)
	markerInfo, err := os.Lstat(marker)
	if err != nil || !markerInfo.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(marker)
	return err == nil && string(data) == bundleReadyValue(bundle)
}

func bundleReadyValue(bundle *Bundle) string {
	return "bundle-v1\n" + bundle.Version + "\n" + HashFilesAndArchives(bundle.Files, bundle.Archives) + "\n"
}

// InstallBundleByName installs a single bundle by name.
func (bm *BinManager) InstallBundleByName(ctx context.Context, name string) error {
	if handled, err := bm.callProvision(ctx, "install-bundle", []string{name}, "", false, 0, true, nil); handled || err != nil {
		return err
	}
	return bm.installBundle(ctx, name)
}

// InstallBundles installs all registered bundles, optionally skipping those with external archives.
func (bm *BinManager) InstallBundles(ctx context.Context, skipExternalArchives bool) (BundleInstallStats, error) {
	var remoteStats BundleInstallStats
	handled, remoteErr := bm.callProvision(ctx, "install-bundles", nil, "", skipExternalArchives, 0, true, &remoteStats)
	if handled || remoteErr != nil {
		return remoteStats, remoteErr
	}
	leaseCtx, releaseLease, err := storelock.WithStore(ctx, env.GetStorePath())
	if err != nil {
		return BundleInstallStats{}, err
	}
	defer releaseLease()
	ctx = leaseCtx
	stats := BundleInstallStats{}

	if len(bm.mapOfBundles) == 0 {
		return stats, nil
	}

	names := make([]string, 0, len(bm.mapOfBundles))
	for name := range bm.mapOfBundles {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if ctx.Err() != nil {
			return stats, fmt.Errorf("install bundles canceled: %w", ctx.Err())
		}

		bundle := bm.mapOfBundles[name]
		if bundle == nil {
			continue
		}
		if skipExternalArchives && bundle.HasExternalArchives() {
			stats.Skipped = append(stats.Skipped, name)
			continue
		}

		if _, err := bm.ComputeBundlePath(name); err != nil {
			stats.Failed = append(stats.Failed, DownloadResult{Name: name, Error: err})
			continue
		}
		installed, err := bm.installBundleChanged(ctx, name)
		if err != nil {
			stats.Failed = append(stats.Failed, DownloadResult{Name: name, Error: err})
			continue
		}
		if installed {
			stats.Installed = append(stats.Installed, name)
		} else {
			stats.AlreadyCached = append(stats.AlreadyCached, name)
		}
	}

	if len(stats.Failed) > 0 {
		return stats, fmt.Errorf("%d bundle(s) failed to install", len(stats.Failed))
	}

	return stats, nil
}

// GetBundles returns the map of bundles.
func (bm *BinManager) GetBundles() MapOfBundles {
	return bm.mapOfBundles
}
