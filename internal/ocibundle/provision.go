package ocibundle

import (
	"context"
	"errors"
	"fmt"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/provision"
)

// SeedRequest describes a resolved registry or local-layout seed operation.
type SeedRequest struct {
	StoreRoot string            `json:"storeRoot"`
	CacheRoot string            `json:"cacheRoot"`
	Config    *config.Config    `json:"config"`
	Ref       *config.OCIRef    `json:"ref"`
	Options   Options           `json:"options"`
	Layout    string            `json:"layout"`
	Digest    string            `json:"digest"`
	Signer    *config.OCISigner `json:"signer"`
}

// SeedResult preserves seed error classification across the private transport.
type SeedResult struct {
	Changed    bool   `json:"changed"`
	Error      string `json:"error"`
	Fatal      bool   `json:"fatal"`
	NoPlatform bool   `json:"noPlatform"`
	Incomplete bool   `json:"incomplete"`
}

// ExecuteSeed runs only the resolved seed operation, without project evaluation.
func ExecuteSeed(ctx context.Context, request SeedRequest) SeedResult {
	var changed bool
	var err error
	if request.Layout != "" {
		changed, err = seedFromLayout(ctx, request.Config, request.Layout, request.Digest, request.Signer, request.Options)
	} else {
		changed, err = seedBundleChanged(ctx, request.Config, request.Ref, request.Options)
	}
	result := SeedResult{Changed: changed}
	if err != nil {
		result.Error = err.Error()
		result.Fatal = IsFatalSeedError(err)
		result.NoPlatform = errors.Is(err, ErrNoPlatformMatch)
		result.Incomplete = errors.Is(err, errIncomplete)
	}
	return result
}

func remoteSeed(ctx context.Context, request SeedRequest) (bool, bool, error) {
	if err := env.RejectInstallChild("seed OCI"); err != nil {
		return false, true, err
	}
	request.StoreRoot = env.GetStorePath()
	request.CacheRoot = env.GetCachePath()
	var result SeedResult
	handled, err := provision.Call(ctx, "seed-oci", request, &result)
	if !handled {
		return false, false, nil
	}
	if err != nil {
		return false, true, fmt.Errorf("%w: provisioning transport: %w", errIntegrity, err)
	}
	if result.Error == "" {
		return result.Changed, true, nil
	}
	err = errors.New(result.Error)
	switch {
	case result.Fatal:
		err = fmt.Errorf("%w: %s", errIntegrity, result.Error)
	case result.NoPlatform:
		err = fmt.Errorf("%w: %s", ErrNoPlatformMatch, result.Error)
	case result.Incomplete:
		err = fmt.Errorf("%w: %s", errIncomplete, result.Error)
	}
	return result.Changed, true, err
}
