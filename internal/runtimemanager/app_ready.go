package runtimemanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/installstate"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

const (
	appReadyMarker        = ".datamitsu-app-ready"
	appReadyMarkerContent = "runtime-data-v1\n"
)

func appReady(path string) bool {
	content, err := os.ReadFile(filepath.Join(path, appReadyMarker))
	return err == nil && string(content) == appReadyMarkerContent
}

func markAppReady(path string) (retErr error) {
	file, err := os.CreateTemp(path, ".datamitsu-app-ready-*")
	if err != nil {
		return fmt.Errorf("create app readiness marker: %w", err)
	}
	tmp := file.Name()
	defer func() {
		if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			retErr = errors.Join(retErr, fmt.Errorf("close temporary app marker: %w", err))
		}
		if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary app marker: %w", err))
		}
	}()
	if _, err := file.WriteString(appReadyMarkerContent); err != nil {
		return fmt.Errorf("write app readiness marker: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close app readiness marker: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(path, appReadyMarker)); err != nil {
		return fmt.Errorf("publish app readiness marker: %w", err)
	}
	return nil
}

func lockAppTarget(ctx context.Context, appPath string) (context.Context, func(), error) {
	if err := env.RejectInstallChild("app installation"); err != nil {
		return nil, nil, err
	}
	storeRoot := env.GetStorePath()
	leaseCtx, releaseLease, err := storelock.WithStore(ctx, storeRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire store lease: %w", err)
	}
	relative, err := filepath.Rel(storeRoot, appPath)
	if err == nil {
		_, err = storelock.ValidateTarget(storeRoot, relative)
	}
	if err != nil {
		releaseLease()
		return nil, nil, fmt.Errorf("resolve app store target: %w", err)
	}
	leaseCtx, targetRelease, err := installstate.Acquire(leaseCtx, storeRoot, relative, "app "+filepath.Base(filepath.Dir(appPath)))
	if err != nil {
		releaseLease()
		return nil, nil, fmt.Errorf("lock app store target: %w", err)
	}
	return leaseCtx, func() { targetRelease(); releaseLease() }, nil
}

func withRuntimeStoreLease(ctx context.Context, label string, run func(context.Context) error) error {
	if err := env.RejectInstallChild("runtime app installation"); err != nil {
		return err
	}
	leaseCtx, release, err := storelock.WithStore(ctx, env.GetStorePath())
	if err != nil {
		return fmt.Errorf("acquire store lease for %s: %w", label, err)
	}
	defer release()
	return run(leaseCtx)
}
