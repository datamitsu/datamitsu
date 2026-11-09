package parsermanager

import (
	"context"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/provision"
)

// EnsureRequest carries parser declarations to the store-owning worker.
type EnsureRequest struct {
	StoreRoot string              `json:"storeRoot"`
	CacheRoot string              `json:"cacheRoot"`
	Parsers   config.MapOfParsers `json:"parsers"`
	Names     []string            `json:"names"`
}

// EnsureResult contains verified module paths; compilation stays in the caller.
type EnsureResult struct {
	Paths map[string]string `json:"paths"`
}

// EnsureModules acquires verified module files without compiling or executing them.
func (m *Manager) EnsureModules(ctx context.Context, names []string) (EnsureResult, error) {
	result := EnsureResult{Paths: make(map[string]string, len(names))}
	for _, name := range names {
		path, err := m.ensureModule(ctx, name)
		if err != nil {
			return EnsureResult{}, err
		}
		result.Paths[name] = path
	}
	return result, nil
}

func (m *Manager) remoteModule(ctx context.Context, name string) (string, bool, error) {
	if err := env.RejectInstallChild("ensure parser"); err != nil {
		return "", true, err
	}
	var result EnsureResult
	handled, err := provision.Call(ctx, "ensure-parsers", EnsureRequest{StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(), Parsers: m.parsers, Names: []string{name}}, &result)
	if !handled || err != nil {
		return "", handled, err
	}
	return result.Paths[name], true, nil
}
