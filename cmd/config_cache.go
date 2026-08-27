package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/configcache"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/facts"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
	"github.com/datamitsu/datamitsu/internal/trace"

	"go.uber.org/zap"
)

var (
	configCacheHits   = trace.NewCounter("config.cache.hit")
	configCacheMisses = trace.NewCounter("config.cache.miss")
)

// configCache is one load's handle on the config-evaluation cache. A nil
// *configCache means this load does not use the cache at all, so every call
// site is a plain method call rather than a chain of enabled-checks.
type configCache struct {
	store *configcache.Store
	key   string
	// chain is the content snapshot the key was computed from. The artifact is
	// written only if the chain still hashes to exactly this after the
	// evaluation, so a file edited mid-evaluation is stale rather than stamped
	// fresh.
	chain []configcache.ChainFile
}

// configCacheParams is everything newConfigCache needs to decide whether this
// load is cacheable and, if so, what its key is.
type configCacheParams struct {
	sources []configSource
	// explicitChain is the chain the user named on the command line
	// (--before-config then --config). It identifies the namespace of a load
	// that has no git root to be identified by.
	explicitChain []string
	noAutoConfig  bool
	gitRoot       string
	cwd           string
	// prior is the content snapshot taken BEFORE any config file was read,
	// keyed by absolute path. It holds the auto config alone — the only file
	// read that early. Every other chain file, flag-given or declared, is first
	// read during evaluation and is covered by the post-evaluation re-check.
	prior map[string]configcache.ChainFile
	opts  loadConfigOptions
}

// configCacheUsable reports whether a load with these options may consult the
// cache at all.
//
//   - requireVM: only loadConfigForSetup uses the returned *goja.Runtime, and a
//     hit has no VM to return.
//   - evaluateSetupContent: the layer map is built from live goja functions,
//     which an artifact cannot hold and must never fake.
//   - skipLockfileValidation: this load validates less than every other one. An
//     artifact it wrote would let a later, strict load skip the error it exists
//     to raise — the one direction of this cache that could turn a refusal into
//     a silent success.
func configCacheUsable(opts loadConfigOptions) bool {
	if opts.requireVM || opts.evaluateSetupContent || opts.skipLockfileValidation {
		return false
	}
	return effectiveRuntimeConfig().ConfigCache
}

// effectiveRuntimeConfig returns the runtime config, computing it fresh when
// Init has not run (unit tests calling into the loader directly).
func effectiveRuntimeConfig() runtimeconfig.Effective {
	eff, err := runtimeconfig.Get()
	if err != nil {
		return runtimeconfig.Compute()
	}
	return eff
}

// newConfigCache computes this load's key and returns the handle, or nil when
// the load must not touch the cache.
//
// It runs after the chain is resolved because the declared before-configs are
// part of the key and are only known once the auto config has been read for
// them — and it re-checks that resolution against the earlier snapshot, so a
// file that changed while the chain was being resolved disables the cache
// instead of producing a key for bytes nobody evaluated.
func newConfigCache(ctx context.Context, p configCacheParams) *configCache {
	if !configCacheUsable(p.opts) {
		return nil
	}
	namespace, err := configCacheNamespace(p.gitRoot, p.explicitChain)
	if err != nil {
		logger.Logger.Debug("config evaluation cache disabled for this chain", zap.Error(err))
		return nil
	}
	store, err := configcache.NewStore(namespace)
	if err != nil {
		logger.Logger.Debug("config evaluation cache disabled for this chain", zap.Error(err))
		return nil
	}

	keySpan := trace.Start(trace.CatConfig, "configcache.key")
	inputs, chain, err := configCacheInputs(ctx, p)
	keySpan.EndWith(trace.A("files", len(chain)))
	if err != nil {
		logger.Logger.Debug("config evaluation cache disabled: cannot compute the key", zap.Error(err))
		return nil
	}
	if !chainMatches(p.prior, chain) {
		logger.Logger.Debug("a config file changed while the chain was being resolved; not using the config evaluation cache")
		return nil
	}
	return &configCache{store: store, key: configcache.Key(inputs), chain: chain}
}

// configCacheNamespace mirrors the source-farm layout: a repository chain is
// namespaced by its git root, a machine-level --config chain by the chain
// itself. There is deliberately no fall back to cwd — two directories sharing a
// namespace is how a cache serves another directory's config — so a load with
// neither a git root nor an explicit chain simply does not cache.
func configCacheNamespace(gitRoot string, explicitChain []string) (string, error) {
	if gitRoot != "" {
		return configcache.ProjectNamespace(gitRoot)
	}
	return configcache.ChainNamespace(explicitChain)
}

// configCacheInputs collects everything the evaluated config is a function of,
// and returns the chain snapshot separately so the write path can re-verify it.
func configCacheInputs(ctx context.Context, p configCacheParams) (configcache.Inputs, []configcache.ChainFile, error) {
	f, factsGitRoot, err := facts.CollectWithOptions(ctx, BinaryCommandOverride,
		facts.CollectOptions{TolerateGitFailure: p.opts.tolerateGitRootFailure})
	if err != nil {
		return configcache.Inputs{}, nil, err
	}

	// Under --no-auto-config the loader never resolves a git root, but every
	// engine still does: computeRootPath makes it the base of tools.path.rel, so
	// it is JS-visible whether or not discovery ran. Falling back to the root
	// facts collection just resolved keeps it in the key — the discovery-only
	// inputs below stay on p.gitRoot, since nothing was discovered.
	gitRoot := p.gitRoot
	if gitRoot == "" {
		gitRoot = factsGitRoot
	}

	chain := hashConfigChain(p.sources)
	return configcache.Inputs{
		FormatVersion:        configcache.FormatVersion,
		Version:              ldflags.Version,
		BinaryIdentity:       binaryIdentity(),
		ChainFiles:           chain,
		NoAutoConfig:         p.noAutoConfig,
		AutoConfigCandidates: autoConfigCandidates(p.gitRoot),
		SkipRemoteConfig:     SkipRemoteConfig,
		Environ:              env.EnvironAll(),
		ConfigInputs: configcache.ConfigInputs{
			MinimumReleaseAgeMinutes: effectiveRuntimeConfig().MinimumReleaseAgeMinutes,
		},
		Facts:        configcache.FactsFrom(f),
		CWD:          p.cwd,
		GitRoot:      gitRoot,
		GitHead:      gitHeadContent(gitRoot),
		ColorEnabled: color.LibraryEnabled(),
	}, chain, nil
}

// binaryIdentity distinguishes two builds that report the same ldflags.Version,
// which every local build does — they all say "dev". Two things in the binary
// decide what an evaluation produces: the embedded default config, which is the
// head of every chain, and the Go merge and validation logic that runs after
// the JS. The first is hashed directly. The second cannot be hashed cheaply —
// the binary is tens of megabytes — so the executable's size and modification
// time stand in for it: a rebuild moves both, and neither costs more than a
// stat.
//
// A step that fails degrades to a literal naming the failure rather than to
// nothing, so a key computed without it is distinguishable from one computed
// with it.
var binaryIdentity = sync.OnceValue(func() string {
	parts := make([][]byte, 0, 4)

	defaultConfig, err := config.GetDefaultConfig()
	if err != nil {
		parts = append(parts, []byte("defaultConfig"), fmt.Appendf(nil, "unreadable\x1f%v", err))
	} else {
		parts = append(parts, []byte("defaultConfig"), []byte(hashutil.XXH3Hex([]byte(defaultConfig))))
	}

	parts = append(parts, []byte("executable"), []byte(executableStamp()))
	return hashutil.XXH3Multi(parts...)
})

// executableStamp is the running executable's size and modification time, the
// cheap stand-in for its content.
func executableStamp() string {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Sprintf("unknown\x1f%v", err)
	}
	info, err := os.Stat(exe)
	if err != nil {
		return fmt.Sprintf("stat failed\x1f%v", err)
	}
	return fmt.Sprintf("%d\x1f%d", info.Size(), info.ModTime().UnixNano())
}

// hashConfigChain hashes every on-disk file of the chain, in chain order. The
// default (embedded) source and remote configs have no path: the former is part
// of the binary, which ldflags.Version already covers, and the latter are
// content-addressed by the hash their parent declared.
func hashConfigChain(sources []configSource) []configcache.ChainFile {
	files := make([]configcache.ChainFile, 0, len(sources))
	for _, s := range sources {
		if s.path == "" {
			continue
		}
		files = append(files, configcache.HashChainFile(absOrSelf(s.path)))
	}
	return files
}

// hashConfigPaths hashes a set of paths into a lookup keyed by path, for the
// pre-read snapshot.
func hashConfigPaths(paths []string) map[string]configcache.ChainFile {
	out := make(map[string]configcache.ChainFile, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		abs := absOrSelf(p)
		if _, seen := out[abs]; seen {
			continue
		}
		out[abs] = configcache.HashChainFile(abs)
	}
	return out
}

// chainMatches reports whether every file the snapshot covers still hashes to
// what the snapshot recorded. Files the snapshot does not cover are ignored:
// they were discovered later and have no earlier state to disagree with.
func chainMatches(prior map[string]configcache.ChainFile, chain []configcache.ChainFile) bool {
	for _, f := range chain {
		was, ok := prior[f.Path]
		if !ok {
			continue
		}
		if was.Exists != f.Exists || was.ContentHash != f.ContentHash {
			return false
		}
	}
	return true
}

// autoConfigCandidates records every file name config discovery stats at the
// git root, chosen or not: discovery refuses to load when two of them exist, so
// a tree that gains a second candidate stops being loadable while every other
// input is unchanged.
func autoConfigCandidates(gitRoot string) []configcache.AutoConfigCandidate {
	if gitRoot == "" {
		return nil
	}
	out := make([]configcache.AutoConfigCandidate, 0, len(sourcefarm.AutoConfigNames))
	for _, name := range sourcefarm.AutoConfigNames {
		p := filepath.Join(gitRoot, name)
		_, err := os.Stat(p)
		out = append(out, configcache.AutoConfigCandidate{Path: p, Exists: err == nil})
	}
	return out
}

// gitHeadContent returns the resolved HEAD, which a branch switch rewrites. A
// branch can add, delete or change chain files, and it can do so without
// changing anything else in the key.
func gitHeadContent(gitRoot string) string {
	if gitRoot == "" {
		return ""
	}
	data, err := os.ReadFile(sourcefarm.GitHeadPath(gitRoot))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// load returns the cached entry for this load's key.
func (c *configCache) load() (*configcache.Entry, bool) {
	if c == nil {
		return nil, false
	}
	readSpan := trace.Start(trace.CatConfig, "configcache.read")
	entry, ok := c.store.Load(c.key)
	readSpan.EndWith(trace.A("key", c.key), trace.A("hit", ok))
	if ok {
		configCacheHits.Add(1)
	} else {
		configCacheMisses.Add(1)
	}
	return entry, ok
}

// save stores what the evaluation produced, unless something makes this result
// unrepresentative of the key: a config that read a clock or Math.random, or a
// chain file that moved while the chain was being evaluated. Every refusal is a
// debug line and never an error — the command already has its config.
//
// The verdict arrives as this load's own observations rather than through the
// package-global configEvalCacheable: two loads in one process (the LSP server
// evaluates a config per request) would otherwise let one load's publish() hand
// the other a verdict for a chain it never ran.
func (c *configCache) save(obs *chainObservations, entry *configcache.Entry) {
	if c == nil || entry == nil || entry.Config == nil {
		return
	}
	if obs == nil || obs.nonDeterminism != "" {
		// publish() already named the source at debug level.
		return
	}
	for _, was := range c.chain {
		if now := configcache.HashChainFile(was.Path); now.Exists != was.Exists || now.ContentHash != was.ContentHash {
			logger.Logger.Debug("a config file changed while the chain was being evaluated; not storing the result",
				zap.String("path", was.Path))
			return
		}
	}

	writeSpan := trace.Start(trace.CatConfig, "configcache.write")
	err := c.store.Save(c.key, entry)
	writeSpan.EndWith(trace.A("key", c.key))
	if err != nil {
		logger.Logger.Debug("failed to store the evaluated config", zap.Error(err))
	}
}
