package runtimemanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/provision"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

// GenerateLockRequest carries resolved inputs without reevaluating project configuration.
type GenerateLockRequest struct {
	StoreRoot string               `json:"storeRoot"`
	CacheRoot string               `json:"cacheRoot"`
	AppName   string               `json:"appName"`
	App       binmanager.App       `json:"app"`
	Runtimes  config.MapOfRuntimes `json:"runtimes"`
}

// GenerateLockResult returns content before its private workspace is removed.
type GenerateLockResult struct {
	Content string `json:"content"`
}

type lockWorkspaceKey struct{}

func lockWorkspaceDestination(ctx context.Context, normal string) string {
	if destination, ok := ctx.Value(lockWorkspaceKey{}).(string); ok {
		return destination
	}
	return normal
}

// withLockWorkspace owns a disposable workspace for one lock generation. The
// callback must read its generated lock content before returning.
func withLockWorkspace(ctx context.Context, generate func(context.Context, string) (string, error)) (content string, err error) {
	ctx, release, err := storelock.WithStore(ctx, env.GetStorePath())
	if err != nil {
		return "", err
	}
	defer release()

	parent := filepath.Join(env.GetStorePath(), ".lockfiles")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create lockfile workspace parent: %w", err)
	}
	workspace, err := os.MkdirTemp(parent, "generate-")
	if err != nil {
		return "", fmt.Errorf("create lockfile workspace: %w", err)
	}
	defer func() { err = errors.Join(err, ForceRemoveAll(workspace)) }()

	return generate(context.WithValue(ctx, lockWorkspaceKey{}, workspace), workspace)
}

// GenerateLock resolves dependencies in a private workspace, never an installed app directory.
func (rm *RuntimeManager) GenerateLock(ctx context.Context, name string, app binmanager.App) (string, error) {
	if err := env.RejectInstallChild("generate lockfile"); err != nil {
		return "", err
	}
	var result GenerateLockResult
	if handled, callErr := provision.Call(ctx, "generate-lock", GenerateLockRequest{StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(), AppName: name, App: app, Runtimes: rm.mapOfRuntimes}, &result); handled {
		return result.Content, callErr
	}
	return withLockWorkspace(ctx, func(ctx context.Context, workspace string) (string, error) {
		var err error
		switch {
		case app.Node != nil:
			copiedConfig := *app.Node
			copiedConfig.LockFile = ""
			err = rm.InstallNodeApp(ctx, name, &copiedConfig, app.Env, app.Files, app.Archives)
		case app.Bun != nil:
			copiedConfig := *app.Bun
			copiedConfig.LockFile = ""
			err = rm.InstallBunApp(ctx, name, &copiedConfig, app.Env, app.Files, app.Archives)
		case app.Uv != nil:
			copiedConfig := *app.Uv
			copiedConfig.LockFile = ""
			err = rm.InstallUVApp(ctx, name, &copiedConfig, app.Env, app.Files, app.Archives)
		case app.Go != nil:
			effective, effectiveErr := runtimeconfig.Get()
			if effectiveErr != nil {
				return "", effectiveErr
			}
			copiedConfig := *app.Go
			copiedConfig.LockFile = ""
			if len(app.Files) > 0 || len(app.Archives) > 0 {
				err = rm.GenerateGoLockFilesFromSource(ctx, name, &copiedConfig, app.Files, app.Archives, workspace, effective.MinimumReleaseAgeMinutes)
			} else {
				err = rm.GenerateGoLockFiles(ctx, name, &copiedConfig, workspace, effective.MinimumReleaseAgeMinutes)
			}
		default:
			return "", fmt.Errorf("app %q does not support lockfile generation", name)
		}
		if err != nil {
			return "", err
		}
		if app.Go != nil {
			mod, readErr := os.ReadFile(filepath.Join(workspace, "go.mod"))
			if readErr != nil {
				return "", fmt.Errorf("read generated go.mod: %w", readErr)
			}
			sum, readErr := os.ReadFile(filepath.Join(workspace, "go.sum"))
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				return "", fmt.Errorf("read generated go.sum: %w", readErr)
			}
			return BuildGoLockFileJSON(string(mod), string(sum))
		}
		filename := "pnpm-lock.yaml"
		if app.Uv != nil {
			filename = "uv.lock"
		}
		data, err := os.ReadFile(filepath.Join(workspace, filename))
		if err != nil {
			return "", fmt.Errorf("read generated %s: %w", filename, err)
		}
		return string(data), nil
	})
}
