package binmanager

import (
	"context"
	"encoding/json"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/provision"
)

// ProvisionRequest describes one app-install operation sent to the provisioning worker.
type ProvisionRequest struct {
	StoreRoot          string          `json:"storeRoot"`
	CacheRoot          string          `json:"cacheRoot"`
	Apps               MapOfApps       `json:"apps"`
	Bundles            MapOfBundles    `json:"bundles,omitempty"`
	RuntimeDefinitions json.RawMessage `json:"runtimeDefinitions,omitempty"`
	Action             string          `json:"action"`
	Names              []string        `json:"names,omitempty"`
	AppName            string          `json:"appName,omitempty"`
	IncludeOptional    bool            `json:"includeOptional,omitempty"`
	Concurrency        int             `json:"concurrency,omitempty"`
	FailOnError        bool            `json:"failOnError,omitempty"`
}

// PrepareAppsRequest describes prepared-app work sent to the provisioning worker.
type PrepareAppsRequest struct {
	StoreRoot          string          `json:"storeRoot"`
	CacheRoot          string          `json:"cacheRoot"`
	Apps               MapOfApps       `json:"apps"`
	Bundles            MapOfBundles    `json:"bundles,omitempty"`
	RuntimeDefinitions json.RawMessage `json:"runtimeDefinitions,omitempty"`
	Names              []string        `json:"names"`
}

func (bm *BinManager) callProvision(ctx context.Context, action string, names []string, appName string, includeOptional bool, concurrency int, failOnError bool, result any) (bool, error) {
	if err := env.RejectInstallChild("store provisioning"); err != nil {
		return false, err
	}
	if !provision.RemoteSelected() {
		return false, nil
	}
	var runtimes json.RawMessage
	if bm.runtimeManager != nil {
		runtimes = bm.runtimeManager.RuntimeDefinitions()
	}
	request := ProvisionRequest{
		StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(), Apps: bm.mapOfApps, Bundles: bm.mapOfBundles,
		RuntimeDefinitions: runtimes, Action: action, Names: names, AppName: appName,
		IncludeOptional: includeOptional, Concurrency: concurrency, FailOnError: failOnError,
	}
	return provision.Call(ctx, "install-apps", request, result)
}

func (bm *BinManager) callPrepareApps(ctx context.Context, names []string) (bool, error) {
	if err := env.RejectInstallChild("prepare apps"); err != nil {
		return false, err
	}
	if !provision.RemoteSelected() {
		return false, nil
	}
	var runtimes json.RawMessage
	if bm.runtimeManager != nil {
		runtimes = bm.runtimeManager.RuntimeDefinitions()
	}
	request := PrepareAppsRequest{
		StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(), Apps: bm.mapOfApps,
		Bundles: bm.mapOfBundles, RuntimeDefinitions: runtimes, Names: names,
	}
	return provision.Call(ctx, "prepare-apps", request, nil)
}
