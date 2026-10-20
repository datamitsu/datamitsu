// Package prepare executes an app's declared one-time preparation step and
// places its result under {store}/.prepared/<app>/<key>/out.
//
// The step runs the app's own executable (or binPath from its install
// directory) directly, without a shell, after the app and its whole dependsOn
// closure are installed. The result is project-independent state shared by
// every project of the user and shipped in OCI bundles, so the engine is
// deliberately strict about what the step may see:
//
//   - PATH is "declared" by default: the runtime's own directories and the
//     dependency closure's binaries, nothing from the host.
//   - TMPDIR/TEMP/TMP and the XDG cache/data/state roots point into a
//     throwaway layout directory that is removed after the step.
//   - ${APP_PREPARED} is the only directory whose content survives.
//
// Readiness is the presence of a marker file written last; seeding an OCI
// bundle that carries the subtree must produce the same marker, after which
// the step never runs again.
package prepare

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpx"
)

// MarkerName is the readiness marker, written last inside the state
// directory (next to out/, never inside it: the app owns out/ entirely and
// must not be able to forge readiness by creating a file in its own output).
const MarkerName = "prepared.json"

// markerFormat is the on-disk schema of the marker file.
const markerFormat = 1

// marker carries only deterministic fields: it is part of an OCI layer and
// must not encode the producing machine.
type marker struct {
	Format   int    `json:"format"`
	Protocol string `json:"protocol"`
	App      string `json:"app"`
	Key      string `json:"key"`
}

// Limits mirror the OCI extraction limits (internal/ocibundle/extract.go):
// a state that exceeds them could be built but never shipped in a bundle.
const (
	maxFileBytes = 2 << 30 // 2 GiB per file
	maxTreeBytes = 8 << 30 // 8 GiB total
)

// layout is the throwaway staging tree under {store}/tmp.
type layout struct {
	root   string // {store}/tmp/prepare-<app>-<rand>
	result string // root/result — becomes {store}/.prepared/<app>/<key>
	out    string // result/out — ${APP_PREPARED} during the step
	work   string // root/work — the step's cwd
	tmp    string // root/tmp — TMPDIR and XDG roots during the step
	lease  string // root/lease — held while the layout is live
}

// Ready reports whether the app's prepared state exists: the marker file is
// present under the state directory for the key. A directory without the
// marker (an interrupted placement or a deleted marker, the documented
// repair) does not count.
func Ready(stateDir string) bool {
	_, err := os.Stat(filepath.Join(stateDir, MarkerName))
	return err == nil
}

// StateDir returns <preparedRoot>/<app>/<key>. The root comes from the
// caller (binmanager's PreparePath) so this package never imports it: the
// wiring layer owns the layout of the store.
func StateDir(preparedRoot, app, key string) string {
	return filepath.Join(preparedRoot, app, key)
}

// Run ensures the app's prepared state exists, running the step at most once.
// The caller must have installed the app and its whole dependsOn closure.
//
// The step's environment assembly, output validation and placement follow
// the contract in internal/config/app_prepare.go and the lifecycle plan;
// nothing here may consult the project.
func Run(ctx context.Context, req Request) error {
	stateDir := StateDir(req.PreparedRoot, req.App, req.Key)
	if Ready(stateDir) {
		return nil
	}

	if req.Network {
		if err := httpx.GuardOffline("prepare of " + strconv.Quote(req.App)); err != nil {
			return err
		}
	}

	// Singleflight within the process is the caller's concern (binmanager);
	// this lock serializes concurrent datamitsu processes placing the same
	// subtree, and a future store gc takes it before removing anything.
	release, err := lockState(ctx, stateDir)
	if err != nil {
		return err
	}
	defer release()

	if Ready(stateDir) {
		return nil // a concurrent process won the placement
	}

	lay, err := newLayout(req.Store)
	if err != nil {
		return err
	}
	defer lay.remove()

	if err := lay.runStep(ctx, req); err != nil {
		return err
	}
	if err := validateOutput(lay.out); err != nil {
		return fmt.Errorf("prepare of %q produced unusable output: %w", req.App, err)
	}
	if err := writeMarker(lay.result, req.Protocol, req.App, req.Key); err != nil {
		return err
	}
	return placeState(lay.result, stateDir, req.Store)
}

// Request is everything the engine needs about one step. BinManager builds
// it from the resolved config; the engine never reads the config itself.
type Request struct {
	App string
	Key string
	// Network is the step's declared network need; under DATAMITSU_OFFLINE
	// an unprepared network step is refused before the process starts.
	Network bool
	// PreparedRoot is {store}/.prepared and Protocol names the key protocol
	// version for the marker. Both come from binmanager, whose types this
	// package deliberately does not import (that would be an import cycle).
	PreparedRoot string
	Protocol     string
	Store        string // store root, for {store}/tmp
	// Command is the step's argv (the app's own command, or binPath resolved
	// by the caller to an absolute executable inside the install directory).
	Command []string
	// Env is the assembled step environment. The engine binds the symbolic
	// ${APP_PREPARED}, ${STEP_WORKDIR} and ${STEP_TMPDIR} values to the
	// staging layout; everything else passes through as given.
	Env []string
	// Stderr receives the step's output.
	Stderr io.Writer
	// Timeout bounds the step; zero means no limit. It is the effective
	// install timeout, separate from the installation of the app itself.
	Timeout time.Duration
}

// lockState serializes placement of one state directory across processes.
func lockState(ctx context.Context, stateDir string) (func(), error) {
	// The lock package keys by (storeRoot, subtree); stateDir is under the
	// store, so the store root is its prefix.
	storeRoot := filepath.Dir(filepath.Dir(filepath.Dir(stateDir)))
	return storeLock(ctx, storeRoot, stateDir)
}

func newLayout(store string) (*layout, error) {
	tmpRoot := filepath.Join(store, "tmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create store tmp root: %w", err)
	}
	// Build the layout in a staging namespace SweepStale never reads, and
	// only then rename it into the swept layout space: once the directory
	// bears the swept prefix it carries a complete lease, so a concurrent
	// sweep can never mistake a half-built layout for an abandoned one.
	// The staging prefix must NOT start with "prepare-": the sweeper matches
	// any that-prefixed directory by prefix, lease or not.
	stagingRoot := filepath.Join(tmpRoot, "preparing")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create prepare staging root: %w", err)
	}
	staging, err := os.MkdirTemp(stagingRoot, "layout-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create prepare staging directory: %w", err)
	}
	lay := &layout{
		root:   staging,
		result: filepath.Join(staging, "result"),
		out:    filepath.Join(staging, "result", "out"),
		work:   filepath.Join(staging, "work"),
		tmp:    filepath.Join(staging, "tmp"),
		lease:  filepath.Join(staging, "lease"),
	}
	for _, dir := range []string{lay.out, lay.work, lay.tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			lay.remove()
			return nil, fmt.Errorf("create prepare layout: %w", err)
		}
	}
	// The lease file marks the layout as owned by a live process: it records
	// the owner PID. Sweepers remove only abandoned layouts (see SweepStale),
	// never one whose lease belongs to a live process, because the step's
	// timeout may be unlimited.
	if err := os.WriteFile(lay.lease, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		lay.remove()
		return nil, fmt.Errorf("create prepare lease: %w", err)
	}
	// Publish: the rename is atomic (same filesystem), so a sweeper sees
	// either nothing or a complete lease. MkdirTemp's random suffix survives;
	// only the directory moves from the staging namespace to the swept one.
	published := filepath.Join(tmpRoot, "prepare-"+strings.TrimPrefix(staging, filepath.Join(tmpRoot, "preparing", "layout-")))
	if err := os.Rename(staging, published); err != nil {
		lay.remove()
		return nil, fmt.Errorf("publish prepare layout: %w", err)
	}
	// The staging root stays put: removing it races another builder's
	// MkdirAll/MkdirTemp pair (B creates the parent, A removes the now-empty
	// parent, B's MkdirTemp fails). An empty preparing/ directory is
	// harmless — the sweeper removes entries inside it older than 24h.
	lay.root = published
	lay.result = filepath.Join(published, "result")
	lay.out = filepath.Join(published, "result", "out")
	lay.work = filepath.Join(published, "work")
	lay.tmp = filepath.Join(published, "tmp")
	lay.lease = filepath.Join(published, "lease")
	return lay, nil
}

func (l *layout) remove() {
	_ = os.RemoveAll(l.root)
}

func (l *layout) runStep(ctx context.Context, req Request) error {
	stepCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		stepCtx, cancel = context.WithTimeout(stepCtx, req.Timeout)
	} else {
		stepCtx, cancel = context.WithCancel(stepCtx)
	}
	defer cancel()

	// The step's environment carries ${APP_PREPARED}, ${STEP_WORKDIR} and
	// ${STEP_TMPDIR} symbolically: the engine binds them to the staging
	// layout, so the caller never learns staging paths and the produced
	// output lands in the layout whatever the temp directory name is.
	env := make([]string, len(req.Env))
	for i, kv := range req.Env {
		env[i] = bindLayoutVars(kv, l.out, l.work, l.tmp)
	}
	cmd := exec.CommandContext(stepCtx, req.Command[0], req.Command[1:]...) //nolint:gosec // G204: the executable is the app's own managed install, resolved by binmanager from the validated config
	// The argv carries the layout tokens symbolically too
	// (--output=${APP_PREPARED}/... is a declared-args form): bind them
	// exactly like the environment entries.
	for i, arg := range cmd.Args {
		cmd.Args[i] = bindLayoutVars(arg, l.out, l.work, l.tmp)
	}
	cmd.Dir = l.work
	cmd.Env = env
	// A raw file for stdout, not io.Discard: a writer wrapper makes the
	// child inherit a pipe whose copy goroutine can outlive the immediate
	// process, and CommandContext alone kills only that process. WaitDelay
	// bounds the wait for such stragglers; killTree cancels the whole tree.
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("prepare of %q: open null output: %w", req.App, err)
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdout = devNull
	cmd.Stderr = req.Stderr
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		killTree(cmd.Process.Pid)
		return cmd.Process.Kill()
	}
	setProcessGroup(cmd)
	// Start and wait separately: the process must join its tree-cancellation
	// container (a process group on Unix, a job object on Windows) right
	// after Start, before the step can spawn descendants.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("prepare of %q: %w", req.App, err)
	}
	if err := trackProcessTree(cmd.Process.Pid); err != nil {
		// The process is already running: cancel it outright rather than
		// let an untracked tree escape the timeout contract.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("prepare of %q: %w", req.App, err)
	}
	waitErr := cmd.Wait()
	untrackProcessTree(cmd.Process.Pid)
	if waitErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("prepare of %q: %w", req.App, ctx.Err())
		}
		if stepCtx.Err() != nil {
			return fmt.Errorf("prepare of %q: timed out after %s", req.App, req.Timeout)
		}
		return fmt.Errorf("prepare of %q: %w", req.App, waitErr)
	}
	return nil
}

func writeMarker(resultDir, protocol, app, key string) error {
	data, err := json.Marshal(marker{Format: markerFormat, Protocol: protocol, App: app, Key: key})
	if err != nil {
		return fmt.Errorf("encode prepare marker: %w", err)
	}
	if err := os.WriteFile(filepath.Join(resultDir, MarkerName), data, 0o644); err != nil {
		return fmt.Errorf("write prepare marker: %w", err)
	}
	return nil
}

// bindLayoutVars replaces the layout placeholders in one environment entry
// (KEY=VALUE) with the staging paths, so the caller hands the engine
// symbolic values and never learns a staging path.
func bindLayoutVars(s, out, work, tmp string) string {
	return strings.NewReplacer(
		"${APP_PREPARED}", out,
		"${STEP_WORKDIR}", work,
		"${STEP_TMPDIR}", tmp,
	).Replace(s)
}

// placeState atomically moves the staged result into its final directory.
// An existing target without a marker is an interrupted earlier attempt (or
// a manually deleted marker, the documented repair): it is moved aside
// before the rename — never deleted in place, since a process may be
// reading it — and the aside copy is left for the sweepers. An existing
// target with a marker means a concurrent process won; our copy is dropped.
func placeState(staged, stateDir, store string) error {
	if err := os.MkdirAll(filepath.Dir(stateDir), 0o755); err != nil {
		return fmt.Errorf("create prepared state parent: %w", err)
	}
	tmpRoot := filepath.Join(store, "tmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return fmt.Errorf("create store tmp root: %w", err)
	}
	if _, statErr := os.Lstat(stateDir); statErr == nil {
		if Ready(stateDir) {
			// Lost the placement race: the winner's subtree is correct.
			if err := os.RemoveAll(staged); err != nil {
				return fmt.Errorf("drop lost placement copy: %w", err)
			}
			return nil
		}
		// A rename over a non-empty directory fails on most systems; move
		// the markerless remnant aside first.
		aside, asideErr := os.MkdirTemp(tmpRoot, "stale-prepared-*.tmp")
		if asideErr != nil {
			return fmt.Errorf("stage aside for stale prepared state: %w", asideErr)
		}
		if moveErr := os.Rename(stateDir, filepath.Join(aside, "state")); moveErr != nil {
			_ = os.RemoveAll(aside)
			if Ready(stateDir) {
				// Another process placed a ready state in the window.
				if err := os.RemoveAll(staged); err != nil {
					return fmt.Errorf("drop lost placement copy: %w", err)
				}
				return nil
			}
			return fmt.Errorf("move stale prepared state aside: %w", moveErr)
		}
	}
	if err := renameWithRetry(staged, stateDir); err != nil {
		// A concurrent winner may have placed a ready state meanwhile.
		if Ready(stateDir) {
			if err := os.RemoveAll(staged); err != nil {
				return fmt.Errorf("drop lost placement copy: %w", err)
			}
			return nil
		}
		return err
	}
	return nil
}

// renameWithRetry renames a directory, retrying transient failures. On
// Windows an antivirus or indexer may hold a freshly written file open for a
// moment, making the rename fail with a sharing violation; a short retry
// with backoff absorbs it.
func renameWithRetry(from, to string) error {
	var err error
	for attempt := range 6 {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Millisecond)
		}
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		if !isTransientRenameError(err) {
			return fmt.Errorf("rename %s to %s: %w", from, to, err)
		}
	}
	return fmt.Errorf("rename %s to %s: %w", from, to, err)
}

// SweepStale removes abandoned prepare layouts and stale aside directories
// under {store}/tmp. A layout is abandoned when its lease file no longer
// belongs to a live process: the lease records the owner PID, and a process
// is considered live when a process with that PID exists (best effort —
// PID reuse can only delay a sweep, never delete a live layout whose owner
// was replaced by an unrelated PID that happens to match, in which case the
// layout still has no holder and its content is unreachable).
func SweepStale(store string) {
	tmpRoot := filepath.Join(store, "tmp")
	entries, err := os.ReadDir(tmpRoot)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		dir := filepath.Join(tmpRoot, name)
		switch {
		case len(name) > len("prepare-") && name[:8] == "prepare-":
			lease := filepath.Join(dir, "lease")
			pidBytes, readErr := os.ReadFile(lease)
			if readErr == nil {
				if pid, convErr := strconv.Atoi(string(pidBytes)); convErr == nil && processAlive(pid) {
					continue
				}
			}
			_ = os.RemoveAll(dir)
		case len(name) > len("stale-prepared-") && name[:15] == "stale-prepared-":
			if info, statErr := e.Info(); statErr == nil && info.ModTime().Before(cutoff) {
				_ = os.RemoveAll(dir)
			}
		case name == "preparing":
			// Staging leftovers: a builder renames its directory out of here
			// within one run, so anything older than the cutoff belongs to a
			// crashed process. Young entries are left alone — a live builder
			// is mid-flight.
			stagingEntries, readErr := os.ReadDir(dir)
			if readErr != nil {
				continue
			}
			for _, se := range stagingEntries {
				if !se.IsDir() {
					continue
				}
				if info, statErr := se.Info(); statErr == nil && info.ModTime().Before(cutoff) {
					_ = os.RemoveAll(filepath.Join(dir, se.Name()))
				}
			}
		}
	}
}

// validateOutput walks the produced out/ tree and rejects anything that
// cannot ship in an OCI layer or survive a store-root move: absolute or
// escaping symlinks, special files, oversize files or trees.
func validateOutput(out string) error {
	// Canonicalize the root once: a symlinked cache root would otherwise
	// make every resolved in-tree link compare against a non-physical root
	// and fail as an escape.
	outRoot, err := filepath.EvalSymlinks(out)
	if err != nil {
		return fmt.Errorf("resolve prepared output root: %w", err)
	}
	var total int64
	err = filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", relDisplay(out, path), err)
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", relDisplay(out, path), err)
			}
			if filepath.IsAbs(target) {
				return fmt.Errorf("%s: absolute symlink %q", relDisplay(out, path), target)
			}
			// Two checks. The lexical one catches direct escapes against the
			// root as walked (the walk yields non-canonical paths when an
			// ancestor is a symlink; comparing them against the canonical
			// root would reject honest links). The component-wise one walks
			// the target exactly as the kernel does — every symlink component
			// is resolved BEFORE a later `..` is applied, so alias -> . with
			// chain -> alias/../outside lands outside the tree although a
			// lexical Clean keeps it inside — and stops at the first missing
			// component (a dangling tail is fine — OCI layers carry dangling
			// links — but the components that do exist must stay inside). The
			// result is compared against the canonical root.
			lexPath := filepath.Clean(filepath.Join(filepath.Dir(path), target))
			if escapesRoot(out, lexPath) {
				return fmt.Errorf("%s: symlink %q escapes the prepared state", relDisplay(out, path), target)
			}
			resolved, ok := resolveChain(filepath.Dir(path), target)
			if !ok || escapesRoot(outRoot, resolved) {
				return fmt.Errorf("%s: symlink %q escapes the prepared state through a symlink chain", relDisplay(out, path), target)
			}
		case info.Mode()&os.ModeDevice != 0, info.Mode()&os.ModeNamedPipe != 0,
			info.Mode()&os.ModeSocket != 0, info.Mode()&fs.ModeIrregular != 0:
			return fmt.Errorf("%s: special file (%s)", relDisplay(out, path), info.Mode())
		case info.Mode().IsRegular():
			if info.Size() > maxFileBytes {
				return fmt.Errorf("%s: file is %d bytes, over the %d byte bundle limit", relDisplay(out, path), info.Size(), maxFileBytes)
			}
			total += info.Size()
			if total > maxTreeBytes {
				return fmt.Errorf("prepared state exceeds the %d byte bundle limit", maxTreeBytes)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk prepared output: %w", err)
	}
	return nil
}

// escapesRoot reports whether a lexically-clean path lies outside the
// canonical output root. A path equal to the root itself stays inside.
func escapesRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveChain walks a symlink target the way the kernel does, component by
// component: each component is appended to the accumulated path, and if it
// is itself a symlink its target replaces it (recursively — a link to a
// link is followed) BEFORE any later `..` is applied, which is exactly the
// order the kernel resolves. Resolution stops at the first component that
// does not exist; the missing suffix is preserved verbatim (a dangling
// tail), but the components that do exist have already been fully checked.
func resolveChain(dir, target string) (string, bool) {
	cur := dir
	// A queue of UNCLEANED components: no Join, no Clean. Join would
	// collapse "dot/../outside" to "outside" before dot is resolved as a
	// symlink — but the kernel resolves dot first and only then applies
	// "..", landing outside the tree. Each component is processed in order;
	// a symlink component prepends its own (equally uncleaned) target
	// components to the queue. Both separators are split: a Windows link
	// target may carry backslashes, and an unsplit "alias\\..\\outside"
	// would collapse exactly the way Join does.
	queue := splitTargetComponents(target)
	links := 0
	for len(queue) > 0 {
		if links > 40 {
			// A symlink loop or a pathological chain: refuse like the
			// kernel's ELOOP.
			return "", false
		}
		comp := queue[0]
		queue = queue[1:]
		switch comp {
		case "", ".":
			// Skip.
		case "..":
			cur = filepath.Dir(cur)
		default:
			next := filepath.Join(cur, comp)
			info, err := os.Lstat(next)
			if err != nil {
				// Missing component: the tail is dangling. Everything up
				// to here has been resolved; canonicalize the existing
				// prefix so the caller's canonical-root comparison is
				// against like forms, and append the rest verbatim.
				prefix := cur
				if canonical, cerr := filepath.EvalSymlinks(prefix); cerr == nil {
					prefix = canonical
				}
				return filepath.Join(append([]string{prefix, comp}, queue...)...), true
			}
			if info.Mode()&os.ModeSymlink != 0 {
				linkTarget, err := os.Readlink(next)
				if err != nil {
					return "", false
				}
				links++
				if filepath.IsAbs(linkTarget) {
					cur = linkTarget
				} else {
					// A relative link is resolved against the link's own
					// directory, and its target's components are processed
					// BEFORE any remaining component of the outer path.
					cur = filepath.Dir(next)
				}
				queue = append(splitTargetComponents(linkTarget), queue...)
				continue
			}
			cur = next
		}
	}
	// Canonicalize the completed traversal so a caller comparing against a
	// canonical root sees like forms (walk paths may differ from the
	// canonical ones by a symlinked ancestor).
	if canonical, cerr := filepath.EvalSymlinks(cur); cerr == nil {
		return canonical, true
	}
	return cur, true
}

// splitTargetComponents splits a symlink target into uncleaned components.
// "/" is a separator everywhere; on Windows, where a native target spells
// "alias\\..\\outside", the backslash is a separator too (an unsplit
// backslashed target would hand filepath.Join a single component and
// collapse exactly the way Clean does). On Unix a backslash is an ordinary
// filename character and stays part of the component.
func splitTargetComponents(target string) []string {
	if runtime.GOOS == "windows" {
		return strings.FieldsFunc(target, func(r rune) bool {
			return r == '/' || r == '\\'
		})
	}
	return strings.Split(target, "/")
}

func relDisplay(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
