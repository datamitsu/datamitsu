package ocibundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/target"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// LayerStatus describes one annotated bundle layer and its store presence.
type LayerStatus struct {
	Subtree   string `json:"subtree"`
	Kind      string `json:"kind,omitempty"`
	App       string `json:"app,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	Present   bool   `json:"present"`
}

// AppCoverage describes whether a configured app is covered by the bundle.
type AppCoverage struct {
	App     string `json:"app"`
	Covered bool   `json:"covered"` // every expected subtree is in the bundle
	Present bool   `json:"present"` // every expected subtree is in the store
}

// Status is the `store status` report: what the bundle contains for this
// platform and which configured apps it covers vs which require the network.
type Status struct {
	Ref       string        `json:"ref"`
	Digest    string        `json:"digest"`
	Signed    bool          `json:"signed"`
	Platforms []string      `json:"platforms,omitempty"`
	Selected  string        `json:"selected,omitempty"`
	StoreRoot string        `json:"builderStoreRoot,omitempty"`
	Seeded    bool          `json:"fullySeeded"`
	Layers    []LayerStatus `json:"layers"`
	Apps      []AppCoverage `json:"apps"`
}

// BundleStatus inspects the declared bundle. It works offline when the
// manifest bodies are already in the on-disk cache; otherwise the registry is
// consulted (subject to the offline guard inside the transport path).
func BundleStatus(ctx context.Context, cfg *config.Config) (*Status, error) {
	if cfg == nil || cfg.OCI == nil {
		return nil, errors.New("no oci bundle declared in the effective config (set the top-level `oci` key)")
	}
	ref := cfg.OCI
	host, repo, ok := strings.Cut(ref.Ref, "/")
	if !ok {
		return nil, fmt.Errorf("oci ref %q has no repository path", ref.Ref)
	}
	src := newRegistrySource(host, repo)
	storeRoot := env.GetStorePath()

	status := &Status{
		Ref:    ref.Ref,
		Digest: ref.Digest,
		Signed: ref.Signer != nil,
		Seeded: markerExists(storeRoot, ref.Digest),
	}

	raw, err := src.manifest(ctx, ref.Digest)
	if err != nil {
		return nil, err
	}
	var probe struct {
		MediaType string               `json:"mediaType"`
		Manifests []ocispec.Descriptor `json:"manifests"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("malformed bundle manifest %s: %w", ref.Digest, err)
	}
	if isIndexMediaType(probe.MediaType) || len(probe.Manifests) > 0 {
		var idx ocispec.Index
		if err := json.Unmarshal(raw, &idx); err != nil {
			return nil, fmt.Errorf("malformed bundle index %s: %w", ref.Digest, err)
		}
		for _, desc := range idx.Manifests {
			if desc.Platform == nil || desc.Platform.OS == "unknown" || desc.Platform.Architecture == "unknown" {
				continue
			}
			status.Platforms = append(status.Platforms, describePlatform(desc))
		}
		sort.Strings(status.Platforms)

		desc, selErr := selectDescriptor(idx, target.HostTarget())
		if selErr != nil {
			//nolint:nilerr // a platform miss is a reportable state, not a failure: the
			// status (with the platform list and an empty Selected) IS the answer.
			return status, nil
		}
		status.Selected = describePlatform(desc)
		if raw, err = src.manifest(ctx, desc.Digest.String()); err != nil {
			return nil, err
		}
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("malformed bundle manifest: %w", err)
	}
	status.StoreRoot = manifest.Annotations[AnnotationStoreRoot]

	bundleSubtrees := make(map[string]bool)
	for _, desc := range manifest.Layers {
		subtree, ok := desc.Annotations[AnnotationSubtree]
		if !ok {
			continue
		}
		_, statErr := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(subtree)))
		bundleSubtrees[subtree] = true
		status.Layers = append(status.Layers, LayerStatus{
			Subtree:   subtree,
			Kind:      desc.Annotations[AnnotationKind],
			App:       desc.Annotations[AnnotationApp],
			SizeBytes: desc.Size,
			Present:   statErr == nil,
		})
	}
	sort.Slice(status.Layers, func(i, j int) bool { return status.Layers[i].Subtree < status.Layers[j].Subtree })

	appNames := make([]string, 0, len(cfg.Apps))
	for name, app := range cfg.Apps {
		if app.Shell != nil {
			continue
		}
		appNames = append(appNames, name)
	}
	sort.Strings(appNames)
	for _, name := range appNames {
		expected := expectedSubtrees(cfg, storeRoot, []string{name}, nil)
		if len(expected) == 0 {
			continue
		}
		coverage := AppCoverage{App: name, Covered: true, Present: true}
		for subtree := range expected {
			if !bundleSubtrees[subtree] {
				coverage.Covered = false
			}
			if _, err := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(subtree))); err != nil {
				coverage.Present = false
			}
		}
		status.Apps = append(status.Apps, coverage)
	}

	return status, nil
}
