// Package shim implements source-mode's argv[0] dispatch: the path taken when
// the datamitsu executable is invoked through one of the farm's symlinks rather
// than by its own name.
//
// It runs before cobra and before the UI exists. cmd.Execute builds the whole
// command tree, initializes color, detects the terminal mode and activates a
// process-global display; none of that is useful to a process whose only job is
// to replace itself with another program, and all of it is measurable against a
// ~10 ms budget. Dispatch therefore happens in main, before cmd.Execute is
// called at all.
//
// # What the dispatch costs
//
// getcwd, a walk up for .git, one read of a JSON manifest, a handful of lstat calls
// to check it is still current, one stat of the exec target, and execve. No
// config load, no network, no second process: syscall.Exec replaces this process
// image, so signals, exit codes, and the stdio the shell wired up are the
// target's, not a child's.
//
// # Why an unknown name is exit 127 and never a PATH search
//
// The farm is prepended to PATH, so a name it declares but cannot run must fail
// loudly. Falling through to the rest of PATH would run a system binary of the
// same name — exiting 0, printing plausible output, from a version the project
// did not pin. That failure mode is worse than the feature's absence, so every
// dead end here is exit 127 with a message naming the app and the root.
//
// The one exception is a datamitsu executable that was simply renamed. If the
// invoked name is not in any resolvable manifest *and* the executable was not
// reached through a farm directory, the invocation is an ordinary CLI run and
// Dispatch declines to handle it.
package shim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/shellquote"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

// ExitNotFound is the exit code for a name the farm cannot run. 127 is the
// shell's own "command not found", which is exactly what happened from the
// user's point of view.
const ExitNotFound = 127

// ExitNotExecutable is the exit code for a target that exists but could not be
// executed, matching the shell's convention for the same condition.
const ExitNotExecutable = 126

// isOwnName reports whether the executable was invoked under datamitsu's own
// name rather than as a farm entry. Anything else is a candidate for dispatch —
// and, when no manifest claims it, falls back to the normal CLI, so a user who
// renames the binary keeps a working datamitsu.
func isOwnName(name string) bool {
	return name == ldflags.PackageName
}

// farmDirName is the last path element of a farm directory, and the two
// namespace names its grandparent. Together they identify an invocation as
// having arrived through a farm: {cache}/{namespace}/{hash}/bin/{name}.
//
// There are two namespaces because there are two farm origins. A project farm
// is keyed by git root and lives under "projects"; a farm baked from a config
// chain the user named with --config is keyed by that chain and lives under
// "configs". Both are farms for every purpose this file cares about — they go
// on PATH, they must be stripped from a child's PATH, and an entry in either
// must fail loudly rather than fall through. The names come from env so the
// two packages cannot spell them differently.
const (
	farmDirName     = "bin"
	projectsDirName = env.ProjectFarmsDirName
	configsDirName  = env.ConfigFarmsDirName
)

// noAutoConfigFlag suppresses git-root config discovery in a spawned child. It
// is what keeps an explicit-config rebake from merging in whatever config the
// directory the tool happened to run from belongs to; see explicitConfigArgs.
const noAutoConfigFlag = "--no-auto-config"

// Dispatcher performs argv[0] dispatch. Every dependency that touches the
// process, the filesystem or another program is a field so the whole decision
// tree is testable without a farm on disk.
type Dispatcher struct {
	// Args is the process argv, argv[0] included.
	Args []string

	Getwd      func() (string, error)
	Executable func() (string, error)
	Environ    func() []string
	// EvalSymlinks resolves a path to the file behind it. It is what keeps a
	// spawn from re-entering dispatch; see datamitsuExe.
	EvalSymlinks func(string) (string, error)

	// ManifestPath maps a git root to its manifest file, and CacheRoot returns
	// the directory every farm lives under.
	ManifestPath func(root string) (string, error)
	CacheRoot    func() string
	// Load and Validate are sourcefarm's manifest reader and freshness check.
	Load     func(path string) (sourcefarm.Manifest, error)
	Validate func(sourcefarm.Manifest) bool

	// Superproject reports the working tree that owns a root as a submodule, or
	// false when the root has no superproject. It is what decides whether the
	// manifest search may climb out of a root that has no farm; see
	// superprojectOf.
	Superproject func(root string) (string, bool)

	Stat func(path string) (os.FileInfo, error)

	// Exec replaces this process with another program. It returns only on
	// failure.
	Exec func(path string, argv, environ []string) error

	// Spawn runs datamitsu as a child process for the two cases that need the
	// full resolution path: re-baking a stale farm and installing a tool that
	// has not been downloaded yet.
	Spawn func(SpawnRequest) error

	Stderr io.Writer

	// throughFarm records whether this invocation arrived through a farm, and
	// invokedFarmDir the farm directory it arrived through. Both are computed
	// once at the top of Dispatch, before anything can rebake the farm out from
	// under the answer; see computeThroughFarm.
	throughFarm    bool
	invokedFarmDir string
}

// New returns a Dispatcher wired to the real process.
func New() *Dispatcher {
	return &Dispatcher{
		Args:         os.Args,
		Getwd:        os.Getwd,
		Executable:   os.Executable,
		Environ:      os.Environ,
		EvalSymlinks: filepath.EvalSymlinks,
		ManifestPath: env.GetProjectManifestPath,
		CacheRoot:    env.GetCachePath,
		Load:         sourcefarm.Load,
		Validate:     sourcefarm.Validate,
		Superproject: superprojectOf,
		Stat:         os.Stat,
		Exec:         execProcess,
		Spawn:        spawnDatamitsu,
		Stderr:       os.Stderr,
	}
}

// Dispatch is the entry point main calls. handled=false means "this is a normal
// datamitsu CLI invocation, carry on"; handled=true means the invocation was a
// farm entry and the process should exit with the returned code — though on the
// success path Dispatch does not return at all, because the process has been
// replaced.
func Dispatch() (exitCode int, handled bool) {
	return New().Dispatch()
}

// Dispatch resolves the invoked name against a farm manifest and execs the
// recorded command. Which manifest depends on the farm's origin: a git-root
// farm's is the one for the current directory's git root, so a `cd` or a branch
// switch is picked up transparently, while an explicit-config farm's is the one
// beside the farm the invocation came through, and cwd plays no part at all.
func (d *Dispatcher) Dispatch() (int, bool) {
	name := invokedName(d.Args)
	if name == "" {
		return 0, false
	}
	if isOwnName(name) {
		return 0, false
	}

	// Decided here, before a rebake can delete the very farm entry the answer is
	// read from: an invocation that arrived through a farm must fail loudly even
	// when the config change that made the manifest stale is what removed it.
	d.throughFarm = d.computeThroughFarm()

	// The origin branch, and with it the trust boundary. An explicit-config farm
	// is answered entirely from what it recorded: no getcwd, no walk up for
	// .git, no manifest belonging to whatever repository the shell is standing
	// in. A shell activated against a machine-level config that cds into an
	// untrusted clone therefore keeps running the machine-level tools rather
	// than starting to evaluate the clone's JavaScript.
	if configManifestPath, configManifest, handled, failure := d.explicitConfigFarm(name); handled {
		if failure != "" {
			return d.fail(failure), true
		}
		return d.runManifest(name, configManifestPath, configManifest)
	}

	nearest, inRepo := d.nearestRoot()
	if !inRepo {
		// Outside a repository there is no manifest to consult, so the name
		// cannot be one of ours.
		return d.decline(name, "", "the current directory is not inside a git repository")
	}

	manifestPath, manifest, root, err := d.loadManifest(nearest)
	if err != nil {
		// An activated shell that cds into a never-activated repository lands
		// here. The farm is deliberately not baked implicitly: baking evaluates
		// that repository's JavaScript, and typing a tool name is not consent to
		// run code from a tree the user has not activated.
		return d.decline(name, root, err.Error())
	}

	return d.runManifest(name, manifestPath, manifest)
}

// runManifest resolves the invoked name against one already-loaded manifest and
// execs it. Both origins share it: once the right manifest is in hand, a
// git-root farm and an explicit-config farm behave identically — the same
// freshness check, the same rebake, the same lazy install, the same exec — and
// everything that differs between them is recorded in the manifest itself.
func (d *Dispatcher) runManifest(name, manifestPath string, manifest sourcefarm.Manifest) (int, bool) {
	entry, found := lookupEntry(manifest, name)
	if !found {
		return d.declineUnknown(name, manifest)
	}

	// The freshness check is what makes `git checkout v2 && terragrunt plan`
	// work on a single line: it is a comparison of recorded stat tuples, so it
	// happens on every invocation rather than on a prompt hook that a compound
	// command never fires.
	if !d.Validate(manifest) {
		res := d.rebake(manifestPath, name, manifest, entry)
		manifest, entry = res.manifest, res.entry
		if res.retired {
			return d.failRetired(name, manifest), true
		}
		if !res.found {
			return d.declineUnknown(name, manifest)
		}
	}

	entry, err := d.ensureInstalled(manifestPath, name, manifest, entry)
	if err != nil {
		return d.fail(err.Error()), true
	}

	return d.execEntry(entry, manifest)
}

// invokedName returns the base name the executable was invoked under, with a
// Windows .exe suffix removed so the same manifest entry matches on either
// platform.
func invokedName(args []string) string {
	if len(args) == 0 {
		return ""
	}
	base := filepath.Base(args[0])
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return strings.TrimSuffix(base, ".exe")
}

// nearestRoot returns the innermost working-tree root at or above the current
// directory. It is a deliberately cheap approximation of the resolution
// facts.GetGitRoot performs: it only selects *which* manifest to open, and the
// manifest records the authoritative root the config loader resolved.
//
// The authoritative root is physical. facts resolves the working directory
// through EvalSymlinks (and `git rev-parse --show-toplevel` reports a physical
// path too), while os.Getwd honours $PWD and so reports the logical path a shell
// cd'd through. On macOS every repository under /tmp or /var is reached
// logically, so hashing the logical path keys a farm that was never baked. The
// cwd is resolved here for the same reason.
func (d *Dispatcher) nearestRoot() (string, bool) {
	dir, err := d.Getwd()
	if err != nil {
		return "", false
	}
	if d.EvalSymlinks != nil {
		if resolved, resolveErr := d.EvalSymlinks(dir); resolveErr == nil {
			dir = resolved
		}
	}

	for {
		if _, err := d.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// loadManifest opens the farm manifest for the nearest root that has one,
// returning its path, its contents and the root it belongs to.
//
// It may climb above the nearest root, but only along the chain the
// authoritative resolution itself follows: facts.GetGitRoot climbs past
// submodules to the topmost superproject (see resolveGitRootViaGit), so a farm
// baked for a superproject is the farm a tool run from inside its submodule must
// get — the submodule's own directory never had one baked.
//
// Every other nested repository stops the search. `outer/vendor/inner`, where
// inner is an unrelated checkout, is a *different repository*, and serving it
// outer's farm would silently pin tools outer chose for a tree that never
// consented to them. That is the same rule as "a farm baked for a different
// repository is never used implicitly", and the answer here is the documented
// one: exit 127 naming the root to activate.
//
// The error names the nearest root, because that is the repository the user is
// standing in and the one `datamitsu source bash` would activate.
func (d *Dispatcher) loadManifest(nearest string) (string, sourcefarm.Manifest, string, error) {
	root := nearest
	for range maxSuperprojectClimb {
		if manifestPath, manifest, ok := d.tryLoad(root); ok {
			return manifestPath, manifest, root, nil
		}
		if d.Superproject == nil {
			break
		}
		super, ok := d.Superproject(root)
		if !ok {
			break
		}
		root = super
	}
	return "", sourcefarm.Manifest{}, nearest,
		errors.New("no source-mode farm has been created for this repository")
}

// tryLoad reads the farm manifest for one root, reporting whether there is one
// to act on.
func (d *Dispatcher) tryLoad(root string) (string, sourcefarm.Manifest, bool) {
	manifestPath, err := d.ManifestPath(root)
	if err != nil {
		return "", sourcefarm.Manifest{}, false
	}
	manifest, err := d.Load(manifestPath)
	if err != nil {
		return "", sourcefarm.Manifest{}, false
	}
	return manifestPath, manifest, true
}

// computeThroughFarm reports whether this invocation arrived through a farm
// entry: {cache}/projects/{hash}/bin/{name}. It is what separates "a farm entry
// whose farm is unusable" (exit 127) from "somebody renamed the datamitsu
// binary" (run the CLI).
//
// It deliberately does not stat the farm's manifest. The cases that most need to
// fail loudly — no manifest for this tree at all, or a manifest that will not
// parse — are exactly the ones a presence check would misread as "not a farm"
// and quietly turn into a CLI run holding a tool's argv.
//
// argv[0] alone cannot answer it. A shell resolving a command through PATH
// passes the *word the user typed*, not the path it found: bash, fish and dash
// all exec with argv[0] == "tofu" (verified in test/shell). So a bare name is
// resolved against PATH here, the same way the shell just did, and the first
// executable hit decides. A name that does carry a directory — `$DATAMITSU_FARM/tofu`,
// or `./tofu` — is answered from the path itself.
//
// Dispatch calls this once and stores the answer in d.throughFarm, because it
// must be taken before a rebake: the config change that made a manifest stale is
// often the one that dropped the app, and the rebake deletes the farm entry this
// reads.
// It also records which farm directory the invocation came through, because
// with two farms on PATH that directory is the only thing that says which of
// them answered the name. That is the whole of the precedence rule: a project
// farm sits ahead of a config farm on PATH, the shell resolved the name against
// PATH, and this reads back the answer it already gave. There is no resolution
// logic here to disagree with `echo $PATH`.
func (d *Dispatcher) computeThroughFarm() bool {
	if len(d.Args) == 0 {
		return false
	}
	argv0 := d.Args[0]
	resolved := argv0
	if !strings.ContainsRune(argv0, filepath.Separator) {
		resolved = d.lookPath(argv0)
	}
	if resolved == "" {
		return false
	}
	dir := filepath.Dir(filepath.Clean(resolved))
	if !d.isFarmDir(dir) {
		return false
	}
	d.invokedFarmDir = dir
	return true
}

// explicitConfigFarm reports whether this invocation must be answered from an
// explicit-config farm, and returns that farm's manifest.
//
// A config farm has no git root, so there is nothing to derive its manifest
// from: the location has to come from the invocation itself. That location is
// the farm directory the invocation arrived through, which is the entry PATH
// order already selected. With both a project farm and a config farm active
// this is what makes the project's pin win, without a precedence table. The
// directory is recognizable either by the cache namespace it sits in or — when
// the path this process computes for the cache disagrees with the one
// activation computed, from a DATAMITSU_CACHE_DIR or HOME that resolves
// differently — by the farm variable the activated shell exported; isFarmDir
// accepts both, so there is no second location to consult here.
//
// A farm directory with no readable manifest is a loud failure, never a
// fall-through. Falling through would send a machine-level tool name into git
// discovery — the exact step this origin exists to refuse — and, on a rebake,
// evaluate the config of whatever repository the shell was standing in. That
// holds for the namespace-recognized farm and the variable-recognized one
// alike: without the manifest the origin is unknowable, and the unknown case is
// the one that must not be guessed. A farm the invocation is known to be a
// machine-level one whose manifest decodes to some *other* origin is the same
// unknown case wearing a readable file, and gets the same loud failure.
//
// "Known to be machine-level" is two independent facts, and either one is
// enough: the config cache namespace the directory sits in, or the pair of
// variables the activated shell exported — DATAMITSU_FARM_CONFIG set, and
// DATAMITSU_FARM naming this very directory. The second exists because the first
// is judged against the cache root *this* process computes, which a
// DATAMITSU_CACHE_DIR or HOME that resolves differently makes disagree with the
// one activation computed; without it, exactly the invocations that already need
// the variable to be recognized as farm invocations at all would lose the
// origin check. The pair cannot name a project farm: a git-root activation
// clears DATAMITSU_FARM_CONFIG (see sourceActivation.staleVars), so the variable
// surviving alongside a matching DATAMITSU_FARM means the farm this shell
// activated is the explicit-config one.
//
// Reading a non-explicit origin as "a project farm answered" is therefore only
// allowed when neither fact holds, where it is the truth: some other farm on
// PATH, in a cache namespace this process cannot recognize.
//
// A directory textually inside the *project* namespace ends the search outright,
// before its manifest is even read. PATH selected that farm, so it is the only
// one allowed to answer, and an unreadable manifest there must become the
// git-root path's loud decline — not a silent hand-off to whatever config farm
// DATAMITSU_FARM happens to name, which would exec a different tool than the one
// PATH resolved. Returning early is also what keeps the ordinary invocation from
// decoding the same manifest twice, once here and once in loadManifest.
//
// handled=false means "this is not a config-farm invocation": either it came
// through a project farm, or it did not come through a farm at all. The
// git-root path answers both.
func (d *Dispatcher) explicitConfigFarm(name string) (string, sourcefarm.Manifest, bool, string) {
	if !d.throughFarm {
		return "", sourcefarm.Manifest{}, false, ""
	}
	dir := filepath.Clean(d.invokedFarmDir)

	if d.inNamespace(dir, projectsDirName) {
		return "", sourcefarm.Manifest{}, false, ""
	}

	manifestPath := filepath.Join(filepath.Dir(dir), env.ProjectManifestFileName)
	manifest, err := d.Load(manifestPath)
	knownConfigFarm := d.isConfigFarmDir(dir) || d.isActivatedConfigFarm(dir)
	if err == nil {
		if manifest.Origin != sourcefarm.OriginExplicitConfig {
			if knownConfigFarm {
				// The invocation says machine-level farm and the manifest says
				// something else, so the two disagree about the one fact this
				// branch exists to establish. A decodable manifest is no more
				// trustworthy here than a missing one — it takes a truncated
				// write, a hand edit or a build that wrote a different schema —
				// and falling through would do exactly what the missing-manifest
				// case already refuses: send a machine-level tool name into git
				// discovery, and evaluate the config of whatever repository the
				// shell happened to be standing in.
				return "", sourcefarm.Manifest{}, true, fmt.Sprintf(
					"datamitsu: %s: the explicit-config farm %s has a manifest recording a different origin (%q)\n"+
						"datamitsu: re-activate it with `datamitsu source bash --config <path>` (or your shell)",
					name, dir, manifest.Origin)
			}
			// A project farm answered the name. Its manifest records the
			// authoritative root, and the ordinary path is where that is read.
			return "", sourcefarm.Manifest{}, false, ""
		}
		return manifestPath, manifest, true, ""
	}
	if knownConfigFarm {
		return "", sourcefarm.Manifest{}, true, fmt.Sprintf(
			"datamitsu: %s: the explicit-config farm %s has no readable manifest: %v\n"+
				"datamitsu: re-activate it with `datamitsu source bash --config <path>` (or your shell)",
			name, dir, err)
	}
	// A farm only the activation variable identifies. Its origin is unknowable
	// without the manifest, so the message names neither one.
	return "", sourcefarm.Manifest{}, true, fmt.Sprintf(
		"datamitsu: %s: the farm %s has no readable manifest: %v\n"+
			"datamitsu: re-activate it with `datamitsu source bash` (add `--config <path>` for a machine-level farm)",
		name, dir, err)
}

// activatedFarm returns the farm directory this shell was activated with, or "".
// It reads the environment through the injected Environ for the same reason
// lookPathFrom does: the whole decision tree stays testable without touching the
// real process environment.
func (d *Dispatcher) activatedFarm() string {
	return d.envValue(env.SourceFarmVarName())
}

// isActivatedConfigFarm reports whether dir is the farm this shell activated
// *and* that activation was an explicit-config one, judged by the variables
// alone.
//
// It is the path-independent half of "this is a machine-level farm", and exists
// for the invocation isFarmDir already has to recognize by variable: one whose
// cache root this process computes differently than activation did, where the
// config namespace check cannot fire. Both variables are required, and the farm
// one must name this directory: a git-root activation exports DATAMITSU_FARM and
// clears DATAMITSU_FARM_CONFIG, so neither a stale chain from an earlier
// machine-level activation nor a config farm still sitting further down PATH can
// make a project farm's entry answer to this.
func (d *Dispatcher) isActivatedConfigFarm(dir string) bool {
	if d.envValue(env.SourceFarmConfigVarName()) == "" {
		return false
	}
	farm := d.activatedFarm()
	return farm != "" && filepath.Clean(farm) == filepath.Clean(dir)
}

// envValue returns the value of one variable in the injected environment, or "".
func (d *Dispatcher) envValue(name string) string {
	if d.Environ == nil {
		return ""
	}
	for _, kv := range d.Environ() {
		if key, value, ok := strings.Cut(kv, "="); ok && key == name {
			return value
		}
	}
	return ""
}

// lookPath returns the first executable named name on PATH, or "" — the file the
// shell just ran. It is the resolution step, not a permission check: a
// directory or a non-executable file is skipped exactly as a shell skips it.
func (d *Dispatcher) lookPath(name string) string {
	return d.lookPathFrom(name, false)
}

// lookPathOutsideFarm is lookPath with every farm directory skipped. It is what
// resolves an interpreter an entry names by bare word — a system-mode JVM's
// "java" — where taking the first PATH hit could find the farm's own entry of
// that name and exec this binary again instead of the interpreter.
func (d *Dispatcher) lookPathOutsideFarm(name string) string {
	return d.lookPathFrom(name, true)
}

func (d *Dispatcher) lookPathFrom(name string, skipFarm bool) string {
	var pathEnv string
	for _, kv := range d.Environ() {
		if key, value, ok := strings.Cut(kv, "="); ok && key == "PATH" {
			pathEnv = value
		}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, name)
		info, err := d.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		if skipFarm && d.underFarm(candidate) {
			continue
		}
		return candidate
	}
	return ""
}

// underFarm reports whether path names a file directly inside some farm
// directory of this cache.
//
// The comparison falls back to resolved paths when the textual one fails,
// because the two sides can describe the same directory differently: a path that
// went through EvalSymlinks (as the executable does) has /var rewritten to
// /private/var on macOS, while the cache root has not.
// The activated shell exported the farm directory it put on PATH, and that
// answer is taken here before the cache root is consulted. The two can disagree:
// a DATAMITSU_CACHE_DIR or HOME that resolves differently in this process than it
// did at activation makes a genuine farm entry look like a plain renamed
// datamitsu, and Dispatch would then decline and hand a tool's argv to the CLI —
// `tofu init` running `datamitsu init`. Trusting the variable is not a widening:
// it only ever adds "yes, through a farm", which is the loud-failure branch.
func (d *Dispatcher) underFarm(path string) bool {
	return d.isFarmDir(filepath.Dir(filepath.Clean(path)))
}

// isFarmDir is underFarm asked about the directory itself, which is what PATH
// entries are. Both farm namespaces count: a config farm is prepended to PATH
// exactly like a project farm, so every place that asks "is this a farm
// directory?" — the loud-failure branch, the PATH stripped from a spawned
// child, the refusal to spawn a farm entry as if it were datamitsu — must
// answer yes for it too.
func (d *Dispatcher) isFarmDir(dir string) bool {
	dir = filepath.Clean(dir)
	if filepath.Base(dir) != farmDirName {
		return false
	}
	if farm := d.activatedFarm(); farm != "" && filepath.Clean(farm) == dir {
		return true
	}
	return d.inNamespace(dir, projectsDirName) || d.inNamespace(dir, configsDirName)
}

// isConfigFarmDir reports whether dir is a farm directory in the config-farm
// namespace, judged by path alone.
//
// It is deliberately not "the manifest there says explicit-config": its one
// caller asks it precisely when there is no readable manifest, to tell a config
// farm whose manifest is missing from a directory that was never a config farm.
func (d *Dispatcher) isConfigFarmDir(dir string) bool {
	dir = filepath.Clean(dir)
	if filepath.Base(dir) != farmDirName {
		return false
	}
	return d.inNamespace(dir, configsDirName)
}

// inNamespace reports whether dir is {cache}/{namespace}/{hash}/bin.
//
// The comparison falls back to resolved paths when the textual one fails,
// because the two sides can describe the same directory differently: a path
// that went through EvalSymlinks (as the executable does) has /var rewritten to
// /private/var on macOS, while the cache root has not.
func (d *Dispatcher) inNamespace(dir, namespace string) bool {
	nsDir := filepath.Clean(filepath.Join(d.CacheRoot(), namespace))
	candidate := filepath.Dir(filepath.Dir(dir))
	if candidate == nsDir {
		return true
	}
	if d.EvalSymlinks == nil {
		return false
	}
	resolvedCandidate, errCandidate := d.EvalSymlinks(candidate)
	resolvedNS, errNS := d.EvalSymlinks(nsDir)
	return errCandidate == nil && errNS == nil && resolvedCandidate == resolvedNS
}

// decline handles every dead end reached before a manifest entry was found.
//
// Reached through a farm symlink, the name is one datamitsu put on PATH and must
// fail loudly rather than let PATH fall through to a system binary. Reached any
// other way, the executable is just datamitsu under another name and the normal
// CLI runs.
func (d *Dispatcher) decline(name, root, reason string) (int, bool) {
	if !d.throughFarm {
		return 0, false
	}
	msg := fmt.Sprintf("datamitsu: %s: %s", name, reason)
	if root != "" {
		msg += fmt.Sprintf(" (%s)", root)
	}
	return d.fail(msg + "\ndatamitsu: run `datamitsu source bash` (or your shell) in that repository to activate it"), true
}

// declineUnknown handles a name the manifest does not list. An excluded name
// reports why it was excluded: a name that silently does not work is
// undebuggable, and the reason is already recorded.
func (d *Dispatcher) declineUnknown(name string, manifest sourcefarm.Manifest) (int, bool) {
	if !d.throughFarm {
		return 0, false
	}
	for _, ex := range manifest.Excluded {
		if ex.Name == name {
			return d.fail(fmt.Sprintf("datamitsu: %s: excluded from source mode: %s (%s)\ndatamitsu: see `datamitsu source status`",
				name, ex.Reason, farmLabel(manifest))), true
		}
	}
	return d.fail(fmt.Sprintf("datamitsu: %s: not declared by this project (%s)\ndatamitsu: see `datamitsu source status`",
		name, farmLabel(manifest))), true
}

// farmLabel names a farm in a user-facing message. A project farm is its git
// root, which is what the user would cd to; an explicit-config farm has no root
// and is named by the config chain it was baked from, which is what the user
// would pass to --config.
func farmLabel(m sourcefarm.Manifest) string {
	if m.Root != "" {
		return m.Root
	}
	if len(m.ConfigPaths) > 0 {
		return strings.Join(m.ConfigPaths, ", ")
	}
	return "unknown farm"
}

// refreshHint is the command that rebuilds a farm from scratch, spelled for the
// farm's own origin — telling someone to run a command "in that repository"
// when there is no repository is an instruction that cannot be followed.
func refreshHint(m sourcefarm.Manifest) string {
	if m.Origin != sourcefarm.OriginExplicitConfig {
		return "run `" + ldflags.PackageName + " source refresh --force` in that repository"
	}
	// The chain is quoted when it has to be: this hint is printed when the farm
	// is already broken and cannot repair itself, so it is the one command the
	// user has to be able to paste verbatim, and an unquoted path with a space
	// in it would split into two arguments and name neither config.
	var b strings.Builder
	b.WriteString("run `")
	b.WriteString(ldflags.PackageName)
	b.WriteString(" source refresh")
	for _, p := range m.ConfigPaths {
		b.WriteString(" --config ")
		b.WriteString(quoteIfNeeded(p))
	}
	b.WriteString(" --force`")
	return b.String()
}

// quoteIfNeeded returns p as a bash word, left alone when it already is one.
//
// The ordinary case is an absolute path of unremarkable characters, and running
// every such path through shellquote.Bash would render it as $'...' — correct,
// but noise in a message whose whole job is to be read and pasted by a human.
// Anything outside the safe set goes through the real quoter.
func quoteIfNeeded(p string) string {
	if p == "" {
		return shellquote.Bash(p)
	}
	for i := range len(p) {
		c := p[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case strings.IndexByte("._/@%+:,-", c) >= 0:
		default:
			return shellquote.Bash(p)
		}
	}
	return p
}

// fail prints one line to stderr and returns the not-found exit code.
func (d *Dispatcher) fail(msg string) int {
	if d.Stderr != nil {
		_, _ = fmt.Fprintln(d.Stderr, msg)
	}
	return ExitNotFound
}

// warn prints one line to stderr without deciding an exit code.
func (d *Dispatcher) warn(msg string) {
	if d.Stderr != nil {
		_, _ = fmt.Fprintln(d.Stderr, msg)
	}
}

// datamitsuExe returns the absolute path of the running datamitsu, never a PATH
// lookup: the farm is on PATH, so a lookup could find a shimmed name and turn
// the rebake spawn into a loop.
//
// The path is additionally resolved through symlinks. A farm entry *is* a
// symlink to this binary, and os.Executable reports the path the process was
// invoked through rather than the file behind it on darwin — so spawning it
// unresolved re-enters dispatch with the tool's name in argv[0], and the install
// that was supposed to happen becomes an exec loop. Resolution is not merely an
// optimization here: a path still inside a farm after it is refused outright,
// because there is no safe way to spawn it.
func (d *Dispatcher) datamitsuExe() (string, error) {
	exe, err := d.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the datamitsu executable: %w", err)
	}
	if d.EvalSymlinks != nil {
		if resolved, resolveErr := d.EvalSymlinks(exe); resolveErr == nil {
			exe = resolved
		}
	}
	if d.underFarm(exe) {
		return "", fmt.Errorf("refusing to run %s: it is a source-mode farm entry, not the datamitsu executable", exe)
	}
	return exe, nil
}

// rebakeResult is what a rebake attempt leaves the dispatcher holding.
type rebakeResult struct {
	manifest sourcefarm.Manifest
	entry    sourcefarm.Entry

	// found is false when the farm no longer declares the invoked name — the
	// config change that made the manifest stale is the one that dropped it.
	found bool

	// retired is true when the rebake did not happen *and* the manifest still on
	// disk is one this build must not act on; see fallback.
	retired bool
}

// rebake re-runs the full resolution path for a stale manifest and re-reads the
// result. This is the one visible hiccup per config change; every invocation
// after it is back to the steady-state cost.
//
// A rebake that fails is not fatal. The previous farm is still on disk and still
// works, so the invocation continues with the stale entry after one line on
// stderr, exactly as materialization keeps the previous farm rather than
// replacing it with an empty one.
func (d *Dispatcher) rebake(manifestPath, name string, manifest sourcefarm.Manifest, entry sourcefarm.Entry) rebakeResult {
	exe, err := d.datamitsuExe()
	if err != nil {
		d.warn("datamitsu: " + err.Error())
		return d.fallback(manifest, entry)
	}
	if err := d.Spawn(d.spawnRequest(exe, manifest, "source", "refresh")); err != nil {
		d.warn("datamitsu: could not refresh the source-mode farm, using the previous one: " + err.Error())
		return d.fallback(manifest, entry)
	}
	reloaded, err := d.Load(manifestPath)
	if err != nil {
		d.warn("datamitsu: could not read the refreshed farm manifest, using the previous one: " + err.Error())
		return d.fallback(manifest, entry)
	}
	if !sourcefarm.UsableStale(reloaded) {
		// The spawn reported success but what is on disk is still a manifest this
		// build must not act on, so the refresh landed somewhere else — a farm
		// baked by a format-2 build from an explicit --config is the case that
		// exists: replaying its recorded flags now writes an explicit-config farm
		// in the other namespace and never touches this path. Serving the entries
		// back would be permanent: every later invocation repeats the same rebake,
		// finds the same manifest, and execs the same never-updated tools.
		d.warn("datamitsu: the refreshed farm manifest is still one this " + ldflags.PackageName + " cannot use")
		return rebakeResult{manifest: reloaded, retired: true}
	}
	refreshed, found := lookupEntry(reloaded, name)
	if !found {
		// The name was removed from the config by whatever change made the
		// manifest stale. Falling through to PATH here would run the system
		// binary the project just stopped pinning.
		return rebakeResult{manifest: reloaded}
	}
	return rebakeResult{manifest: reloaded, entry: refreshed, found: true}
}

// fallback decides whether the manifest that is still on disk may answer this
// invocation after a rebake that could not run.
//
// Usually it may: stale-but-working is the whole point of not making a failed
// rebake fatal. But a manifest this build cannot read the way it was written is
// a different case — sourcefarm.UsableStale names the three states that qualify,
// and the one this exists for is the retired format version. A format-1 entry
// decodes with no RequiredPaths, which entryHealthy then reads as "nothing else
// is required", so serving it back would exec a runtime-managed app whose
// wrapper exists and whose interpreter does not. That is exactly what the
// version bump retired, and it must not come back through the fallback door.
//
// The answer there is exit 127 rather than a silent downgrade: the farm is on
// PATH, so anything short of failing loudly runs the system binary or a
// half-installed one.
func (d *Dispatcher) fallback(manifest sourcefarm.Manifest, entry sourcefarm.Entry) rebakeResult {
	if !sourcefarm.UsableStale(manifest) {
		return rebakeResult{manifest: manifest, retired: true}
	}
	return rebakeResult{manifest: manifest, entry: entry, found: true}
}

// failRetired reports a farm that could neither be refreshed nor served.
func (d *Dispatcher) failRetired(name string, manifest sourcefarm.Manifest) int {
	return d.fail(fmt.Sprintf("datamitsu: %s: this project's source-mode farm was built by a different %s and could not be refreshed (%s)\n"+
		"datamitsu: %s",
		name, ldflags.PackageName, farmLabel(manifest), refreshHint(manifest)))
}

// ensureInstalled materializes an entry that has never been downloaded. This is
// lazy materialization: activation downloads nothing, and a tool arrives on its
// first real use.
//
// The install runs exactly once. When the entry already records where the tool
// will land — every kind whose store path is a pure function of its config —
// that path is used directly afterwards, and only an entry with no recorded
// command needs a second pass through the resolver.
//
// The decision is the recorded paths alone, deliberately ignoring
// entry.Installed (see entryHealthy for which paths those are). That
// flag is bake-time state: a lazy install writes the store, not the manifest,
// and nothing an install touches is in the watch set, so the manifest stays
// fresh with Installed=false forever. Consulting it would send every later
// invocation of a lazily installed tool back through a full `datamitsu install`
// child process — a config load per exec, against this package's ~10 ms budget.
// The store path is the truth; the flag is only a hint for `source status`.
func (d *Dispatcher) ensureInstalled(manifestPath, name string, manifest sourcefarm.Manifest, entry sourcefarm.Entry) (sourcefarm.Entry, error) {
	if d.entryHealthy(entry) {
		return entry, nil
	}

	exe, err := d.datamitsuExe()
	if err != nil {
		return entry, err
	}
	if err := d.Spawn(d.spawnRequest(exe, manifest, "install", name)); err != nil {
		return entry, fmt.Errorf("datamitsu: %s: install failed: %w", name, err)
	}

	if d.entryHealthy(entry) {
		return entry, nil
	}

	// No recorded location, or the install put it somewhere else: ask the full
	// resolver where it went and re-read the answer.
	if err := d.Spawn(d.spawnRequest(exe, manifest, "source", "refresh", "--force")); err != nil {
		return entry, fmt.Errorf("datamitsu: %s: could not refresh the farm after install: %w", name, err)
	}
	reloaded, err := d.Load(manifestPath)
	if err != nil {
		return entry, fmt.Errorf("datamitsu: %s: could not read the farm manifest after install: %w", name, err)
	}
	refreshed, found := lookupEntry(reloaded, name)
	if !found || !d.entryHealthy(refreshed) {
		// Asked against the re-resolved entry, so an install that produced a
		// wrapper without its interpreter — or a package without its runtime —
		// fails here instead of exec'ing something that runs unpinned.
		return entry, fmt.Errorf("datamitsu: %s: still not installed after install (%s)", name, farmLabel(reloaded))
	}
	return refreshed, nil
}

// execEntry stats the target and replaces this process with it.
func (d *Dispatcher) execEntry(entry sourcefarm.Entry, manifest sourcefarm.Manifest) (int, bool) {
	if entry.Command == "" {
		return d.fail(fmt.Sprintf("datamitsu: %s: no executable recorded for this app (%s)\ndatamitsu: see `datamitsu source status`",
			entry.Name, farmLabel(manifest))), true
	}

	// A command with no separator is an interpreter the config named by bare
	// word — a system-mode JVM runtime's "java". syscall.Exec does not search
	// PATH, so it is resolved here, the way the shell would have.
	command := entry.Command
	if !strings.ContainsRune(command, filepath.Separator) {
		resolved := d.lookPathOutsideFarm(command)
		if resolved == "" {
			return d.fail(fmt.Sprintf("datamitsu: %s: %s was not found on PATH\ndatamitsu: install it, or point this app's runtime at an absolute path (%s)",
				entry.Name, command, farmLabel(manifest))), true
		}
		command = resolved
	}

	// The target is stat'ed rather than left to execve's ENOENT. For an
	// interpreter-based target — a script starting with #!/usr/bin/env node —
	// ENOENT means either "the script is missing" or "the interpreter is
	// missing", and the two need different messages. A stat costs microseconds
	// against a process that has already cost milliseconds.
	if _, err := d.Stat(command); err != nil {
		return d.fail(fmt.Sprintf("datamitsu: %s: %s is missing: %v\ndatamitsu: %s",
			entry.Name, command, err, refreshHint(manifest))), true
	}

	// User argv is passed through untouched. `datamitsu exec actionlint
	// --version` cannot do this — cobra parses the tool's flags and rejects
	// them — and passing them verbatim is a correctness requirement of the
	// feature, not a performance detail.
	//
	// argv[0] is the name the user typed, not the store path being exec'd. Most
	// CLIs derive their usage line and error prefixes from argv[0], and
	// syscall.Exec takes the path to run separately, so there is no reason for
	// `actionlint --help` to print a content-addressed cache path as the program
	// name. Where an entry carries Args (a JVM app's `-jar`, say), the exec'd
	// program is the interpreter and its own argv[0] convention still holds, so
	// the entry's command name is used instead.
	argv := make([]string, 0, 1+len(entry.Args)+len(d.Args)-1)
	argv = append(argv, execArgv0(entry, d.Args))
	argv = append(argv, entry.Args...)
	argv = append(argv, d.Args[1:]...)

	if err := d.Exec(command, argv, mergeEnv(d.Environ(), entry.Env)); err != nil {
		d.warn(fmt.Sprintf("datamitsu: %s: cannot execute %s: %v", entry.Name, command, err))
		return ExitNotExecutable, true
	}
	// Unreachable on success: the process image has been replaced.
	return 0, true
}

// execArgv0 returns the value to pass as the exec'd program's argv[0].
//
// For a direct entry it is the name the user typed, so the tool reports itself
// under that name rather than under its store path. An entry with Args execs an
// interpreter that owns argv[0] by its own convention (`java -jar …` expects
// "java"), so the recorded command's base name is used there.
func execArgv0(entry sourcefarm.Entry, args []string) string {
	if len(entry.Args) > 0 {
		return filepath.Base(entry.Command)
	}
	if name := invokedName(args); name != "" {
		return name
	}
	return entry.Command
}

// prependPath puts the entry's runtime-owned directories in front of the
// inherited PATH, dropping any inherited copy of a directory it already names.
//
// The de-duplication is what keeps PATH bounded: a tool the farm runs may itself
// invoke another farm entry, and without it each hop would append the prefix
// again.
func prependPath(prefix, inherited string) string {
	if prefix == "" {
		return inherited
	}
	if inherited == "" {
		return prefix
	}
	dirs := filepath.SplitList(prefix)
	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		seen[dir] = struct{}{}
	}
	for _, dir := range filepath.SplitList(inherited) {
		if _, dup := seen[dir]; dup {
			continue
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

// installedPath returns the file whose existence decides whether an entry has
// been downloaded — the recorded artifact when the entry runs through an
// interpreter, the command otherwise. Stat'ing Command for a jvm entry would
// answer "is there a JVM?", which is true even when the JAR was never fetched
// and, for a system-mode runtime naming a bare "java", false even when it was.
func installedPath(entry sourcefarm.Entry) string {
	if entry.Artifact != "" {
		return entry.Artifact
	}
	return entry.Command
}

// entryHealthy reports whether every file this entry needs is on disk, which is
// what decides that the install can be skipped.
//
// One stat is not the whole question for a runtime-managed app, which is why the
// bake records RequiredPaths: a uv wrapper without its venv interpreter, a node
// .bin shim without its package or without the managed node it was pinned to,
// and a managed JVM app without its java are all states where the recorded
// command exists and running it is wrong. The installers already refuse to call
// those installed; this asks them the same way, from the recorded paths, without
// a config load.
//
// An entry with nothing recorded is not healthy — there is no path to run.
func (d *Dispatcher) entryHealthy(entry sourcefarm.Entry) bool {
	path := installedPath(entry)
	if path == "" {
		return false
	}
	if _, err := d.Stat(path); err != nil {
		return false
	}
	for _, required := range entry.RequiredPaths {
		if required == "" {
			continue
		}
		if _, err := d.Stat(required); err != nil {
			return false
		}
	}
	return true
}

// SpawnRequest describes the datamitsu child process a rebake or an install runs.
type SpawnRequest struct {
	// Exe is the resolved real datamitsu executable — never a PATH lookup; see
	// datamitsuExe.
	Exe string

	// Args is the child's argv[1:], the recorded config-chain flags included.
	Args []string

	// Dir is the working directory to run the child in, or "" to inherit.
	Dir string

	// Environ is the child's environment, or nil to inherit.
	Environ []string
}

// spawnRequest builds the child description for one datamitsu subcommand.
//
// Two properties of the child are set here rather than inherited, and both are
// correctness rather than hygiene:
//
//   - Every farm directory is removed from PATH. datamitsu runs some of its own
//     subprocesses by bare name, and a system-mode runtime declaring
//     `command: "uv"` is the documented shape for that, so with the farm in front
//     of PATH an app declared under one of those names interposes on them. For an
//     app of that runtime's own kind the interposition closes a loop: the install
//     spawn resolves the runtime through the farm, re-enters this shim under that
//     name, and spawns another install, without bound. The deny list cannot close
//     it, because which names are hazardous comes from the user's config.
//   - The working directory is the manifest's root. The child re-evaluates the
//     project's config, and facts() exposes cwd-derived inputs config JS may
//     branch on (facts().isMonorepo is true in a subdirectory and false at the
//     root). Inheriting the tool's cwd would make the baked farm depend on which
//     directory happened to trigger the rebake, while the staleness key — which
//     records no cwd — reported it fresh either way.
func (d *Dispatcher) spawnRequest(exe string, m sourcefarm.Manifest, sub ...string) SpawnRequest {
	return SpawnRequest{
		Exe:     exe,
		Args:    spawnArgs(m, sub...),
		Dir:     d.spawnDir(m),
		Environ: d.environOutsideFarms(),
	}
}

// spawnDir returns the manifest root when it is still a directory, and ""
// otherwise. A root that has been deleted or renamed must not turn a rebake into
// a chdir failure the user reads as "install failed": the child inherits the
// tool's cwd instead and reports whatever the real problem is.
//
// An explicit-config farm has no root, and inheriting the tool's cwd there is
// the one thing that must not happen: it would make a machine-level farm's
// contents depend on which directory the user happened to be standing in when
// the rebake fired, through every cwd-derived input facts() exposes. The
// directory holding the first config in the chain is used instead — recorded,
// stable, and the same on every rebake.
func (d *Dispatcher) spawnDir(m sourcefarm.Manifest) string {
	dir := m.Root
	if dir == "" && len(m.ConfigPaths) > 0 {
		dir = filepath.Dir(m.ConfigPaths[0])
	}
	if dir == "" {
		return ""
	}
	info, err := d.Stat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// environOutsideFarms returns this process's environment with every farm
// directory dropped from PATH. A PATH that names no directory at all is returned
// as an empty PATH rather than removed, because an absent PATH would make the
// child search a system default list this process never had.
func (d *Dispatcher) environOutsideFarms() []string {
	if d.Environ == nil {
		return nil
	}
	environ := d.Environ()
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || key != "PATH" {
			out = append(out, kv)
			continue
		}
		kept := make([]string, 0, len(filepath.SplitList(value)))
		for _, dir := range filepath.SplitList(value) {
			if dir != "" && d.isFarmDir(dir) {
				continue
			}
			kept = append(kept, dir)
		}
		out = append(out, "PATH="+strings.Join(kept, string(os.PathListSeparator)))
	}
	return out
}

// spawnArgs prefixes a datamitsu subcommand with the config-chain flags the farm
// was baked from.
//
// Both spawns here re-enter datamitsu to re-resolve the project's apps, and the
// binary they re-enter is the resolved real one — a wrapper that would have
// supplied `--before-config <shared config>` is deliberately bypassed. Replaying
// the recorded flags is what keeps the re-resolved chain the same chain: without
// them a rebake drops every app only the wrapper's config declares, and an
// install of one fails with "app not found".
func spawnArgs(m sourcefarm.Manifest, sub ...string) []string {
	prefix := m.ConfigArgs
	if m.Origin == sourcefarm.OriginExplicitConfig {
		prefix = explicitConfigArgs(m)
	}
	if len(prefix) == 0 {
		return sub
	}
	args := make([]string, 0, len(prefix)+len(sub))
	args = append(args, prefix...)
	return append(args, sub...)
}

// explicitConfigArgs returns the global flags that re-select an explicit-config
// farm's chain in a spawned child.
//
// Two things are guaranteed here and nowhere else. The chain is named
// explicitly — from the recorded flags when there are any, and from the
// recorded config paths otherwise, so a farm can still be rebuilt even if the
// flags were never written. And discovery is switched off, which is the trust
// boundary in its rebake form: the child runs `source refresh` or `install`
// with a working directory that may sit inside some repository, and without
// this flag it would discover that repository's config and merge the tools it
// declares into a machine-level farm the user activated from their shell rc.
func explicitConfigArgs(m sourcefarm.Manifest) []string {
	args := append([]string(nil), m.ConfigArgs...)
	if len(args) == 0 {
		for _, p := range m.ConfigPaths {
			args = append(args, "--config", p)
		}
	}
	if !slices.Contains(args, noAutoConfigFlag) {
		args = append(args, noAutoConfigFlag)
	}
	return args
}

// lookupEntry finds a manifest entry by name.
func lookupEntry(m sourcefarm.Manifest, name string) (sourcefarm.Entry, bool) {
	for _, e := range m.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return sourcefarm.Entry{}, false
}

// mergeEnv overlays an entry's environment on the inherited one. The result is
// sorted so an ran process sees a stable environment regardless of map
// iteration order.
//
// PATH is the one key that is prepended rather than replaced. A manifest is
// baked once and read by every shell afterwards, so a PATH captured at bake time
// is wrong by the time it is used: it pins whatever the baking shell happened to
// have, which for a per-shell version manager is a directory that stops existing
// when that shell exits. The entry records only the directories the runtime
// itself owns (a managed node's bin/, say) and they go in front of whatever the
// caller actually has.
func mergeEnv(base []string, overlay map[string]string) []string {
	if len(overlay) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(overlay))
	order := make([]string, 0, len(base)+len(overlay))
	set := func(k, v string) {
		if _, seen := merged[k]; !seen {
			order = append(order, k)
		}
		merged[k] = v
	}
	for _, kv := range base {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		set(key, value)
	}
	for k, v := range overlay {
		if k == "PATH" {
			v = prependPath(v, merged[k])
		}
		set(k, v)
	}
	sort.Strings(order)

	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+merged[k])
	}
	return out
}

// spawnDatamitsu runs datamitsu as a child for a rebake or an install.
//
// Its stdout is redirected to stderr: this process's stdout belongs to the tool
// the user asked for, and a progress line landing in `terragrunt output -json |
// jq` would corrupt it.
//
// Its stdin is closed for the mirror-image reason. This process's stdin belongs
// to the tool too — in `cat data.json | jq .` the pipe is the tool's input, and a
// child that read even one byte of it on a first-use install would silently eat
// data the tool was about to receive. Neither an install nor a bake prompts.
func spawnDatamitsu(req SpawnRequest) error {
	// The child inherits this process's lifetime rather than a context: there is
	// no deadline to impose on an install the user is waiting for, and cancelling
	// a bake midway is materialization's own problem, not the shim's.
	// #nosec G204 -- Exe comes from os.Executable and Args are fixed literals
	// plus the manifest's recorded config-chain flags.
	cmd := exec.CommandContext(context.Background(), req.Exe, req.Args...)
	cmd.Dir = req.Dir
	cmd.Env = req.Environ
	cmd.Stdin = nil
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run datamitsu %s: %w", strings.Join(req.Args, " "), err)
	}
	return nil
}
