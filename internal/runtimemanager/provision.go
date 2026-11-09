package runtimemanager

import (
	"context"
	"encoding/json"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/provision"
)

// InstallRuntimesRequest carries the resolved runtime definitions and selected
// names to the provisioning worker without reevaluating project configuration.
type InstallRuntimesRequest struct {
	StoreRoot   string               `json:"storeRoot"`
	CacheRoot   string               `json:"cacheRoot"`
	Runtimes    config.MapOfRuntimes `json:"runtimes"`
	Names       []string             `json:"names"`
	Concurrency int                  `json:"concurrency"`
}

func (rm *RuntimeManager) dispatchAppInstall(ctx context.Context, name string, app binmanager.App) (bool, error) {
	if err := env.RejectInstallChild("runtime app installation"); err != nil {
		return false, err
	}
	if !provision.RemoteSelected() {
		return false, nil
	}
	request := binmanager.ProvisionRequest{
		StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(),
		Apps: binmanager.MapOfApps{name: app}, RuntimeDefinitions: rm.RuntimeDefinitions(),
		Action: "install-runtime-app", AppName: name,
	}
	return provision.Call(ctx, "install-apps", request, nil)
}

// RuntimeDefinitions returns the runtime map as JSON for cycle-free transport
// through binmanager's provisioning request.
func (rm *RuntimeManager) RuntimeDefinitions() json.RawMessage {
	data, err := json.Marshal(rm.mapOfRuntimes)
	if err != nil {
		return nil
	}
	return data
}

func (rm *RuntimeManager) dispatchInstallRuntimes(ctx context.Context, names []string, concurrency int) (bool, RuntimeInstallStats, error) {
	if err := env.RejectInstallChild("runtime installation"); err != nil {
		return false, RuntimeInstallStats{}, err
	}
	request := InstallRuntimesRequest{
		StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(),
		Runtimes: rm.mapOfRuntimes, Names: names, Concurrency: concurrency,
	}
	var result RuntimeInstallStats
	handled, err := provision.Call(ctx, "install-runtimes", request, &result)
	return handled, result, err
}
