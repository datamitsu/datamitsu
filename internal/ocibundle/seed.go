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
	"github.com/datamitsu/datamitsu/internal/installstate"
	"github.com/datamitsu/datamitsu/internal/ocidigest"
	"github.com/datamitsu/datamitsu/internal/ociref"
	"github.com/datamitsu/datamitsu/internal/prepare"
	"github.com/datamitsu/datamitsu/internal/storelock"
	"github.com/datamitsu/datamitsu/internal/target"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// errIntegrity marks seed failures that mean tampered or malformed bundle
// content (digest chain violation, allowlist violation, failed re-verify).
// They are always fatal — never downgraded to a warn-and-fall-through.
var errIntegrity = errors.New("bundle integrity violation")

// errIncomplete marks a seed that finished without covering everything the
// caller explicitly asked for. Deliberately NOT fatal: AutoSeed still degrades
// to the network path, and only a caller that set RequireRequested sees it.
var errIncomplete = errors.New("bundle does not cover the requested tools")

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
	Needed []string `json:"needed"`
	// NeededRuntimes adds standalone runtime targets (install --runtime) to
	// the demand-driven set.
	NeededRuntimes []string `json:"neededRuntimes"`
	// RequireRequested turns an unsatisfied request into an error instead of a
	// quietly partial seed. Set it for an explicit `store seed --apps`, where
	// the caller named the tools and a store that still cannot run them is a
	// failed command. AutoSeed leaves it off: there the bundle is an
	// accelerator, and whatever it does not cover the network path installs.
	RequireRequested bool `json:"requireRequested"`
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
func AutoSeed(ctx context.Context, cfg *config.Config, neededApps, neededRuntimes []string) (bool, error) {
	if cfg == nil || cfg.OCI == nil {
		return false, nil
	}
	if disabledByFlag.Load() || env.NoOCI() {
		log.Debug("oci bundle seeding disabled", zap.Bool("flag", disabledByFlag.Load()))
		return false, nil
	}
	if env.Offline() {
		log.Debug("offline mode: skipping oci bundle auto-seed (store must be pre-seeded)")
		return false, nil
	}
	if len(neededApps) == 0 && len(neededRuntimes) == 0 {
		return false, nil
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
	changed, err := seedBundleChanged(ctx, cfg, cfg.OCI, opts)
	switch {
	case err == nil:
		return changed, nil
	case IsFatalSeedError(err):
		return changed, err
	case errors.Is(err, ErrNoPlatformMatch):
		log.Warn("oci bundle has no entry for this platform; falling back to direct downloads", zap.Error(err))
		return changed, nil
	default:
		log.Warn("oci bundle seed failed; falling back to direct downloads", zap.Error(err))
		return changed, nil
	}
}

// SeedBundle materializes the requested bundle layers and surfaces all seed errors.
func SeedBundle(ctx context.Context, cfg *config.Config, ref *config.OCIRef, opts Options) error {
	_, err := seedBundleChanged(ctx, cfg, ref, opts)
	return err
}

func seedBundleChanged(ctx context.Context, cfg *config.Config, ref *config.OCIRef, opts Options) (bool, error) {
	if changed, handled, err := remoteSeed(ctx, SeedRequest{Config: cfg, Ref: ref, Options: opts}); handled {
		return changed, err
	}
	if err := config.ValidateOCI(ref); err != nil {
		return false, err
	}
	if ref == nil {
		return false, errors.New("no oci bundle declared in the effective config")
	}
	if err := httpx.GuardOffline("oci bundle seed of " + ref.Ref); err != nil {
		return false, err
	}
	host, repo, err := ociref.Parse(ref.Ref)
	if err != nil {
		return false, fmt.Errorf("oci ref %q %w", ref.Ref, err)
	}
	return seedFromChanged(ctx, cfg, newRegistrySource(host, repo), ref.Ref, ref.Digest, ref.Signer, opts)
}

// SeedFromLayout seeds the store from a local OCI image layout directory
// (`store import`) — fully offline transfer of a bundle.
func SeedFromLayout(ctx context.Context, cfg *config.Config, dir, digest string, signer *config.OCISigner, opts Options) error {
	_, err := seedFromLayout(ctx, cfg, dir, digest, signer, opts)
	return err
}

func seedFromLayout(ctx context.Context, cfg *config.Config, dir, digest string, signer *config.OCISigner, opts Options) (bool, error) {
	if changed, handled, err := remoteSeed(ctx, SeedRequest{Config: cfg, Layout: dir, Digest: digest, Signer: signer, Options: opts}); handled {
		return changed, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return false, fmt.Errorf("OCI layout directory: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("OCI layout path %q is not a directory", dir)
	}
	return seedFromChanged(ctx, cfg, newLayoutSource(dir), "oci-layout:"+dir, digest, signer, opts)
}

func seedFromChanged(ctx context.Context, cfg *config.Config, src blobSource, refLabel, digest string, signer *config.OCISigner, opts Options) (changed bool, retErr error) {
	storeRoot := env.GetStorePath()

	// Fast no-op without touching the network: demand-driven runs are
	// satisfied by the same per-subtree stat binmanager performs anyway; full
	// pulls by the seed marker written after the last layer landed.
	var expected map[string]string
	if opts.demandDriven() {
		expected = expectedSubtrees(cfg, storeRoot, opts.Needed, opts.NeededRuntimes)
		if len(expected) == 0 || allSubtreesPresent(storeRoot, expected) {
			return false, nil
		}
	} else if markerSatisfied(storeRoot, digest) {
		log.Debug("bundle already fully seeded", zap.String("digest", digest))
		return false, nil
	}

	if signer != nil {
		return false, fmt.Errorf("%w: oci.signer is set but signature verification is not implemented yet (planned phase 1b); remove signer or build with a datamitsu version that supports it", errIntegrity)
	}
	manifest, err := resolvePlatformManifest(ctx, src, digest)
	if err != nil {
		return false, err
	}
	builderRoot := manifest.Annotations[AnnotationStoreRoot]
	if builderRoot == "" || !path.IsAbs(builderRoot) {
		return false, fmt.Errorf("%w: bundle manifest is missing a valid %s annotation (got %q)", errIntegrity, AnnotationStoreRoot, builderRoot)
	}
	jobs, allSubtrees, err := classifyLayers(manifest, storeRoot, expected)
	if err != nil {
		return false, err
	}
	if len(jobs) > 0 {
		reVerify := buildReVerifyIndex(cfg, storeRoot)
		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(env.GetConcurrency())
		var completed atomic.Int64
		for _, job := range jobs {
			group.Go(func() error {
				placed, err := seedLayer(groupCtx, src, job, builderRoot, storeRoot, reVerify)
				if err != nil {
					return err
				}
				if placed {
					completed.Add(1)
				}
				return nil
			})
		}
		if err := group.Wait(); err != nil {
			return completed.Load() > 0, fmt.Errorf("seed layers: %w", err)
		}
		changed = completed.Load() > 0
	}
	if opts.RequireRequested {
		if err := verifyRequested(storeRoot, expected, manifest); err != nil {
			return changed, err
		}
	}
	if !opts.demandDriven() {
		if err := writeMarker(storeRoot, refLabel, digest, allSubtrees); err != nil {
			log.Warn("failed to write seed marker (pull succeeded; the next full pull re-checks layers)", zap.Error(err))
		}
	}
	return changed, nil
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
	seen := make(map[string]struct{}, len(manifest.Layers))
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
		if _, duplicate := seen[subtree]; duplicate {
			return nil, nil, fmt.Errorf("%w: duplicate layer target %q", errIntegrity, subtree)
		}
		seen[subtree] = struct{}{}
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
		if subtreeReady(storeRoot, subtree) {
			log.Debug("subtree already in store, skipping layer", zap.String("subtree", subtree))
			continue
		}
		jobs = append(jobs, layerJob{desc: desc, subtree: subtree, comp: comp})
	}
	for i, subtree := range allSubtrees {
		for _, other := range allSubtrees[i+1:] {
			a, b := strings.ToLower(subtree), strings.ToLower(other)
			if a == b || strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/") {
				return nil, nil, fmt.Errorf("%w: overlapping layer targets %q and %q", errIntegrity, subtree, other)
			}
		}
	}
	sort.Strings(allSubtrees)
	return jobs, allSubtrees, nil
}

func seedLayer(ctx context.Context, src blobSource, job layerJob, builderRoot, storeRoot string, reVerify map[string]reVerifySpec) (bool, error) {
	// A parser module the consumer's config does not declare has no hash to be
	// checked against, and a full seed (`store seed <ref>@<digest>` with no
	// --apps) queues every annotated layer the bundle carries. Placing it would
	// leave executable-by-the-parser-runtime bytes in the content-addressed
	// store that nothing ever verified, ready to be picked up by the plain
	// os.Stat fast path the day the config pins that very hash.
	//
	// Skip rather than fail: a shared organization bundle legitimately carries
	// the modules of several config variants, and "a partial bundle is a valid
	// partial seed" is a deliberate property of this seeder. Skipping costs the
	// consumer nothing — it cannot load a module its config does not declare.
	if _, ok := reVerify[job.subtree]; !ok && strings.HasPrefix(job.subtree, parsersSubtreeRoot) {
		log.Warn("skipping bundle parser layer that the config does not declare (nothing could verify it)",
			zap.String("subtree", job.subtree),
			zap.String("digest", job.desc.Digest.String()))
		return false, nil
	}

	if _, err := storelock.ValidateTarget(storeRoot, filepath.FromSlash(job.subtree)); err != nil {
		return false, fmt.Errorf("validate store subtree %q: %w", job.subtree, err)
	}
	storeAbs, err := filepath.Abs(storeRoot)
	if err != nil {
		return false, fmt.Errorf("absolutize store root: %w", err)
	}
	storeTarget := filepath.FromSlash(job.subtree)
	_, release, err := installstate.Acquire(ctx, storeAbs, storeTarget, "OCI layer "+job.subtree)
	if err != nil {
		return false, err
	}
	defer release()

	if subtreeReady(storeRoot, job.subtree) {
		return false, nil // another process seeded it while we waited for the lock
	}

	tmpRoot := filepath.Join(storeRoot, "tmp")
	blobPath, err := src.blob(ctx, job.desc, tmpRoot, "bundle "+path.Base(path.Dir(job.subtree)))
	if err != nil {
		return false, err
	}

	stagingDir, err := os.MkdirTemp(tmpRoot, "oci-seed-*")
	if err != nil {
		return false, fmt.Errorf("create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stagingDir) }()

	staged, err := extractLayerSubtree(blobPath, job.comp, layerExtractOptions{
		Subtree:           job.subtree,
		BuilderStoreRoot:  builderRoot,
		ConsumerStoreRoot: storeRoot,
	}, stagingDir)
	if err != nil {
		return false, fmt.Errorf("%w: layer %s (subtree %s): %w", errIntegrity, job.desc.Digest, job.subtree, err)
	}
	if strings.HasPrefix(job.subtree, ".apps/") && !appTreeReady(staged) {
		return false, fmt.Errorf("%w: staged app target %q has no valid readiness marker", errIntegrity, job.subtree)
	}
	if strings.HasPrefix(job.subtree, ".runtime-data/") && !pythonTreeReady(job.subtree, staged) {
		return false, fmt.Errorf("%w: staged Python target %q has no valid readiness record", errIntegrity, job.subtree)
	}
	if strings.HasPrefix(job.subtree, preparedSubtreeRoot) && !prepare.Ready(staged) {
		return false, fmt.Errorf("%w: staged prepared target %q is not ready", errIntegrity, job.subtree)
	}
	if spec, ok := reVerify[job.subtree]; ok {
		file := staged
		if spec.relPath != "" {
			file = filepath.Join(staged, filepath.FromSlash(spec.relPath))
		}
		if err := binmanager.VerifyFileHashPublic(file, spec.sha256); err != nil {
			return false, fmt.Errorf("%w: seeded content for %q does not match the published SHA-256 from the config: %w", errIntegrity, spec.owner, err)
		}
		log.Debug("re-verified seeded artifact against published hash",
			zap.String("subtree", job.subtree), zap.String("owner", spec.owner))
	}
	dest := filepath.Join(storeRoot, filepath.FromSlash(job.subtree))
	if err := placeSubtree(staged, dest, job.subtree, storeRoot); err != nil {
		return false, fmt.Errorf("place subtree %q: %w", job.subtree, err)
	}
	log.Info("seeded store subtree from oci bundle", zap.String("subtree", job.subtree))
	return true, nil
}

func placeSubtree(staged, dest, subtree, storeRoot string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create destination parent: %w", err)
	}
	if _, err := os.Lstat(dest); err == nil {
		if subtreeReady(storeRoot, subtree) {
			return removeStaged(staged)
		}
		if err := os.RemoveAll(dest); err != nil {
			return fmt.Errorf("remove incomplete destination: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect destination: %w", err)
	}
	if err := os.Rename(staged, dest); err != nil {
		if subtreeReady(storeRoot, subtree) {
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

// verifyRequested fails a seed that left an explicitly requested tool absent.
//
// Layers are matched on the app's full identity hash, so a config that computes
// a different hash than the one the bundle was built from matches nothing at
// all. Without this check that seed reports success and the store silently
// still has to install the tool — which surfaces much later, and under
// DATAMITSU_OFFLINE surfaces as a network error naming neither the bundle nor
// the mismatch. The bundle's app-level annotation is what makes the difference
// between the two causes visible: absent from the bundle, or present under
// another identity. It only ever sharpens the message — a layer whose hash
// disagrees is never accepted, since an app name identifies the tool, not the
// configured build of it.
func verifyRequested(storeRoot string, expected map[string]string, manifest *ocispec.Manifest) error {
	missing := make([]string, 0)
	for subtree, owner := range expected {
		if strings.HasPrefix(owner, parserOwnerPrefix) {
			continue
		}
		if !subtreeReady(storeRoot, subtree) {
			missing = append(missing, subtree)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)

	carried := bundleSubtreesByApp(manifest)
	details := make([]string, 0, len(missing))
	for _, subtree := range missing {
		// The bundle annotates a layer with the same name this path segment
		// carries, so the expected path yields the key to look up. Prepared
		// states compare only against prepared layers of the same app —
		// mixing roots would report an install subtree as "the state the
		// bundle carries", and the reverse.
		root := path.Dir(strings.TrimSuffix(subtree, "/"))
		if inBundle := carried[path.Base(path.Dir(subtree))]; len(inBundle) > 0 {
			if same := sameRootSubtrees(inBundle, root); len(same) > 0 {
				details = append(details, fmt.Sprintf(
					"%s: expected %s, but the bundle carries %s — this config differs from the one the bundle was built from",
					expected[subtree], subtree, strings.Join(same, ", ")))
				continue
			}
		}
		details = append(details, fmt.Sprintf(
			"%s: expected %s, which the bundle does not carry", expected[subtree], subtree))
	}
	return fmt.Errorf("%w:\n  %s", errIncomplete, strings.Join(details, "\n  "))
}

// sameRootSubtrees filters a bundle's subtree list down to those sharing the
// expected subtree's store root (the first path segment: .bin, .prepared,
// .apps, …) — an app's install tree and its prepared state are different
// things, and a mismatch report must not conflate them.
func sameRootSubtrees(subtrees []string, subtreePath string) []string {
	root := ""
	if i := strings.Index(subtreePath, "/"); i >= 0 {
		root = subtreePath[:i+1]
	}
	var same []string
	for _, s := range subtrees {
		if root != "" && strings.HasPrefix(s, root) {
			same = append(same, s)
		}
	}
	return same
}

// bundleSubtreesByApp indexes the bundle's annotated layers by the app (or
// runtime) name they belong to, for diagnostics only.
func bundleSubtreesByApp(manifest *ocispec.Manifest) map[string][]string {
	byApp := make(map[string][]string)
	for _, desc := range manifest.Layers {
		app := desc.Annotations[AnnotationApp]
		subtree, ok := desc.Annotations[AnnotationSubtree]
		if app == "" || !ok {
			continue
		}
		byApp[app] = append(byApp[app], subtree)
	}
	for app := range byApp {
		sort.Strings(byApp[app])
	}
	return byApp
}

func allSubtreesPresent(storeRoot string, expected map[string]string) bool {
	for subtree := range expected {
		if !subtreeReady(storeRoot, subtree) {
			return false
		}
	}
	return true
}
