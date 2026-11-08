package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ocibundle"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/provision"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/storelock"
	"github.com/datamitsu/datamitsu/internal/ui"
)

func runProvisionWorker() (resultErr error) {
	if len(os.Args) < 2 || os.Args[1] != "--internal-provision-worker" {
		return errors.New("invalid worker entrypoint")
	}
	rpc, liveness, cancelPipe, handshake, err := provision.OpenWorkerPipes(os.Args[2:])
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := rpc.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			resultErr = errors.Join(resultErr, fmt.Errorf("close worker RPC pipe: %w", closeErr))
		}
	}()
	defer func() {
		if closeErr := liveness.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			resultErr = errors.Join(resultErr, fmt.Errorf("close worker liveness pipe: %w", closeErr))
		}
	}()
	defer func() {
		if closeErr := cancelPipe.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			resultErr = errors.Join(resultErr, fmt.Errorf("close worker cancel pipe: %w", closeErr))
		}
	}()
	if err := provision.ReadStartupHandshake(handshake); err != nil {
		return err
	}
	if err := runtimeconfig.Init(); err != nil {
		return fmt.Errorf("initialize worker runtime settings: %w", err)
	}
	provision.Select(nil)
	ui.SetMuted(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, liveness)
		cancel()
	}()
	go func() {
		var b [1]byte
		for {
			if _, err := cancelPipe.Read(b[:]); err != nil {
				return
			}
			cancel()
		}
	}()
	serveErr := provision.ServeContext(ctx, rpc, handleProvisionOperation)
	if errors.Is(ctx.Err(), context.Canceled) && errors.Is(serveErr, context.Canceled) {
		return nil
	}
	return serveErr
}

func handleProvisionOperation(ctx context.Context, operation string, payload json.RawMessage) (any, error) {
	switch operation {
	case "install-apps", "install-runtimes", "generate-lock", "prepare-apps", "seed-oci", "ensure-parsers", "ensure-execution":
	default:
		return nil, fmt.Errorf("unknown provision operation %q", operation)
	}
	if operation == "install-apps" {
		var action struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(payload, &action); err != nil {
			return nil, fmt.Errorf("decode install-apps action: %w", err)
		}
		switch action.Action {
		case "install-concurrency", "install", "ensure-tools", "get-command-info", "get-command-info-install-only", "ensure-prepared", "install-bundle", "install-bundles", "install-runtime-app":
		default:
			return nil, fmt.Errorf("unknown install-apps action %q", action.Action)
		}
	}
	var roots struct {
		StoreRoot string `json:"storeRoot"`
		CacheRoot string `json:"cacheRoot"`
	}
	if err := json.Unmarshal(payload, &roots); err != nil {
		return nil, fmt.Errorf("decode %s roots: %w", operation, err)
	}
	if roots.StoreRoot == "" {
		return nil, errors.New("worker store root is required")
	}
	if err := checkWorkerRoots(roots.StoreRoot, roots.CacheRoot); err != nil {
		return nil, err
	}
	leaseCtx, releaseLease, err := storelock.WithStore(ctx, roots.StoreRoot)
	if err != nil {
		return nil, fmt.Errorf("acquire worker store lease: %w", err)
	}
	defer releaseLease()
	ctx = leaseCtx
	switch operation {
	case "install-apps":
		var req binmanager.ProvisionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode install-apps request: %w", err)
		}
		if err := checkWorkerRoots(req.StoreRoot, req.CacheRoot); err != nil {
			return nil, err
		}
		runtimes, err := decodeRuntimeDefinitions(req.RuntimeDefinitions)
		if err != nil {
			return nil, err
		}
		bm := binmanager.New(req.Apps, req.Bundles, runtimemanager.New(runtimes))
		switch req.Action {
		case "install-concurrency":
			return bm.InstallWithConcurrency(ctx, req.IncludeOptional, req.Concurrency, req.FailOnError)
		case "install":
			if err := bm.Install(ctx); err != nil {
				return nil, err
			}
			return struct{}{}, nil
		case "ensure-tools":
			if err := bm.EnsureTools(ctx, req.Names); err != nil {
				return nil, err
			}
			return struct{}{}, nil
		case "get-command-info":
			return bm.GetCommandInfo(ctx, req.AppName)
		case "get-command-info-install-only":
			return bm.GetCommandInfoInstallOnly(ctx, req.AppName)
		case "ensure-prepared":
			if err := bm.EnsurePrepared(ctx, req.AppName); err != nil {
				return nil, err
			}
			return struct{}{}, nil
		case "install-bundle":
			if len(req.Names) != 1 {
				return nil, errors.New("install-bundle requires one bundle name")
			}
			if err := bm.InstallBundleByName(ctx, req.Names[0]); err != nil {
				return nil, err
			}
			return struct{}{}, nil
		case "install-bundles":
			return bm.InstallBundles(ctx, req.IncludeOptional)
		case "install-runtime-app":
			app, ok := req.Apps[req.AppName]
			if !ok {
				return nil, fmt.Errorf("runtime app %q is missing from request", req.AppName)
			}
			rm := runtimemanager.New(runtimes)
			var installErr error
			switch {
			case app.Uv != nil:
				installErr = rm.InstallUVApp(ctx, req.AppName, app.Uv, app.Env, app.Files, app.Archives)
			case app.Node != nil:
				installErr = rm.InstallNodeApp(ctx, req.AppName, app.Node, app.Env, app.Files, app.Archives)
			case app.Bun != nil:
				installErr = rm.InstallBunApp(ctx, req.AppName, app.Bun, app.Env, app.Files, app.Archives)
			case app.Go != nil:
				installErr = rm.InstallGoApp(ctx, req.AppName, app.Go, app.Env, app.Files, app.Archives)
			case app.Jvm != nil:
				installErr = rm.InstallJVMApp(ctx, req.AppName, app.Jvm, app.Files, app.Archives)
			default:
				return nil, fmt.Errorf("app %q is not a runtime-managed app", req.AppName)
			}
			if installErr != nil {
				return nil, installErr
			}
			return struct{}{}, nil
		default:
			return nil, fmt.Errorf("unknown install-apps action %q", req.Action)
		}
	case "install-runtimes":
		var req runtimemanager.InstallRuntimesRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode install-runtimes request: %w", err)
		}
		if err := checkWorkerRoots(req.StoreRoot, req.CacheRoot); err != nil {
			return nil, err
		}
		return runtimemanager.New(req.Runtimes).InstallRuntimes(ctx, req.Names, req.Concurrency)
	case "generate-lock":
		var req runtimemanager.GenerateLockRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode generate-lock request: %w", err)
		}
		if err := checkWorkerRoots(req.StoreRoot, req.CacheRoot); err != nil {
			return nil, err
		}
		content, err := runtimemanager.New(req.Runtimes).GenerateLock(ctx, req.AppName, req.App)
		return runtimemanager.GenerateLockResult{Content: content}, err
	case "prepare-apps":
		var req binmanager.PrepareAppsRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode prepare-apps request: %w", err)
		}
		if err := checkWorkerRoots(req.StoreRoot, req.CacheRoot); err != nil {
			return nil, err
		}
		runtimes, err := decodeRuntimeDefinitions(req.RuntimeDefinitions)
		if err != nil {
			return nil, err
		}
		bm := binmanager.New(req.Apps, req.Bundles, runtimemanager.New(runtimes))
		for _, name := range req.Names {
			if err := bm.EnsurePrepared(ctx, name); err != nil {
				return nil, err
			}
		}
		return struct{}{}, nil
	case "seed-oci":
		var req ocibundle.SeedRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode seed-oci request: %w", err)
		}
		if err := checkWorkerRoots(req.StoreRoot, req.CacheRoot); err != nil {
			return nil, err
		}
		return ocibundle.ExecuteSeed(ctx, req), nil
	case "ensure-parsers":
		var req parsermanager.EnsureRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode ensure-parsers request: %w", err)
		}
		if err := checkWorkerRoots(req.StoreRoot, req.CacheRoot); err != nil {
			return nil, err
		}
		return parsermanager.New(req.Parsers).EnsureModules(ctx, req.Names)
	case "ensure-execution":
		var req runtimemanager.EnsureExecutionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode ensure-execution request: %w", err)
		}
		if err := checkWorkerRoots(req.StoreRoot, req.CacheRoot); err != nil {
			return nil, err
		}
		return runtimemanager.New(req.Runtimes).EnsureExecution(ctx, req.Name)
	default:
		return nil, fmt.Errorf("unknown provision operation %q", operation)
	}
}

func decodeRuntimeDefinitions(raw json.RawMessage) (config.MapOfRuntimes, error) {
	runtimes := config.MapOfRuntimes{}
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &runtimes); err != nil {
			return nil, fmt.Errorf("decode runtime definitions: %w", err)
		}
	}
	return runtimes, nil
}

func checkWorkerRoots(storeRoot, cacheRoot string) error {
	if storeRoot != "" && storeRoot != env.GetStorePath() {
		return errors.New("worker store root mismatch")
	}
	if cacheRoot != "" && cacheRoot != env.GetCachePath() {
		return errors.New("worker cache root mismatch")
	}
	return nil
}
