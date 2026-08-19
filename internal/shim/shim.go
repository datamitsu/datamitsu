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
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ldflags"
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

// farmDirName is the last path element of a farm directory, and projectsDirName
// its grandparent. Together they identify an invocation as having arrived
// through a farm: {cache}/projects/{hash}/bin/{name}.
const (
	farmDirName     = "bin"
	projectsDirName = "projects"
)

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

	Stat func(path string) (os.FileInfo, error)

	// Exec replaces this process with another program. It returns only on
	// failure.
	Exec func(path string, argv, environ []string) error

	// Spawn runs datamitsu as a child process for the two cases that need the
	// full resolution path: re-baking a stale farm and installing a tool that
	// has not been downloaded yet.
	Spawn func(exe string, args []string) error

	Stderr io.Writer

	// throughFarm records whether this invocation arrived through a farm. It is
	// computed once at the top of Dispatch, before anything can rebake the farm
	// out from under the answer; see computeThroughFarm.
	throughFarm bool
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

// Dispatch resolves the invoked name against the manifest for the current
// directory's git root and execs the recorded command.
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

	roots := d.discoverRoots()
	if len(roots) == 0 {
		// Outside a repository there is no manifest to consult, so the name
		// cannot be one of ours.
		return d.decline(name, "", "the current directory is not inside a git repository")
	}

	manifestPath, manifest, root, err := d.loadManifest(roots)
	if err != nil {
		// An activated shell that cds into a never-activated repository lands
		// here. The farm is deliberately not baked implicitly: baking evaluates
		// that repository's JavaScript, and typing a tool name is not consent to
		// run code from a tree the user has not activated.
		return d.decline(name, root, err.Error())
	}

	entry, found := lookupEntry(manifest, name)
	if !found {
		return d.declineUnknown(name, manifest)
	}

	// The freshness check is what makes `git checkout v2 && terragrunt plan`
	// work on a single line: it is a comparison of recorded stat tuples, so it
	// happens on every invocation rather than on a prompt hook that a compound
	// command never fires.
	if !d.Validate(manifest) {
		manifest, entry, found = d.rebake(manifestPath, name, manifest, entry)
		if !found {
			return d.declineUnknown(name, manifest)
		}
	}

	entry, err = d.ensureInstalled(manifestPath, name, entry)
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

// discoverRoots returns every working-tree root above the current directory,
// innermost first. It is a deliberately cheap approximation of the resolution
// facts.GetGitRoot performs: it only selects *which* manifest to open, and the
// manifest records the authoritative root the config loader resolved.
//
// Two properties of that resolution force the shape of this walk, and getting
// either wrong means a farm that exists is never found:
//
//   - The authoritative root is physical. facts resolves the working directory
//     through EvalSymlinks (and `git rev-parse --show-toplevel` reports a
//     physical path too), while os.Getwd honours $PWD and so reports the logical
//     path a shell cd'd through. On macOS every repository under /tmp or /var is
//     reached logically, so hashing the logical path keys a farm that was never
//     baked. The cwd is resolved here for the same reason.
//
//   - The authoritative root climbs past submodules to the topmost superproject
//     (see resolveGitRootViaGit). Stopping at the nearest `.git` would key a
//     submodule's own directory, where no farm was ever baked. Rather than
//     re-implementing git's superproject detection — which needs the outer
//     index to tell a submodule from an unrelated nested repository — every
//     candidate is returned and loadManifest takes the innermost one that has a
//     farm. A plain repository yields exactly one candidate and the extra
//     stat calls never happen.
func (d *Dispatcher) discoverRoots() []string {
	dir, err := d.Getwd()
	if err != nil {
		return nil
	}
	if d.EvalSymlinks != nil {
		if resolved, resolveErr := d.EvalSymlinks(dir); resolveErr == nil {
			dir = resolved
		}
	}

	var roots []string
	for {
		if _, err := d.Stat(filepath.Join(dir, ".git")); err == nil {
			roots = append(roots, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return roots
		}
		dir = parent
	}
}

// loadManifest opens the farm manifest for the innermost candidate root that has
// one, returning its path, its contents and the root it belongs to.
//
// The error names the innermost root, because that is the repository the user is
// standing in and the one `datamitsu source bash` would activate.
func (d *Dispatcher) loadManifest(roots []string) (string, sourcefarm.Manifest, string, error) {
	for _, root := range roots {
		manifestPath, err := d.ManifestPath(root)
		if err != nil {
			continue
		}
		manifest, err := d.Load(manifestPath)
		if err != nil {
			continue
		}
		return manifestPath, manifest, root, nil
	}
	return "", sourcefarm.Manifest{}, roots[0],
		errors.New("no source-mode farm has been created for this repository")
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
func (d *Dispatcher) computeThroughFarm() bool {
	if len(d.Args) == 0 {
		return false
	}
	argv0 := d.Args[0]
	if strings.ContainsRune(argv0, filepath.Separator) {
		return d.underFarm(argv0)
	}
	return d.underFarm(d.lookPath(argv0))
}

// activatedFarm returns the farm directory this shell was activated with, or "".
// It reads the environment through the injected Environ for the same reason
// lookPathFrom does: the whole decision tree stays testable without touching the
// real process environment.
func (d *Dispatcher) activatedFarm() string {
	if d.Environ == nil {
		return ""
	}
	name := env.SourceFarmVarName()
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
	dir := filepath.Dir(filepath.Clean(path))
	if filepath.Base(dir) != farmDirName {
		return false
	}
	if farm := d.activatedFarm(); farm != "" && filepath.Clean(farm) == dir {
		return true
	}
	projects := filepath.Clean(filepath.Join(d.CacheRoot(), projectsDirName))
	candidate := filepath.Dir(filepath.Dir(dir))
	if candidate == projects {
		return true
	}
	if d.EvalSymlinks == nil {
		return false
	}
	resolvedCandidate, errCandidate := d.EvalSymlinks(candidate)
	resolvedProjects, errProjects := d.EvalSymlinks(projects)
	return errCandidate == nil && errProjects == nil && resolvedCandidate == resolvedProjects
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
				name, ex.Reason, manifest.Root)), true
		}
	}
	return d.fail(fmt.Sprintf("datamitsu: %s: not declared by this project (%s)\ndatamitsu: see `datamitsu source status`",
		name, manifest.Root)), true
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

// rebake re-runs the full resolution path for a stale manifest and re-reads the
// result. This is the one visible hiccup per config change; every invocation
// after it is back to the steady-state cost.
//
// A rebake that fails is not fatal. The previous farm is still on disk and still
// works, so the invocation continues with the stale entry after one line on
// stderr, exactly as materialization keeps the previous farm rather than
// replacing it with an empty one.
func (d *Dispatcher) rebake(manifestPath, name string, manifest sourcefarm.Manifest, entry sourcefarm.Entry) (sourcefarm.Manifest, sourcefarm.Entry, bool) {
	exe, err := d.datamitsuExe()
	if err != nil {
		d.warn("datamitsu: " + err.Error())
		return manifest, entry, true
	}
	if err := d.Spawn(exe, []string{"source", "refresh"}); err != nil {
		d.warn("datamitsu: could not refresh the source-mode farm, using the previous one: " + err.Error())
		return manifest, entry, true
	}
	reloaded, err := d.Load(manifestPath)
	if err != nil {
		d.warn("datamitsu: could not read the refreshed farm manifest, using the previous one: " + err.Error())
		return manifest, entry, true
	}
	refreshed, found := lookupEntry(reloaded, name)
	if !found {
		// The name was removed from the config by whatever change made the
		// manifest stale. Falling through to PATH here would run the system
		// binary the project just stopped pinning.
		return reloaded, sourcefarm.Entry{}, false
	}
	return reloaded, refreshed, true
}

// ensureInstalled materializes an entry that has never been downloaded. This is
// lazy materialization: activation downloads nothing, and a tool arrives on its
// first real use.
//
// The install runs exactly once. When the entry already records where the tool
// will land — every kind whose store path is a pure function of its config —
// that path is used directly afterwards, and only an entry with no recorded
// command needs a second pass through the resolver.
func (d *Dispatcher) ensureInstalled(manifestPath, name string, entry sourcefarm.Entry) (sourcefarm.Entry, error) {
	if entry.Installed && installedPath(entry) != "" {
		if _, err := d.Stat(installedPath(entry)); err == nil {
			return entry, nil
		}
	}

	exe, err := d.datamitsuExe()
	if err != nil {
		return entry, err
	}
	if err := d.Spawn(exe, []string{"install", name}); err != nil {
		return entry, fmt.Errorf("datamitsu: %s: install failed: %w", name, err)
	}

	if path := installedPath(entry); path != "" {
		if _, err := d.Stat(path); err == nil {
			entry.Installed = true
			return entry, nil
		}
	}

	// No recorded location, or the install put it somewhere else: ask the full
	// resolver where it went and re-read the answer.
	if err := d.Spawn(exe, []string{"source", "refresh", "--force"}); err != nil {
		return entry, fmt.Errorf("datamitsu: %s: could not refresh the farm after install: %w", name, err)
	}
	reloaded, err := d.Load(manifestPath)
	if err != nil {
		return entry, fmt.Errorf("datamitsu: %s: could not read the farm manifest after install: %w", name, err)
	}
	refreshed, found := lookupEntry(reloaded, name)
	if !found || refreshed.Command == "" {
		return entry, fmt.Errorf("datamitsu: %s: still not installed after install (%s)", name, reloaded.Root)
	}
	return refreshed, nil
}

// execEntry stats the target and replaces this process with it.
func (d *Dispatcher) execEntry(entry sourcefarm.Entry, manifest sourcefarm.Manifest) (int, bool) {
	if entry.Command == "" {
		return d.fail(fmt.Sprintf("datamitsu: %s: no executable recorded for this app (%s)\ndatamitsu: see `datamitsu source status`",
			entry.Name, manifest.Root)), true
	}

	// A command with no separator is an interpreter the config named by bare
	// word — a system-mode JVM runtime's "java". syscall.Exec does not search
	// PATH, so it is resolved here, the way the shell would have.
	command := entry.Command
	if !strings.ContainsRune(command, filepath.Separator) {
		resolved := d.lookPathOutsideFarm(command)
		if resolved == "" {
			return d.fail(fmt.Sprintf("datamitsu: %s: %s was not found on PATH\ndatamitsu: install it, or point this app's runtime at an absolute path (%s)",
				entry.Name, command, manifest.Root)), true
		}
		command = resolved
	}

	// The target is stat'ed rather than left to execve's ENOENT. For an
	// interpreter-based target — a script starting with #!/usr/bin/env node —
	// ENOENT means either "the script is missing" or "the interpreter is
	// missing", and the two need different messages. A stat costs microseconds
	// against a process that has already cost milliseconds.
	if _, err := d.Stat(command); err != nil {
		return d.fail(fmt.Sprintf("datamitsu: %s: %s is missing: %v\ndatamitsu: run `datamitsu source refresh --force` in %s",
			entry.Name, command, err, manifest.Root)), true
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
func spawnDatamitsu(exe string, args []string) error {
	// The child inherits this process's lifetime rather than a context: there is
	// no deadline to impose on an install the user is waiting for, and cancelling
	// a bake midway is materialization's own problem, not the shim's.
	// #nosec G204 -- exe comes from os.Executable and args are fixed literals.
	cmd := exec.CommandContext(context.Background(), exe, args...)
	cmd.Stdin = nil
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run datamitsu %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
