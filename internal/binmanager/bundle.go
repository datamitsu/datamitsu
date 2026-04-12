package binmanager

import (
	"context"
	"fmt"
	"os"
	"sort"

	"go.uber.org/zap"
)

type MapOfBundles = map[string]*Bundle

type Bundle struct {
	Version  string                 `json:"version,omitempty"`
	Files    map[string]string      `json:"files,omitempty"`
	Archives map[string]*ArchiveSpec `json:"archives,omitempty"`
	Links    map[string]string      `json:"links,omitempty"`
}

func (b *Bundle) HasExternalArchives() bool {
	for _, a := range b.Archives {
		if a != nil && a.IsExternal() {
			return true
		}
	}
	return false
}

type BundleInstallStats struct {
	AlreadyCached []string
	Installed     []string
	Skipped       []string
	Failed        []DownloadResult
}

func (bm *BinManager) installBundle(_ context.Context, name string) error {
	bundle, ok := bm.mapOfBundles[name]
	if !ok {
		return fmt.Errorf("bundle %q not found", name)
	}

	path, err := bm.ComputeBundlePath(name)
	if err != nil {
		return fmt.Errorf("bundle %q: failed to compute path: %w", name, err)
	}

	info, statErr := os.Stat(path)
	if statErr == nil && info.IsDir() {
		return nil
	}

	if err := WriteAppFiles(path, bundle.Files, bundle.Archives); err != nil {
		if removeErr := os.RemoveAll(path); removeErr != nil {
			log.Warn("failed to clean up bundle dir after error", zap.String("name", name), zap.String("path", path), zap.Error(removeErr))
		}
		return fmt.Errorf("bundle %q: failed to write files: %w", name, err)
	}

	return nil
}

// InstallBundleByName installs a single bundle by name.
func (bm *BinManager) InstallBundleByName(ctx context.Context, name string) error {
	return bm.installBundle(ctx, name)
}

func (bm *BinManager) InstallBundles(ctx context.Context, skipExternalArchives bool) (BundleInstallStats, error) {
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
			return stats, ctx.Err()
		}

		bundle := bm.mapOfBundles[name]
		if bundle == nil {
			continue
		}
		if skipExternalArchives && bundle.HasExternalArchives() {
			stats.Skipped = append(stats.Skipped, name)
			continue
		}

		path, err := bm.ComputeBundlePath(name)
		if err != nil {
			stats.Failed = append(stats.Failed, DownloadResult{Name: name, Error: err})
			continue
		}
		info, statErr := os.Stat(path)
		if statErr == nil && info.IsDir() {
			stats.AlreadyCached = append(stats.AlreadyCached, name)
			continue
		}

		err = bm.installBundle(ctx, name)
		if err != nil {
			stats.Failed = append(stats.Failed, DownloadResult{Name: name, Error: err})
			continue
		}

		stats.Installed = append(stats.Installed, name)
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
