package binmanager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/hashutil"
)

// dependencyLink is one entry of an app's dependency PATH directory: the command name the app
// finds on PATH, and the exact store executable behind it.
type dependencyLink struct {
	name   string
	target string
}

// dependencyLinks lists the binary apps in appName's dependsOn closure as PATH entries.
//
// The store names a binary by its config hash, so without this directory a tool that looks a
// helper up by name (a linter running `shellcheck`) cannot find it at all.
//
// The whole closure, not only direct dependencies: a dependency invoked by name through this
// directory runs straight from the store, not through datamitsu, so it never gets a PATH
// directory of its own and would miss its own helpers. Only native binary apps qualify, the same
// targets `${APP_BIN:…}` accepts: a runtime-managed app is a command plus arguments, which a PATH
// entry cannot carry, and a shell app already resolves through the inherited PATH.
func (bm *BinManager) dependencyLinks(appName string, resolve func(string) (string, error)) ([]dependencyLink, error) {
	closure, err := AppDependencyClosure(bm.mapOfApps, []string{appName})
	if err != nil {
		return nil, err
	}
	var links []dependencyLink
	for _, dep := range slices.Sorted(slices.Values(closure)) {
		if dep == appName || bm.mapOfApps[dep].Binary == nil {
			continue
		}
		path, err := resolve(dep)
		if err != nil {
			return nil, fmt.Errorf("dependency %q: %w", dep, err)
		}
		// A relative DATAMITSU_CACHE_DIR yields a relative store path, and a relative symlink target
		// resolves against the link's own directory, not the working directory.
		if path, err = filepath.Abs(path); err != nil {
			return nil, fmt.Errorf("dependency %q: %w", dep, err)
		}
		links = append(links, dependencyLink{name: commandName(dep), target: path})
	}
	return links, nil
}

// commandName is the file name under which a dependency is found on PATH. Windows resolves a bare
// command through PATHEXT, so the entry needs the extension a store binary's hash name lacks.
func commandName(app string) string {
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(app), ".exe") {
		return app + ".exe"
	}
	return app
}

// linkDependency creates one PATH entry. Windows creates symlinks only under Developer Mode or with
// elevation, which source mode may ask of a user but a plain `datamitsu exec` must not, so a hard
// link stands in there: it needs no privilege, the entry lives on the store's own volume, and the
// target never changes under a content-addressed name.
func linkDependency(target, entry string) error {
	err := os.Symlink(target, entry)
	if err == nil {
		return nil
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("symlink %s: %w", entry, err)
	}
	if linkErr := os.Link(target, entry); linkErr != nil {
		return fmt.Errorf("link %s: %w", entry, errors.Join(err, linkErr))
	}
	return nil
}

// dependencyPathDir is content-addressed by every name and target it holds, so a dependency bump
// produces a new directory instead of rewriting one that a running process may still be using.
func dependencyPathDir(links []dependencyLink) (string, error) {
	parts := make([][]byte, 0, 2*len(links))
	for _, link := range links {
		parts = append(parts, []byte(link.name), []byte(link.target))
	}
	// Absolute for the same reason as the targets: a relative PATH entry is resolved against
	// whatever directory the tool happens to run in.
	root, err := filepath.Abs(env.GetDependencyPathRoot())
	if err != nil {
		return "", fmt.Errorf("dependency PATH directory: %w", err)
	}
	return filepath.Join(root, hashutil.XXH3Multi(parts...)), nil
}

func dependencyPathEntries(dir string, links []dependencyLink) []string {
	entries := make([]string, 0, len(links))
	for _, link := range links {
		entries = append(entries, filepath.Join(dir, link.name))
	}
	return entries
}

// ensureDependencyPathDir materializes dir. A new directory is staged and renamed into place, so a
// concurrent run never observes it half-populated.
//
// An existing directory is never removed, even an incomplete one (a partial store restore): a tool
// started from it may still be running, and a second process repairing the same directory would
// otherwise delete the copy the first had just finished. The name fixes the content, so the missing
// entries are added in place instead, and one another process created meanwhile counts as done.
func ensureDependencyPathDir(dir string, links []dependencyLink) error {
	entries := dependencyPathEntries(dir, links)
	if allPathsExist(entries) {
		return nil
	}
	if _, err := os.Stat(dir); err == nil {
		return repairDependencyPathDir(dir, links)
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", parent, err)
	}
	staged, err := os.MkdirTemp(parent, ".staging-")
	if err != nil {
		return fmt.Errorf("stage dependency PATH directory: %w", err)
	}
	// After a successful rename the staged path no longer exists, so this only cleans up failures.
	defer func() { _ = os.RemoveAll(staged) }()
	// MkdirTemp creates 0700; the store is shared with other users of the same group (an image
	// run under an arbitrary UID in group 0), so the directory gets the store's usual mode.
	if err := os.Chmod(staged, 0o755); err != nil {
		return fmt.Errorf("stage dependency PATH directory: %w", err)
	}
	for _, link := range links {
		if err := linkDependency(link.target, filepath.Join(staged, link.name)); err != nil {
			return fmt.Errorf("link dependency %q: %w", link.name, err)
		}
	}
	if os.Rename(staged, dir) == nil {
		return nil
	}
	// Another run created the directory between the check above and the rename.
	return repairDependencyPathDir(dir, links)
}

func repairDependencyPathDir(dir string, links []dependencyLink) error {
	for _, link := range links {
		entry := filepath.Join(dir, link.name)
		if pathExists(entry) {
			continue
		}
		if err := linkDependency(link.target, entry); err != nil && !pathExists(entry) {
			return fmt.Errorf("repair dependency %q in %s: %w", link.name, dir, err)
		}
	}
	return nil
}

// addDependencyPath puts appName's binary dependencies on PATH under their app names and returns
// the directory's entries (nil when the app has no binary dependency).
//
// The exec path materializes the directory and composes the full PATH, as the runtime managers
// do. The resolve-only path never writes and records the directory as a PATH prefix, which the
// source-mode shim prepends to the caller's PATH.
func (bm *BinManager) addDependencyPath(appName string, cmdInfo *CommandInfo, resolve func(string) (string, error), materialize bool) ([]string, error) {
	links, err := bm.dependencyLinks(appName, resolve)
	if err != nil || len(links) == 0 {
		return nil, err
	}
	dir, err := dependencyPathDir(links)
	if err != nil {
		return nil, err
	}
	if materialize {
		if err := ensureDependencyPathDir(dir, links); err != nil {
			return nil, err
		}
	}
	if cmdInfo.Env == nil {
		cmdInfo.Env = make(map[string]string, 1)
	}
	inherited := ""
	if materialize {
		inherited = os.Getenv("PATH") //nolint:forbidigo // standard PATH for child process env, not a datamitsu env var
	}
	runtimePath, set := cmdInfo.Env["PATH"]
	cmdInfo.Env["PATH"] = composeDependencyPath(runtimePath, set, dir, inherited)
	return dependencyPathEntries(dir, links), nil
}

// composeDependencyPath places dir after the runtime-owned PATH prefix and before the inherited
// PATH. Runtime-owned entries keep precedence: a node or bun app finds its pinned interpreter
// through that prefix, and a dependency that happens to be named `node` must not displace it.
//
// runtimePath is what the runtime manager put in the app env: on the exec path its own prefix
// followed by the inherited PATH, on the resolve path the prefix alone (inherited is empty there,
// since the shim supplies the caller's PATH).
func composeDependencyPath(runtimePath string, set bool, dir, inherited string) string {
	join := func(parts ...string) string {
		kept := slices.DeleteFunc(parts, func(part string) bool { return part == "" })
		return strings.Join(kept, string(os.PathListSeparator))
	}
	if !set || runtimePath == inherited {
		return join(dir, inherited)
	}
	if inherited == "" {
		return join(runtimePath, dir)
	}
	if prefix, ok := strings.CutSuffix(runtimePath, string(os.PathListSeparator)+inherited); ok {
		return join(prefix, dir, inherited)
	}
	// A runtime PATH that does not end in the inherited one was composed some other way; keep it
	// whole and in front, so the runtime still wins.
	return join(runtimePath, dir)
}
