package sourcefarm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
)

// Materialization turns a Plan into a directory of symlinks plus the manifest
// describing it. Three rules shape the implementation, and each one exists
// because of a failure mode that is worse than not baking at all:
//
//   - The live farm is never mutated in place. A shell's PATH points at the farm
//     directory for as long as that shell lives, so editing it entry by entry
//     would expose a half-built toolchain to every running shell on the machine.
//     The new farm is assembled in a staging directory on the same filesystem
//     and moved into place with rename.
//   - A failed bake keeps the previous farm and the previous manifest. Being
//     offline, or having a config that does not evaluate, must not replace a
//     working toolchain with an empty directory — the result would be a
//     machine-wide storm of exit-127s until the next successful bake. Stale but
//     working beats empty, and the previous manifest's staleness check will keep
//     asking for a rebake until one succeeds.
//   - No entry is ever a dangling symlink. An entry whose target does not exist
//     is a shim by construction (see strategyFor), so a missing target here means
//     the plan and the filesystem disagree, and the bake fails rather than
//     writing a link that turns into ENOENT at exec time.
//
// The package is silent about progress: it never touches internal/ui, because
// the caller's stdout is a shell script being piped into eval and any stray byte
// on it corrupts the activation.

// defaultLockTimeout is how long a bake waits for a peer's bake to produce the
// manifest it needs before giving up on politeness and blocking on the lock.
// Ten tmux panes activating at once is the case this exists for: nine of them
// should notice the tenth's result and exit, not queue up ten full bakes.
const defaultLockTimeout = 10 * time.Second

// defaultPollInterval is how often the waiting path re-checks for a peer's
// finished manifest.
const defaultPollInterval = 25 * time.Millisecond

// Options tunes materialization. The zero value is what production uses.
type Options struct {
	// ShimTarget is the absolute path shim entries link to. Empty means the
	// running datamitsu executable, resolved through os.Executable and
	// filepath.EvalSymlinks. It is never looked up on PATH: the farm itself is
	// on PATH, so a PATH lookup could point the shim at a shimmed name.
	ShimTarget string

	// CacheRoot is the directory the farm must live under. Empty means
	// env.GetCachePath(). The containment check is a guard against a bad root
	// turning a bake into an arbitrary-directory rewrite.
	CacheRoot string

	// ManifestPath overrides where the manifest is written. Empty means the
	// standard sibling of the farm directory.
	ManifestPath string

	// LockPath overrides the advisory lock file. Empty means the standard
	// sibling of the farm directory.
	LockPath string

	// LockTimeout and PollInterval override the waiting behavior when a peer
	// holds the lock.
	LockTimeout  time.Duration
	PollInterval time.Duration

	// Warn receives the single line a failed bake reports. Empty means one line
	// on os.Stderr. Callers that report the returned error themselves pass a
	// no-op to avoid saying it twice.
	Warn func(string)
}

// MaterializeWithOptions writes the farm described by plan, and the manifest
// describing it, atomically.
func MaterializeWithOptions(plan Plan, m Manifest, opts Options) error {
	err := materialize(plan, m, opts)
	if err != nil {
		warn := opts.Warn
		if warn == nil {
			warn = func(line string) { fmt.Fprintln(os.Stderr, line) }
		}
		// A failure after the farm has been swapped in leaves the new entries
		// live under the previous manifest, so "keeping the previous one" would
		// be untrue. The stale watch set makes the next invocation rebake, but
		// the message must not claim nothing happened.
		var swapped farmSwappedError
		if errors.As(err, &swapped) {
			warn("datamitsu: source farm replaced but its manifest could not be written; run `datamitsu source refresh --force`: " + err.Error())
		} else {
			warn("datamitsu: source farm not updated, keeping the previous one: " + err.Error())
		}
	}
	return err
}

// farmSwappedError marks a failure that happened after the new farm directory was
// already live, so the caller can tell "nothing changed" from "everything
// changed except the manifest".
type farmSwappedError struct{ err error }

func (e farmSwappedError) Error() string { return e.err.Error() }
func (e farmSwappedError) Unwrap() error { return e.err }

func materialize(plan Plan, m Manifest, opts Options) error {
	farmDir, parent, cacheRoot, err := checkFarmPath(plan.FarmDir, opts.CacheRoot)
	if err != nil {
		return err
	}

	shimTarget, err := resolveShimTarget(opts.ShimTarget)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(parent, farmDirMode); err != nil {
		return fmt.Errorf("create farm parent directory: %w", err)
	}
	if err := checkAncestorsSafe(parent, cacheRoot); err != nil {
		return err
	}
	if err := CheckFarmSafe(farmDir); err != nil {
		return err
	}

	// Both are siblings of the farm directory, derived from `parent` rather than
	// from env.GetProject*Path(plan.Root): those recompute the whole path from
	// the real cache root and would ignore opts.CacheRoot, writing a test's
	// manifest into the developer's cache. The file *names* still come from the
	// env constants, which is what keeps these in step with what the shim reads.
	manifestPath := opts.ManifestPath
	if manifestPath == "" {
		manifestPath = filepath.Join(parent, env.ProjectManifestFileName)
	}
	lockPath := opts.LockPath
	if lockPath == "" {
		lockPath = filepath.Join(parent, env.ProjectLockFileName)
	}

	release, peerWon, err := acquireBakeLock(lockPath, manifestPath, m, opts)
	if err != nil {
		return err
	}
	if peerWon {
		// A concurrent bake produced exactly the farm this call would have
		// produced. Redoing it would only churn inodes under live shells.
		return nil
	}
	defer release()

	stage, err := os.MkdirTemp(parent, ".stage-")
	if err != nil {
		return fmt.Errorf("create farm staging directory: %w", err)
	}
	// Every failure below leaves the live farm untouched; only the staging
	// directory needs cleaning up.
	defer func() { _ = os.RemoveAll(stage) }()

	stagedFarm := filepath.Join(stage, "bin")
	if err := os.Mkdir(stagedFarm, farmDirMode); err != nil {
		return fmt.Errorf("create staged farm directory: %w", err)
	}
	// Mkdir's mode is masked by umask; the farm's mode is a security property,
	// not a default, so it is set explicitly.
	if err := os.Chmod(stagedFarm, farmDirMode); err != nil {
		return fmt.Errorf("set staged farm directory mode: %w", err)
	}
	if err := writeEntries(stagedFarm, plan.Entries, shimTarget); err != nil {
		return err
	}

	// Recorded here rather than by the caller: this is the only place that knows
	// which executable the entries actually point at, and the manifest must not
	// name one the farm was not built against.
	m.ShimTarget = shimTarget

	manifestData, err := Encode(m)
	if err != nil {
		return err
	}
	stagedManifest := filepath.Join(stage, env.ProjectManifestFileName)
	if err := os.WriteFile(stagedManifest, manifestData, 0o600); err != nil {
		return fmt.Errorf("write staged farm manifest: %w", err)
	}

	if err := syncDir(stagedFarm); err != nil {
		return err
	}
	if err := syncDir(stage); err != nil {
		return err
	}

	return swapIntoPlace(stagedFarm, farmDir, stagedManifest, manifestPath, stage, parent)
}

// farmDirMode is the mode the farm directory is built with. It is owner-only
// because it is a directory of executables that PATH resolution trusts; a
// group-writable farm would let another account choose what this user's shell
// runs.
const farmDirMode os.FileMode = 0o700

// farmEntryMode is the mode a farm entry is expected to resolve to. Entries are
// symlinks, and a symlink carries no useful mode of its own — the kernel uses
// the target's — so this is enforced on the target rather than set on the link:
// the target must be executable and must not be writable by group or other.
const farmEntryMode os.FileMode = 0o755

// checkFarmPath validates the farm directory and returns it cleaned along with
// its parent (the per-root directory holding the manifest and the lock) and the
// cleaned cache root the containment check was made against.
func checkFarmPath(farmDir, cacheRoot string) (string, string, string, error) {
	if farmDir == "" {
		return "", "", "", errors.New("farm directory must not be empty")
	}
	if !filepath.IsAbs(farmDir) {
		return "", "", "", fmt.Errorf("farm directory must be absolute: %q", farmDir)
	}
	cleaned := filepath.Clean(farmDir)

	cacheRoot = resolveCacheRoot(cacheRoot)

	if !underRoot(cleaned, cacheRoot) {
		return "", "", "", fmt.Errorf("farm directory %q is not under the datamitsu cache directory %q", cleaned, cacheRoot)
	}

	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		return "", "", "", fmt.Errorf("farm directory %q has no parent directory", cleaned)
	}
	return cleaned, parent, cacheRoot, nil
}

// resolveCacheRoot returns the cleaned cache root an override names, or the real
// one when the override is empty.
func resolveCacheRoot(override string) string {
	if override == "" {
		override = env.GetCachePath()
	}
	return filepath.Clean(override)
}

// underRoot reports whether dir is a strict descendant of root. Both must
// already be cleaned.
func underRoot(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// FarmUsable reports whether farmDir is a farm that may be put on PATH: it
// exists, it is a directory, and it — along with every directory between it and
// the cache root — passes the same ownership and mode checks materialization
// enforces before replacing it. cacheRoot empty means the real cache root, which
// is what every production caller wants; tests that bake into a temporary root
// pass it explicitly so the chain is walked to the same place materialization
// walked it.
//
// Every caller that activates or falls back to an *existing* farm — rather than
// one this process just wrote — goes through here. Without it the security
// refusal in CheckFarmSafe is only a refusal to rewrite: a farm another account
// can write to would fail materialization and then be activated anyway, because
// "the directory is there and its manifest decodes" was the whole test. The two
// questions ("may I replace this?" and "may I trust this?") have the same
// answer, so they share the same check.
func FarmUsable(farmDir, cacheRoot string) bool {
	info, err := os.Lstat(farmDir)
	if err != nil || !info.IsDir() {
		return false
	}
	if parent := filepath.Dir(farmDir); parent != farmDir {
		if checkAncestorsSafe(parent, resolveCacheRoot(cacheRoot)) != nil {
			return false
		}
	}
	return CheckFarmSafe(farmDir) == nil
}

// CheckFarmSafe refuses a farm that is not safely owned. A farm another account
// can write to is a farm another account can point at its own binaries, and PATH
// resolution asks no further questions.
//
// A farm that does not exist is not an error: materialization is about to create
// one. Callers that need it to exist use FarmUsable.
func CheckFarmSafe(farmDir string) error {
	info, err := os.Lstat(farmDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect existing farm directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("existing farm path %q is not a directory", farmDir)
	}
	return checkDirOwnership(info, farmDir, "existing farm directory")
}

// checkAncestorsSafe refuses a farm whose path runs through any directory
// another account can write to, from the per-root directory holding the farm up
// to and including the cache root.
//
// The farm's own mode is not the whole answer, because a farm entry is reached
// by *path*: PATH names {cache}/projects/{hash}/bin, and every exec re-resolves
// it. An account that can write any directory along that path cannot write
// inside an owner-only bin, but it does not need to — it can rename the
// component below it aside and put its own tree there after FarmUsable has
// already passed, and every shell already holding that path on PATH then execs
// whatever it contains. Checking only the immediate parent would leave
// {cache}/projects and the cache root as the same hijack one level up: renaming
// {hash} substitutes the whole per-root directory, bin and manifest together.
// The manifest and the lock live beside the farm, so a writable ancestor is also
// a free hand to rewrite the recorded commands the shim execs without a config
// load.
//
// The walk stops at the cache root because that is the last directory datamitsu
// creates: everything above it is the user's own $HOME or $XDG_CACHE_HOME, whose
// mode is not this program's to have an opinion about (and /tmp-rooted caches
// would fail on the sticky world-writable directory above them).
//
// Unlike the farm itself, these directories are never repaired: they are created
// 0700 by materialization, so finding one otherwise means something outside
// datamitsu made it, and chmod'ing another account's directory is not a fix.
func checkAncestorsSafe(parent, cacheRoot string) error {
	dir := filepath.Clean(parent)
	for {
		if err := checkAncestorSafe(dir); err != nil {
			return err
		}
		if dir == cacheRoot || !underRoot(dir, cacheRoot) {
			// Either the walk reached the last directory datamitsu owns, or the
			// farm is not under this cache root at all — the latter cannot happen
			// on the materialization path (checkFarmPath rejects it first) and is
			// the caller's own business on the FarmUsable path.
			return nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			return nil
		}
		dir = next
	}
}

// checkAncestorSafe applies the ownership and mode rule to one directory on the
// farm's path.
func checkAncestorSafe(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect the farm parent directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("farm parent path %q is not a directory", dir)
	}
	return checkDirOwnership(info, dir, "farm parent directory")
}

// checkDirOwnership is the ownership and mode rule both directory checks apply:
// owned by this user, and writable by nobody else.
func checkDirOwnership(info os.FileInfo, dir, what string) error {
	if perm := info.Mode().Perm(); perm&0o022 != 0 {
		return fmt.Errorf("%s %q is group- or world-writable (mode %04o)", what, dir, perm)
	}
	if uid, ok := ownerUID(info); ok {
		if self := os.Getuid(); self >= 0 && uid != self {
			return fmt.Errorf("%s %q is owned by uid %d, not %d", what, dir, uid, self)
		}
	}
	return nil
}

// resolveShimTarget returns the absolute, symlink-resolved path shim entries
// point at.
func resolveShimTarget(override string) (string, error) {
	target := override
	if target == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("locate the datamitsu executable: %w", err)
		}
		target = exe
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("shim target must be absolute: %q", target)
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("shim target %q is not usable: %w", target, err)
	}
	// Every farm entry is a symlink to this one file, so its mode is checked
	// once here rather than repeated per entry — and the message names the
	// datamitsu executable, which is the thing the user has to fix. A
	// group-writable target means anyone in that group can replace what every
	// shimmed tool on the machine execs.
	if perm := info.Mode().Perm(); perm&0o100 == 0 || perm&0o022 != 0 {
		return "", fmt.Errorf("the datamitsu executable %q has mode %04o; source mode needs it owner-executable and not group- or world-writable (for example %04o)",
			target, perm, farmEntryMode.Perm())
	}
	return target, nil
}

// writeEntries creates one symlink per entry in the staged directory.
//
// The dangling-symlink invariant is enforced here rather than assumed: a
// symlink entry whose target has vanished from the store between planning and
// baking fails the whole bake, which keeps the previous working farm, instead of
// silently producing a name that exits ENOENT.
func writeEntries(dir string, entries []Entry, shimTarget string) error {
	for _, entry := range entries {
		target := shimTarget
		if entry.Strategy == StrategySymlink {
			if entry.Command == "" {
				return fmt.Errorf("entry %q is a symlink strategy with no command", entry.Name)
			}
			target = entry.Command
		}
		info, err := os.Stat(target)
		if err != nil {
			return fmt.Errorf("entry %q would be a dangling symlink to %q: %w", entry.Name, target, err)
		}
		// The shim target's mode is already checked once in resolveShimTarget,
		// which can name the executable in its message; only a symlink entry's
		// own store target still needs checking here.
		if entry.Strategy == StrategySymlink {
			if perm := info.Mode().Perm(); perm&0o100 == 0 || perm&0o022 != 0 {
				return fmt.Errorf("entry %q target %q has mode %04o, want an owner-executable, non-group-writable file such as %04o",
					entry.Name, target, perm, farmEntryMode.Perm())
			}
		}
		link := filepath.Join(dir, entry.Name)
		if filepath.Dir(link) != dir {
			// Config validation rejects names like this; reaching it means the
			// plan came from an unvalidated map, and a farm entry that escapes
			// its own directory is not something to recover from.
			return fmt.Errorf("entry name %q would escape the farm directory", entry.Name)
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("link farm entry %q: %w", entry.Name, err)
		}
	}
	return nil
}

// swapIntoPlace moves the staged farm and manifest into their final locations.
//
// The farm goes first and the manifest second, deliberately. A crash between the
// two leaves a new farm described by the *previous* manifest, whose watch set no
// longer matches the tree, so the next invocation rebakes. The opposite order
// would leave a manifest promising entries the farm does not have, which the
// shim would resolve into exit-127s for names that exist.
func swapIntoPlace(stagedFarm, farmDir, stagedManifest, manifestPath, stage, parent string) error {
	// rename(2) refuses to replace a non-empty directory, so the live farm is
	// moved aside first. The window where the farm does not exist is a single
	// rename wide and only concurrent readers can observe it; the alternative —
	// mutating the live directory — exposes a half-built farm for the whole bake.
	retired := filepath.Join(stage, "retired")
	hadPrevious := false
	if err := os.Rename(farmDir, retired); err == nil {
		hadPrevious = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("retire the previous farm directory: %w", err)
	}

	if err := os.Rename(stagedFarm, farmDir); err != nil {
		// A peer that finished its own bake while this one was staging has
		// already put a farm here. aqua's lock-free model: whoever lands first
		// wins, and the loser restores nothing.
		if _, statErr := os.Stat(farmDir); statErr == nil {
			return nil
		}
		if hadPrevious {
			// Put the previous farm back rather than leaving PATH pointing at
			// nothing.
			_ = os.Rename(retired, farmDir)
		}
		return fmt.Errorf("move the new farm into place: %w", err)
	}

	// From here the new farm is live, so every remaining failure is reported as
	// farmSwappedError rather than as "nothing was updated".
	if err := os.Rename(stagedManifest, manifestPath); err != nil {
		return farmSwappedError{fmt.Errorf("move the farm manifest into place: %w", err)}
	}
	if err := syncDir(parent); err != nil {
		return farmSwappedError{err}
	}
	return nil
}

// syncDir flushes a directory's entries so a crash cannot leave the rename
// visible without the entries it names.
func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %q for sync: %w", dir, err)
	}
	defer func() { _ = handle.Close() }()
	// Some filesystems refuse fsync on a directory. The rename is still ordered;
	// durability across a power loss is not worth failing a bake over.
	_ = handle.Sync()
	return nil
}

// acquireBakeLock takes the per-root advisory lock.
//
// The lock is tried without blocking first. When a peer holds it, this call
// polls for the peer's manifest to appear with the same staleness key rather
// than queueing: ten shells activating at once should cost one bake, not ten.
// Only when the peer has not produced a usable manifest before the timeout does
// this call block on the lock and bake itself.
func acquireBakeLock(lockPath, manifestPath string, want Manifest, opts Options) (release func(), peerWon bool, err error) {
	// os.OpenFile sets close-on-exec, so a lock held here is not inherited by
	// the tools datamitsu execs. The lock file is created once and never
	// unlinked: unlinking it would let a peer lock a fresh inode for the same
	// root and bake concurrently.
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open farm lock file: %w", err)
	}
	closeFile := func() {
		unlockFile(file)
		_ = file.Close()
	}

	locked, err := tryLockFile(file)
	if err != nil {
		_ = file.Close()
		return nil, false, fmt.Errorf("lock the farm: %w", err)
	}
	if locked {
		return closeFile, false, nil
	}

	timeout := opts.LockTimeout
	if timeout <= 0 {
		timeout = defaultLockTimeout
	}
	poll := opts.PollInterval
	if poll <= 0 {
		poll = defaultPollInterval
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(poll)
		if manifestMatches(manifestPath, want) {
			_ = file.Close()
			return nil, true, nil
		}
		locked, err = tryLockFile(file)
		if err != nil {
			_ = file.Close()
			return nil, false, fmt.Errorf("lock the farm: %w", err)
		}
		if locked {
			return closeFile, false, nil
		}
	}

	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, false, fmt.Errorf("lock the farm: %w", err)
	}
	return closeFile, false, nil
}

// manifestMatches reports whether the manifest on disk is already the one this
// bake would have written.
//
// The staleness key alone does not answer it. Two fields are deliberately
// outside the key and each one, left unchecked, turns a peer's bake into a
// silently wrong success:
//
//   - ShimTarget (see Manifest.ShimTarget). A bake triggered by a moved
//     datamitsu binary would find the old manifest's key equal to its own,
//     declare the peer the winner, and return success over a farm of dangling
//     symlinks — the exact state this bake was run to repair.
//   - ConfigArgs (see Manifest.ConfigArgs). Two concurrent bakes for the same
//     root from different config chains compute the same key, so the loser
//     would activate the winner's toolchain while reporting that it had baked
//     its own.
func manifestMatches(manifestPath string, want Manifest) bool {
	if want.StalenessKey == "" {
		return false
	}
	existing, err := Load(manifestPath)
	if err != nil {
		return false
	}
	if existing.StalenessKey != want.StalenessKey {
		return false
	}
	if !slices.Equal(existing.ConfigArgs, want.ConfigArgs) {
		return false
	}
	return shimTargetUsable(existing.ShimTarget)
}
