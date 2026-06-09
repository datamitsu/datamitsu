package ocibundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/ocidigest"
	"github.com/datamitsu/datamitsu/internal/target"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// errIntegrity marks seed failures that mean tampered or malformed bundle
// content (digest chain violation, allowlist violation, failed re-verify).
// They are always fatal — never downgraded to a warn-and-fall-through.
var errIntegrity = errors.New("bundle integrity violation")

// IsFatalSeedError reports whether a seed failure must abort the operation
// (an attack or corruption indicator) rather than degrade to the network
// path. Everything else — transient network trouble, missing platform — is a
// degradation: the bundle is an accelerator, not a gate.
func IsFatalSeedError(err error) bool {
	return errors.Is(err, errIntegrity) || ocidigest.IsDigestMismatch(err)
}

// disabledByFlag is set from the --no-oci persistent CLI flag (the env twin
// is DATAMITSU_NO_OCI, checked independently).
var disabledByFlag atomic.Bool

// SetDisabledByFlag wires the --no-oci CLI flag into the auto-seed decision.
func SetDisabledByFlag(disabled bool) { disabledByFlag.Store(disabled) }

// Options parameterize a seed run.
type Options struct {
	// Needed limits the pull to the layers covering these tools plus their
	// transitive store dependencies (demand-driven). nil (together with a nil
	// NeededRuntimes) pulls the whole bundle (explicit `store seed` / airgap
	// seeding).
	Needed []string
	// NeededRuntimes adds standalone runtime targets (install --runtime) to
	// the demand-driven set.
	NeededRuntimes []string
}

// demandDriven reports whether the run is limited to a needed set.
func (o Options) demandDriven() bool {
	return o.Needed != nil || o.NeededRuntimes != nil
}

// AutoSeed is the §3 seam invoked before tool installation (runner, install,
// init). It is a no-op when no bundle is declared, when seeding is disabled
// (--no-oci / DATAMITSU_NO_OCI), or under DATAMITSU_OFFLINE (offline never
// auto-pulls — the store must be seeded explicitly while online). Degradable
// failures (no platform entry, network trouble) warn and fall through to the
// regular network path; integrity violations abort.
func AutoSeed(ctx context.Context, cfg *config.Config, neededApps, neededRuntimes []string) error {
	if cfg == nil || cfg.OCI == nil {
		return nil
	}
	if disabledByFlag.Load() || env.NoOCI() {
		log.Debug("oci bundle seeding disabled", zap.Bool("flag", disabledByFlag.Load()))
		return nil
	}
	if env.Offline() {
		log.Debug("offline mode: skipping oci bundle auto-seed (store must be pre-seeded)")
		return nil
	}
	if len(neededApps) == 0 && len(neededRuntimes) == 0 {
		return nil
	}

	// Auto-seed is always demand-driven; empty non-nil slices keep it so even
	// when only one of the two lists has entries.
	opts := Options{Needed: neededApps, NeededRuntimes: neededRuntimes}
	if opts.Needed == nil {
		opts.Needed = []string{}
	}
	if opts.NeededRuntimes == nil {
		opts.NeededRuntimes = []string{}
	}

	err := SeedBundle(ctx, cfg, cfg.OCI, opts)
	switch {
	case err == nil:
		return nil
	case IsFatalSeedError(err):
		return err
	case errors.Is(err, ErrNoPlatformMatch):
		log.Warn("oci bundle has no entry for this platform; falling back to direct downloads", zap.Error(err))
		return nil
	default:
		log.Warn("oci bundle seed failed; falling back to direct downloads", zap.Error(err))
		return nil
	}
}

// SeedBundle pulls (parts of) the declared bundle from its registry into the
// store. Explicit callers surface every error; AutoSeed downgrades the
// degradable ones.
func SeedBundle(ctx context.Context, cfg *config.Config, ref *config.OCIRef, opts Options) error {
	if err := config.ValidateOCI(ref); err != nil {
		return err
	}
	if ref == nil {
		return errors.New("no oci bundle declared in the effective config")
	}
	if err := httpx.GuardOffline("oci bundle seed of " + ref.Ref); err != nil {
		return err
	}
	host, repo, ok := strings.Cut(ref.Ref, "/")
	if !ok {
		return fmt.Errorf("oci ref %q has no repository path", ref.Ref)
	}
	return seedFrom(ctx, cfg, newRegistrySource(host, repo), ref.Ref, ref.Digest, ref.Signer, opts)
}

// SeedFromLayout seeds the store from a local OCI image layout directory
// (`store import`) — fully offline transfer of a bundle.
func SeedFromLayout(ctx context.Context, cfg *config.Config, dir, digest string, signer *config.OCISigner, opts Options) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("OCI layout directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("OCI layout path %q is not a directory", dir)
	}
	return seedFrom(ctx, cfg, newLayoutSource(dir), "oci-layout:"+dir, digest, signer, opts)
}

func seedFrom(ctx context.Context, cfg *config.Config, src blobSource, refLabel, digest string, signer *config.OCISigner, opts Options) error {
	storeRoot := env.GetStorePath()

	// Fast no-op without touching the network: demand-driven runs are
	// satisfied by the same per-subtree stat binmanager performs anyway; full
	// pulls by the seed marker written after the last layer landed.
	var expected map[string]string
	if opts.demandDriven() {
		expected = expectedSubtrees(cfg, storeRoot, opts.Needed, opts.NeededRuntimes)
		if len(expected) == 0 {
			return nil
		}
		if allSubtreesPresent(storeRoot, expected) {
			return nil
		}
	} else if markerExists(storeRoot, digest) {
		log.Debug("bundle already fully seeded", zap.String("digest", digest))
		return nil
	}

	if signer != nil {
		return fmt.Errorf("%w: oci.signer is set but signature verification is not implemented yet (planned phase 1b); remove signer or build with a datamitsu version that supports it", errIntegrity)
	}

	manifest, err := resolvePlatformManifest(ctx, src, digest)
	if err != nil {
		return err
	}

	builderRoot := manifest.Annotations[AnnotationStoreRoot]
	if builderRoot == "" || !path.IsAbs(builderRoot) {
		return fmt.Errorf("%w: bundle manifest is missing a valid %s annotation (got %q)", errIntegrity, AnnotationStoreRoot, builderRoot)
	}

	jobs, allSubtrees, err := classifyLayers(manifest, storeRoot, expected)
	if err != nil {
		return err
	}

	if len(jobs) > 0 {
		reVerify := buildReVerifyIndex(cfg, storeRoot)

		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(env.GetConcurrency())
		for _, job := range jobs {
			group.Go(func() error {
				return seedLayer(groupCtx, src, job, builderRoot, storeRoot, reVerify)
			})
		}
		if err := group.Wait(); err != nil {
			// No marker on partial failure; subtrees already laid out remain
			// a valid partial seed.
			return fmt.Errorf("seed layers: %w", err)
		}
	}

	if !opts.demandDriven() {
		if err := writeMarker(storeRoot, refLabel, digest, allSubtrees); err != nil {
			log.Warn("failed to write seed marker (pull succeeded; the next full pull re-checks layers)", zap.Error(err))
		}
	}
	return nil
}

// resolvePlatformManifest fetches the pinned manifest; when it is an index,
// the (os, arch, libc) child for the host is selected and fetched, every body
// verified against its digest.
func resolvePlatformManifest(ctx context.Context, src blobSource, digest string) (*ocispec.Manifest, error) {
	raw, err := src.manifest(ctx, digest)
	if err != nil {
		return nil, err
	}

	var probe struct {
		MediaType string               `json:"mediaType"`
		Manifests []ocispec.Descriptor `json:"manifests"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("%w: malformed bundle manifest %s: %w", errIntegrity, digest, err)
	}

	if isIndexMediaType(probe.MediaType) || len(probe.Manifests) > 0 {
		var idx ocispec.Index
		if err := json.Unmarshal(raw, &idx); err != nil {
			return nil, fmt.Errorf("%w: malformed bundle index %s: %w", errIntegrity, digest, err)
		}
		desc, err := selectDescriptor(idx, target.HostTarget())
		if err != nil {
			return nil, err
		}
		raw, err = src.manifest(ctx, desc.Digest.String())
		if err != nil {
			return nil, err
		}
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("%w: malformed bundle manifest: %w", errIntegrity, err)
	}
	return &manifest, nil
}

type layerJob struct {
	desc    ocispec.Descriptor
	subtree string
	comp    layerCompression
}

// classifyLayers applies the §2.12 contract: a layer WITHOUT the subtree
// annotation is a base layer of the runnable image — silently skipped; an
// annotated layer with a malformed subtree or an unknown media type is fatal;
// covered/unneeded subtrees are skipped. Returns the download queue and the
// full list of annotated subtrees (for the seed marker).
func classifyLayers(manifest *ocispec.Manifest, storeRoot string, expected map[string]string) ([]layerJob, []string, error) {
	var jobs []layerJob
	var allSubtrees []string
	for _, desc := range manifest.Layers {
		subtree, ok := desc.Annotations[AnnotationSubtree]
		if !ok {
			log.Debug("skipping layer without subtree annotation (base image layer)",
				zap.String("digest", desc.Digest.String()))
			continue
		}
		if err := validateSubtree(subtree); err != nil {
			return nil, nil, fmt.Errorf("%w: layer %s: %w", errIntegrity, desc.Digest, err)
		}
		comp, err := layerCompressionFor(desc.MediaType)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: layer %s (subtree %s): %w", errIntegrity, desc.Digest, subtree, err)
		}
		allSubtrees = append(allSubtrees, subtree)

		if expected != nil {
			if _, needed := expected[subtree]; !needed {
				continue
			}
		}
		if _, err := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(subtree))); err == nil {
			log.Debug("subtree already in store, skipping layer", zap.String("subtree", subtree))
			continue
		}
		jobs = append(jobs, layerJob{desc: desc, subtree: subtree, comp: comp})
	}
	sort.Strings(allSubtrees)
	return jobs, allSubtrees, nil
}

func seedLayer(ctx context.Context, src blobSource, job layerJob, builderRoot, storeRoot string, reVerify map[string]reVerifySpec) error {
	release, err := lockSubtree(storeRoot, job.subtree)
	if err != nil {
		return err
	}
	defer release()

	dest := filepath.Join(storeRoot, filepath.FromSlash(job.subtree))
	if _, err := os.Stat(dest); err == nil {
		return nil // another process seeded it while we waited for the lock
	}

	tmpRoot := filepath.Join(storeRoot, "tmp")
	blobPath, err := src.blob(ctx, job.desc, tmpRoot, "bundle "+path.Base(path.Dir(job.subtree)))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(blobPath) }()

	stagingDir, err := os.MkdirTemp(tmpRoot, "oci-seed-*")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stagingDir) }()

	staged, err := extractLayerSubtree(blobPath, job.comp, layerExtractOptions{
		Subtree:           job.subtree,
		BuilderStoreRoot:  builderRoot,
		ConsumerStoreRoot: storeRoot,
	}, stagingDir)
	if err != nil {
		return fmt.Errorf("%w: layer %s (subtree %s): %w", errIntegrity, job.desc.Digest, job.subtree, err)
	}

	if spec, ok := reVerify[job.subtree]; ok {
		file := staged
		if spec.relPath != "" {
			file = filepath.Join(staged, filepath.FromSlash(spec.relPath))
		}
		if err := binmanager.VerifyFileHashPublic(file, spec.sha256, binmanager.BinHashTypeSHA256); err != nil {
			return fmt.Errorf("%w: seeded content for %q does not match the published SHA-256 from the config: %w", errIntegrity, spec.owner, err)
		}
		log.Debug("re-verified seeded artifact against published hash",
			zap.String("subtree", job.subtree), zap.String("owner", spec.owner))
	}

	if err := placeSubtree(staged, dest); err != nil {
		return fmt.Errorf("place subtree %q: %w", job.subtree, err)
	}
	log.Info("seeded store subtree from oci bundle", zap.String("subtree", job.subtree))
	return nil
}

// placeSubtree atomically renames the staged subtree (file or directory) into
// the store. The staging area lives under {store}/tmp — same filesystem — so
// os.Rename is atomic; a concurrently appeared destination wins (content-
// addressed paths hold identical content).
func placeSubtree(staged, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create destination parent: %w", err)
	}
	if _, err := os.Stat(dest); err == nil {
		return removeStaged(staged)
	}
	if err := os.Rename(staged, dest); err != nil {
		if _, statErr := os.Stat(dest); statErr == nil {
			return removeStaged(staged)
		}
		return fmt.Errorf("rename staged subtree: %w", err)
	}
	return nil
}

// removeStaged drops a staged copy that lost the placement race (the
// destination is content-addressed, so the winner holds identical content).
func removeStaged(staged string) error {
	if err := os.RemoveAll(staged); err != nil {
		return fmt.Errorf("drop staged copy: %w", err)
	}
	return nil
}

func allSubtreesPresent(storeRoot string, expected map[string]string) bool {
	for subtree := range expected {
		if _, err := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(subtree))); err != nil {
			return false
		}
	}
	return true
}
