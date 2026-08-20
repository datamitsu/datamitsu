package sourcefarm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/ldflags"
)

// The manifest is the farm's own description of itself, written beside the farm
// directory in the same atomic swap that creates it. It exists so a shim
// invocation can answer two questions — "is this farm still the right one for
// the tree as it is right now?" and "what does this name run?" — with a handful
// of lstat calls and a hash comparison, instead of a config load. That gap is
// the whole feature: a config load costs tens of milliseconds, and paying it per
// tool invocation would make an activated shell noticeably slower than a
// shell without source mode.
//
// The format is JSON with a typed struct, not a packed binary encoding.
// Decoding a 100-app table was measured at ~311 µs against a ~10 ms shim
// process; a hand-rolled codec would buy a fraction of a millisecond and cost a
// format version to migrate plus test data that cannot be golden-diffed.
//
// # Freshness
//
// Freshness is decided by comparing a recorded watch set — one {path, mtime,
// size, exists} tuple per file that can change the resolved toolchain — against
// the same files as they are now, with != rather than a > watermark. A
// watermark misses the two transitions a branch switch actually produces: an
// mtime that moves backwards, and a file that appears or disappears. .git/HEAD
// is in the set for the same reason: a branch that deletes datamitsu.config.ts
// leaves no mtime to compare on a file that no longer exists, and HEAD is
// rewritten by checkout but not by commit, so it invalidates exactly when a
// checkout happens and not on every local edit.
//
// # Known hole: config JS reading non-DATAMITSU_ environment variables
//
// The staleness key covers datamitsu's own environment variables, but config JS
// receives the entire environment via facts().env (internal/facts/facts.go),
// and this repository's own datamitsu.config.ts already branches on
// facts().env.DATAMITSU_BENCH. A config that branches on a variable outside the
// DATAMITSU_ namespace — CI, a cloud profile, a custom flag — changes its output
// without changing the key, and the farm will not rebake.
//
// `datamitsu source refresh --force` is the escape hatch. Closing the hole
// soundly means instrumenting goja to record which environment keys the VM
// actually read (plus a fallback flag for configs that enumerate the whole
// object with Object.keys), which is deliberately deferred to its own plan.

// ManifestFormatVersion is the on-disk format version. A manifest carrying any
// other value is reported stale rather than rejected: an old datamitsu meeting a
// newer farm must rebake, never error.
//
// Version 2 added Entry.RequiredPaths. The bump is what retires manifests baked
// before it: a format-1 entry decodes with RequiredPaths == nil, which the shim
// reads as "nothing else is required" and falls back to the single-path check
// the field exists to replace. Nothing else in the manifest distinguishes the
// two — the watch set does not cover the store, and DatamitsuVersion is "dev"
// for every local build — so a stale format-1 farm would otherwise keep exec'ing
// partially installed runtime-managed apps until an unrelated edit invalidated
// it. Any future field the shim's correctness depends on needs the same bump.
//
// Version 3 added Manifest.Origin and Manifest.ConfigPaths, and with them a
// second farm namespace. The bump retires the one manifest a format-2 build
// could write that this build can no longer refresh in place: a git-root farm
// baked from an explicit --config recorded those flags in ConfigArgs, and
// replaying them now resolves an explicit-config target, so the rebake writes a
// farm under {cache}/configs/ and leaves the git-root manifest exactly as stale
// as it was. Without the bump that manifest is still readable and still
// UsableStale, so the shim would serve its entries back after every failed-to-
// land rebake — silently, forever, from a farm nothing can update.
const ManifestFormatVersion = 3

// Origin records how the farm's root was established.
type Origin string

// OriginGitRoot is the origin of a project farm: the root came from git
// discovery and a config file found there. The shim re-resolves such a farm from
// cwd's git root, which is what makes branch switching transparent.
const OriginGitRoot Origin = "git-root"

// OriginExplicitConfig is the origin of a farm baked from a config chain the
// user named with --config. There is no git root behind it: the identity is a
// fingerprint of the resolved config paths (env.ConfigFarmIdentity), the watch
// set is that chain and nothing else, and the shim revalidates only what the
// manifest records — it never walks up for .git.
//
// That last property is the trust boundary. A shell activated against a
// machine-level config that cd's into an untrusted clone keeps running the
// machine-level tools; it does not start evaluating the clone's JavaScript
// because a tool name was typed. Entering a repository's toolchain stays an
// explicit `datamitsu source` in that repository.
const OriginExplicitConfig Origin = "explicit-config"

// WatchFile is one file whose state can change the resolved toolchain.
//
// Exists is recorded explicitly because absence is a meaningful state: a config
// file that a branch does not have must compare unequal to the same path on a
// branch that does. MtimeNS and Size of a missing file are zero.
type WatchFile struct {
	Path    string `json:"path"`
	MtimeNS int64  `json:"mtimeNs"`
	Size    int64  `json:"size"`
	Exists  bool   `json:"exists"`
}

// Manifest is the on-disk description of a materialized farm.
type Manifest struct {
	// FormatVersion is ManifestFormatVersion at write time.
	FormatVersion int `json:"formatVersion"`

	// Origin is how the root was established.
	Origin Origin `json:"origin"`

	// Root is the *authoritative* git root — the one the config loader resolved,
	// not the one a cheap upward walk for .git would find. The two disagree
	// inside a submodule, where root discovery climbs to the topmost
	// superproject. The shim's walk selects which manifest to open; this field
	// decides what the root actually is.
	//
	// Empty for an OriginExplicitConfig farm, which has no git root. Nothing
	// synthetic is put here: a fabricated root would be indistinguishable from a
	// real one to every later reader, and the shim's origin branch would be
	// deciding on a lie. ConfigPaths carries that farm's identity instead.
	Root string `json:"root"`

	// ConfigPaths is the resolved, absolute, ordered config chain an
	// OriginExplicitConfig farm was baked from — the exact input its identity
	// (env.ConfigFarmIdentity) is computed over. Empty for a git-root farm.
	//
	// Order is significant and duplicates are preserved, because the chain is a
	// merge order: two chains over the same files in a different order resolve
	// to different configs and are therefore different farms.
	//
	// Deliberately *not* folded into the staleness key: every path here is also a
	// watch-set entry, so the key already changes when the chain does, and the
	// key is recomputed from the manifest's own recorded fields — adding
	// ConfigPaths would compare it against itself.
	ConfigPaths []string `json:"configPaths,omitempty"`

	// FarmDir is the directory the entries live in.
	FarmDir string `json:"farmDir"`

	// DatamitsuVersion is the version that baked the farm. A datamitsu upgrade
	// invalidates every farm: recorded store paths and resolution rules are
	// version-dependent.
	DatamitsuVersion string `json:"datamitsuVersion"`

	OS   string `json:"os"`
	Arch string `json:"arch"`

	// ShimTarget is the datamitsu executable every shim entry links to, as
	// resolveShimTarget resolved it at bake time. Materialization fills it in;
	// callers building a manifest leave it empty.
	//
	// It is recorded because moving the datamitsu binary — a `mv`, a package
	// manager relocating it, a `go build -o` to a new path — turns every entry in
	// every farm on the machine into a dangling symlink while changing nothing the
	// watch set or the version string can see. The farm would report fresh
	// forever, `source refresh` would answer "already up to date", and the shim
	// could not repair it because the kernel fails the exec before any datamitsu
	// code runs. Validate stats this path so the states that *can* still run —
	// `source status`, `source refresh`, a fresh activation — see the farm as
	// stale and rebake it against the executable's new location.
	//
	// Deliberately *not* part of the staleness key: the key is a recorded value
	// compared against a recomputation over recorded inputs, so folding the path
	// in would compare it against itself. Only stat'ing it answers the question.
	ShimTarget string `json:"shimTarget,omitempty"`

	// StalenessKey is the XXH3-128 fingerprint described on ComputeStalenessKey.
	// It is an internal fingerprint, never compared against a value that came
	// over the network, so a cryptographic hash would only cost cycles.
	StalenessKey string `json:"stalenessKey"`

	// Watch is the ordered watch set, sorted by Path.
	Watch []WatchFile `json:"watch"`

	// ConfigArgs are the global flags that selected the config chain this farm
	// was baked from, with every path made absolute — `--before-config
	// /abs/shared.js`, `--config /abs/x.ts`, `--no-auto-config`. Empty for the
	// ordinary case of auto-discovery at the git root.
	//
	// It exists so the shim can re-bake the *same* farm. A wrapper invokes
	// datamitsu with `--before-config <shared config>`, so a farm baked through
	// one lists apps that only that chain declares; the shim's rebake spawns the
	// resolved real datamitsu binary, deliberately bypassing the wrapper, and
	// without these flags it would re-resolve a chain that never saw them. Every
	// wrapper-provided app would vanish from the farm on the next branch switch,
	// and the next invocation of those names would fall through PATH to the
	// system binary — the silent wrong-binary failure the farm exists to prevent,
	// arriving through the rebake door.
	//
	// Deliberately *not* part of the staleness key, because folding it in would
	// not answer the question it looks like it answers: Validate recomputes the
	// key from the manifest's own recorded fields, so a recorded ConfigArgs would
	// only ever be compared against itself. Whether a farm was baked from the
	// chain the *caller* selected is a comparison against the caller's flags, and
	// it lives where those flags are known — cmd.manifestChainMatches, which
	// every reader of an existing manifest (activation, status, refresh) goes
	// through. It matters in both directions: a flagged invocation must not be
	// answered by a plain farm, and a plain invocation must not be answered by a
	// flagged one, which is the silent half — a flagged bake overwrites the
	// root's farm and leaves a watch set that compares equal forever.
	ConfigArgs []string `json:"configArgs,omitempty"`

	// Entries, Excluded and Shadowed mirror the Plan the farm was baked from.
	// Excluded is carried so `source status` can explain a name's absence
	// without reloading the config.
	Entries  []Entry    `json:"entries"`
	Excluded []Excluded `json:"excluded,omitempty"`
	Shadowed []Shadow   `json:"shadowed,omitempty"`
}

// lstat is a package variable so tests can count filesystem calls and prove
// that Validate does nothing but stat.
var lstat = os.Lstat

// StatWatchFile records the current state of one watched path. A path that does
// not exist — or that cannot be stat'ed for any other reason — is recorded as a
// well-formed absent entry rather than an error, because absence is a normal
// state a branch switch produces.
func StatWatchFile(path string) WatchFile {
	info, err := lstat(path)
	if err != nil {
		return WatchFile{Path: path, Exists: false}
	}
	return WatchFile{
		Path:    path,
		MtimeNS: info.ModTime().UnixNano(),
		Size:    info.Size(),
		Exists:  true,
	}
}

// WatchSet stats every path, returning tuples sorted by path with duplicates
// removed. Sorting and deduplication are what make the staleness key
// independent of the order the caller happened to discover the files in.
func WatchSet(paths []string) []WatchFile {
	unique := make(map[string]struct{}, len(paths))
	ordered := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, seen := unique[p]; seen {
			continue
		}
		unique[p] = struct{}{}
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)

	out := make([]WatchFile, 0, len(ordered))
	for _, p := range ordered {
		out = append(out, StatWatchFile(p))
	}
	return out
}

// WatchPaths returns the paths that must be watched for a farm rooted at root,
// given the config chain the loader resolved.
//
// Beyond the config files themselves it adds:
//
//   - .git/HEAD, which is what makes a branch switch detectable when the switch
//     deletes or adds a config file rather than modifying one;
//   - pnpm-lock.yaml, because a branch that bumps the shared config dependency
//     changes the lockfile and nothing else datamitsu reads. Without it a rebake
//     would read a stale node_modules and stamp the result fresh.
//   - every auto-config candidate filename, not just the one that was
//     discovered. Config discovery stats three names at the root and refuses to
//     load when more than one exists, so a tree that gains a second candidate
//     stops being loadable — but if only the discovered file were watched, every
//     stat tuple would be unchanged and the farm would be reported fresh. The
//     shim would keep running the old toolchain while `datamitsu` itself
//     errored. Absent candidates record as Exists=false, so their appearance is
//     what changes the key.
//
// configFiles is every file in the chain — the discovered config plus any
// resolved before-config files — and may contain paths that do not exist.
// Duplicates are collapsed by BuildManifest, which sorts and de-duplicates
// before stat'ing.
func WatchPaths(root string, configFiles []string) []string {
	paths := make([]string, 0, len(configFiles)+2+len(AutoConfigNames))
	paths = append(paths, configFiles...)
	if root != "" {
		paths = append(paths,
			gitHeadPath(root),
			filepath.Join(root, "pnpm-lock.yaml"),
		)
		for _, name := range AutoConfigNames {
			paths = append(paths, filepath.Join(root, name))
		}
	}
	return paths
}

// ConfigWatchPaths returns the paths an explicit-config farm watches: the
// resolved config chain, and nothing else.
//
// There is no .git/HEAD to watch and no root to stat auto-config candidates at,
// and nothing here pretends otherwise — a farm whose identity is a list of files
// becomes stale exactly when one of those files changes, appears or disappears,
// which the {path, mtime, size, exists} tuples already express. Adding the
// repository tripwires a project farm needs would make a machine-level farm
// rebake on every checkout of whatever repository the shell happened to be in.
func ConfigWatchPaths(configFiles []string) []string {
	return WatchPaths("", configFiles)
}

// gitHeadPath returns the HEAD file a checkout in root rewrites.
//
// In an ordinary repository that is <root>/.git/HEAD. In a linked `git worktree`
// — and in any repository whose .git is a gitdir pointer file, which is also how
// a submodule checkout looks — <root>/.git is a file, so that path never exists:
// the watch entry would record Exists=false at bake time and match forever,
// silently losing the tripwire in exactly the setup where several branches are
// checked out at once. The pointer names the directory holding that worktree's
// own HEAD, which is the file a checkout there rewrites.
//
// Every failure to follow the pointer falls back to the ordinary path. A wrong
// path costs the redundancy this entry provides — the watch set already carries
// every auto-config candidate with its existence flag, which is what catches a
// branch that adds or deletes a config — and never a false freshness claim.
func gitHeadPath(root string) string {
	dotGit := filepath.Join(root, ".git")
	fallback := filepath.Join(dotGit, "HEAD")

	info, err := lstat(dotGit)
	if err != nil || info.IsDir() {
		return fallback
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return fallback
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return fallback
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return fallback
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	return filepath.Join(filepath.Clean(target), "HEAD")
}

// AutoConfigNames are the file names config discovery stats at the git root, in
// the order it stats them. It mirrors cmd.discoverAutoConfig, which cannot be
// imported here.
var AutoConfigNames = []string{
	ldflags.PackageName + ".config.js",
	ldflags.PackageName + ".config.mjs",
	ldflags.PackageName + ".config.ts",
}

// ComputeStalenessKey fingerprints everything that, when changed, must produce a
// different farm: the format version, the datamitsu version, the authoritative
// root, the platform, the ordered watch-set tuples, and datamitsu's own
// environment variables.
//
// datamitsuEnv is passed in rather than read here so the key stays a pure
// function of its inputs and tests can vary it without touching the process
// environment. Production callers pass env.Environ().
func ComputeStalenessKey(formatVersion int, datamitsuVersion, root, goos, goarch string, watch []WatchFile, datamitsuEnv []string) string {
	parts := make([][]byte, 0, 5+len(watch)+len(datamitsuEnv))
	parts = append(parts,
		[]byte(strconv.Itoa(formatVersion)),
		[]byte(datamitsuVersion),
		[]byte(root),
		[]byte(goos),
		[]byte(goarch),
	)
	for _, w := range watch {
		parts = append(parts, fmt.Appendf(nil, "%s\x1f%d\x1f%d\x1f%t", w.Path, w.MtimeNS, w.Size, w.Exists))
	}
	// Sorted by env.Environ; sorted again here so a caller passing an unordered
	// slice cannot produce an unstable key.
	sortedEnv := append([]string(nil), datamitsuEnv...)
	sort.Strings(sortedEnv)
	for _, kv := range sortedEnv {
		parts = append(parts, []byte(kv))
	}
	return hashutil.XXH3Multi(parts...)
}

// BuildManifest returns the manifest describing plan. It writes nothing —
// materialization is Task 7's concern — so the caller can place the manifest
// inside the same atomic swap as the farm directory and the two can never
// disagree.
//
// The watch set arrives already stat'ed rather than as paths, because *when* it
// was stat'ed is a correctness question only the caller can answer: a tuple
// taken after the config was read describes a file the plan may not reflect.
// See cmd.bakeSourceFarm, which snapshots before the load for that reason.
func BuildManifest(plan Plan, origin Origin, watch []WatchFile) Manifest {
	m := Manifest{
		FormatVersion:    ManifestFormatVersion,
		Origin:           origin,
		Root:             plan.Root,
		FarmDir:          plan.FarmDir,
		DatamitsuVersion: ldflags.Version,
		OS:               runtime.GOOS,
		Arch:             runtime.GOARCH,
		Watch:            watch,
		Entries:          plan.Entries,
		Excluded:         plan.Excluded,
		Shadowed:         plan.Shadowed,
	}
	m.StalenessKey = ComputeStalenessKey(m.FormatVersion, m.DatamitsuVersion, m.Root, m.OS, m.Arch, watch, env.Environ())
	return m
}

// BuildConfigManifest returns the manifest describing a farm baked from an
// explicitly named config chain. configPaths must already be resolved —
// env.ResolveConfigChain is what produced the farm directory, and recording a
// different spelling of the same files here would leave the manifest describing
// a farm no identity computation reproduces.
//
// plan.Root is expected to be empty for such a farm; it is not overwritten here,
// so a caller that sets it wrongly fails its own test rather than being quietly
// corrected.
func BuildConfigManifest(plan Plan, configPaths []string, watch []WatchFile) Manifest {
	m := BuildManifest(plan, OriginExplicitConfig, watch)
	m.ConfigPaths = append([]string(nil), configPaths...)
	return m
}

// Encode renders the manifest as indented JSON. Key order is the struct's field
// order and every list is already sorted, so the same manifest always produces
// byte-identical output.
func Encode(m Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode farm manifest: %w", err)
	}
	return append(data, '\n'), nil
}

// Load reads and decodes a manifest.
//
// It does not judge the contents: a manifest from a future format version loads
// successfully and is reported stale by Validate. Only an unreadable or
// malformed file is an error.
func Load(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read farm manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse farm manifest %s: %w", path, err)
	}
	return m, nil
}

// shimTargetUsable reports whether the datamitsu executable a farm's shim
// entries link to is still there and still executable.
//
// An empty target means the manifest predates the field, or was built by a
// caller that never materialized it; there is nothing to check and the farm is
// judged on its watch set alone.
//
// It goes through the same lstat hook as the watch set: resolveShimTarget
// already ran the path through EvalSymlinks, so the target is a real file and
// lstat and stat agree — while keeping Validate's "nothing but lstat" property
// mechanically checkable.
func shimTargetUsable(target string) bool {
	if target == "" {
		return true
	}
	info, err := lstat(target)
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o100 != 0
}

// UsableStale reports whether a manifest that is *not* fresh may still be
// served back.
//
// Every path that falls back to the farm already on disk goes through here:
// activation after a bake that could not be computed, and the shim after a
// rebake that could not run. Those paths deliberately prefer a stale farm to no
// farm — a working toolchain from five minutes ago beats a machine-wide storm of
// exit-127s — but "stale" and "written in a schema this build cannot read" are
// different states, and only the first one is safe to serve.
//
// Three of Validate's checks therefore survive the fallback:
//
//   - FormatVersion. The version is bumped exactly when the shim's correctness
//     depends on a new field (Entry.RequiredPaths in version 2), so an older
//     manifest is not merely stale: reading it silently downgrades a check.
//     Serving it back is what would let a partially installed runtime-managed
//     app exec — the failure the bump exists to retire.
//   - OS and Arch. The recorded commands are absolute paths to this platform's
//     binaries; a farm baked for another one (a cache directory shared over a
//     network home) names files that are not runnable here.
//   - ShimTarget. Every shim entry is a symlink to it, so a target that has
//     moved leaves a farm of dangling links — and a dangling link is the one
//     kind of broken entry a shell skips silently, walking on to the system
//     binary of the same name.
//
// DatamitsuVersion is deliberately *not* checked. It is a staleness signal, not
// a schema one: an upgraded datamitsu meeting a farm baked by the previous build
// finds entries that still name real store paths, and the shim re-stats every
// one of them before exec'ing. Refusing there would break the case the fallback
// exists for — offline, right after an upgrade.
func UsableStale(m Manifest) bool {
	if m.FormatVersion != ManifestFormatVersion {
		return false
	}
	if m.OS != runtime.GOOS || m.Arch != runtime.GOARCH {
		return false
	}
	return shimTargetUsable(m.ShimTarget)
}

// Validate reports whether the farm this manifest describes is still correct for
// the tree as it is now.
//
// It performs only lstat calls and one hash comparison — no config load, no
// subprocess, no network — because it runs on every shim invocation. Everything
// it needs was recorded at bake time.
//
// Any mismatch is reported as stale rather than as an error. There is nothing a
// caller can do with the distinction: the response to "wrong format version",
// "different datamitsu build" and "the config changed" is the same rebake.
func Validate(m Manifest) bool {
	if m.DatamitsuVersion != ldflags.Version {
		return false
	}
	if !UsableStale(m) {
		return false
	}

	current := make([]WatchFile, len(m.Watch))
	for i, w := range m.Watch {
		current[i] = StatWatchFile(w.Path)
		if current[i] != m.Watch[i] {
			return false
		}
	}

	// The key comparison is what catches a changed DATAMITSU_* variable, which
	// no amount of stat'ing would reveal.
	return ComputeStalenessKey(m.FormatVersion, m.DatamitsuVersion, m.Root, m.OS, m.Arch, current, env.Environ()) == m.StalenessKey
}
