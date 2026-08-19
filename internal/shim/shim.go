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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

// ExitNotFound is the exit code for a name the farm cannot run. 127 is the
// shell's own "command not found", which is exactly what happened from the
// user's point of view.
const ExitNotFound = 127

// ExitNotExecutable is the exit code for a target that exists but could not be
// executed, matching the shell's convention for the same condition.
const ExitNotExecutable = 126

// ownNames are the names under which the executable is datamitsu itself rather
// than a farm entry. Anything else is a candidate for dispatch — and, when no
// manifest claims it, falls back to the normal CLI, so a user who renames the
// binary keeps a working datamitsu.
var ownNames = map[string]struct{}{
	"datamitsu": {},
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
}

// New returns a Dispatcher wired to the real process.
func New() *Dispatcher {
	return &Dispatcher{
		Args:         os.Args,
		Getwd:        os.Getwd,
		Executable:   os.Executable,
		Environ:      os.Environ,
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
	if _, own := ownNames[name]; own {
		return 0, false
	}

	root, ok := d.discoverRoot()
	if !ok {
		// Outside a repository there is no manifest to consult, so the name
		// cannot be one of ours.
		return d.decline(name, "", "the current directory is not inside a git repository")
	}

	manifestPath, err := d.ManifestPath(root)
	if err != nil {
		return d.decline(name, root, err.Error())
	}
	manifest, err := d.Load(manifestPath)
	if err != nil {
		// An activated shell that cds into a never-activated repository lands
		// here. The farm is deliberately not baked implicitly: baking evaluates
		// that repository's JavaScript, and typing a tool name is not consent to
		// run code from a tree the user has not activated.
		return d.decline(name, root, "no source-mode farm has been created for this repository")
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

// discoverRoot walks up from the working directory looking for .git. This is a
// deliberately cheap approximation: it selects *which* manifest to open, and the
// manifest records the authoritative root the config loader resolved. The two
// disagree inside a submodule, where root discovery climbs to the topmost
// superproject, and the manifest is the one that is right.
func (d *Dispatcher) discoverRoot() (string, bool) {
	dir, err := d.Getwd()
	if err != nil {
		return "", false
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

// invokedThroughFarm reports whether argv[0] names a file inside a farm
// directory: {cache}/projects/{hash}/bin/{name}. It is what separates "a farm
// entry whose farm is unusable" (exit 127) from "somebody renamed the datamitsu
// binary" (run the CLI).
//
// It is a pure path comparison and deliberately does not stat the farm's
// manifest. The cases that most need to fail loudly — no manifest for this
// tree at all, or a manifest that will not parse — are exactly the ones a
// presence check would misread as "not a farm" and quietly turn into a CLI run
// with a tool's argv.
//
// A shell resolving a command through PATH execs the absolute path it found and
// passes it as argv[0], so the directory is available here.
func (d *Dispatcher) invokedThroughFarm() bool {
	if len(d.Args) == 0 {
		return false
	}
	dir := filepath.Dir(filepath.Clean(d.Args[0]))
	if filepath.Base(dir) != farmDirName {
		return false
	}
	projects := filepath.Join(d.CacheRoot(), projectsDirName)
	return filepath.Dir(filepath.Dir(dir)) == filepath.Clean(projects)
}

// decline handles every dead end reached before a manifest entry was found.
//
// Reached through a farm symlink, the name is one datamitsu put on PATH and must
// fail loudly rather than let PATH fall through to a system binary. Reached any
// other way, the executable is just datamitsu under another name and the normal
// CLI runs.
func (d *Dispatcher) decline(name, root, reason string) (int, bool) {
	if !d.invokedThroughFarm() {
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
	if !d.invokedThroughFarm() {
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
func (d *Dispatcher) datamitsuExe() (string, error) {
	exe, err := d.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the datamitsu executable: %w", err)
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
	if entry.Installed && entry.Command != "" {
		if _, err := d.Stat(entry.Command); err == nil {
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

	if entry.Command != "" {
		if _, err := d.Stat(entry.Command); err == nil {
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

	// The target is stat'ed rather than left to execve's ENOENT. For an
	// interpreter-based target — a script starting with #!/usr/bin/env node —
	// ENOENT means either "the script is missing" or "the interpreter is
	// missing", and the two need different messages. A stat costs microseconds
	// against a process that has already cost milliseconds.
	if _, err := d.Stat(entry.Command); err != nil {
		return d.fail(fmt.Sprintf("datamitsu: %s: %s is missing: %v\ndatamitsu: run `datamitsu source refresh --force` in %s",
			entry.Name, entry.Command, err, manifest.Root)), true
	}

	// User argv is passed through untouched. `datamitsu exec actionlint
	// --version` cannot do this — cobra parses the tool's flags and rejects
	// them — and passing them verbatim is a correctness requirement of the
	// feature, not a performance detail.
	argv := make([]string, 0, 1+len(entry.Args)+len(d.Args)-1)
	argv = append(argv, entry.Command)
	argv = append(argv, entry.Args...)
	argv = append(argv, d.Args[1:]...)

	if err := d.Exec(entry.Command, argv, mergeEnv(d.Environ(), entry.Env)); err != nil {
		d.warn(fmt.Sprintf("datamitsu: %s: cannot execute %s: %v", entry.Name, entry.Command, err))
		return ExitNotExecutable, true
	}
	// Unreachable on success: the process image has been replaced.
	return 0, true
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
func spawnDatamitsu(exe string, args []string) error {
	// The child inherits this process's lifetime rather than a context: there is
	// no deadline to impose on an install the user is waiting for, and cancelling
	// a bake midway is materialization's own problem, not the shim's.
	// #nosec G204 -- exe comes from os.Executable and args are fixed literals.
	cmd := exec.CommandContext(context.Background(), exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run datamitsu %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
