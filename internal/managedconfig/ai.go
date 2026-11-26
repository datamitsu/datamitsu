package managedconfig

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

// InstallAI prepares the complete desired state before changing the checkout.
func InstallAI(ctx context.Context, gitRoot string, cfg *config.Config, bundleRoot func(string) (string, error), diagnostic func(string, ...any)) (bool, error) {
	if cfg.AI == nil {
		return false, nil
	}
	ctx, releaseStore, err := storelock.WithStore(ctx, env.GetStorePath())
	if err != nil {
		return false, err
	}
	defer releaseStore()
	artifacts, err := planAI(cfg.AI, bundleRoot)
	if err != nil {
		return false, err
	}
	for name, entry := range cfg.ManagedConfigs {
		for _, file := range append([]string{name}, entry.OtherFileNameList...) {
			file = strings.ToLower(filepath.ToSlash(filepath.Clean(file)))
			for target, artifact := range artifacts {
				paths := []string{target}
				if artifact.files == nil {
					paths = append(paths, sidecar(target))
				}
				for _, path := range paths {
					lower := strings.ToLower(filepath.ToSlash(path))
					if file == lower || strings.HasPrefix(file, lower+"/") || strings.HasPrefix(lower, file+"/") {
						return false, fmt.Errorf("managed config %q overlaps AI artifact %q", name, path)
					}
				}
			}
		}
	}
	release, err := lockDatamitsuDir(gitRoot)
	if err != nil {
		return false, err
	}
	defer release()
	return applyAI(ctx, gitRoot, artifacts, func() (bool, error) {
		if len(artifacts) == 0 {
			return false, nil
		}
		return ensureAIExclude(ctx, gitRoot)
	}, diagnostic)
}
