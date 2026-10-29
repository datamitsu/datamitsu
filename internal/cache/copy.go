// CopyProject moves a project's execution cache to another project root —
// what a fresh git worktree needs to keep the warm cache of the tree it came
// from. The per-root namespace is {cacheDir}/projects/{xxh3(root)}, so a new
// root is cold by construction; everything inside toolstate.msgpack is keyed by
// root-relative paths and content hashes, which is why moving the file is both
// enough and safe.

package cache

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/shamaton/msgpack/v2"
)

var (
	// ErrNoSourceCache reports a source project with nothing to copy: neither
	// toolstate.msgpack nor (with CopyOptions.ToolCaches) a cache/ tree.
	ErrNoSourceCache = errors.New("no datamitsu cache found for the source project")
	// ErrSameProject reports source and destination roots that are the same
	// project, where a copy could only overwrite the cache with itself.
	ErrSameProject = errors.New("source and destination are the same project")
)

// ToolstateStatus explains what happened to the destination's toolstate.msgpack.
type ToolstateStatus string

const (
	// ToolstateCopied is set when the destination had no readable toolstate
	// and the source's was imported verbatim (only ProjectPath rewritten).
	ToolstateCopied ToolstateStatus = "copied"
	// ToolstateMerged is set when the destination had a readable toolstate
	// under the same invalidation key and the source's entries were imported
	// into it.
	ToolstateMerged ToolstateStatus = "merged"
	// ToolstateKeptIncompatible is set when the two toolstate files carry
	// different invalidation keys — different configuration, datamitsu version
	// or selected tools — so the destination's was left untouched; a run under
	// the source's configuration would reset it anyway.
	ToolstateKeptIncompatible ToolstateStatus = "kept-incompatible"
	// ToolstateSkippedInvalid is set when the source toolstate exists but does
	// not decode; the binary reading it would discard it too.
	ToolstateSkippedInvalid ToolstateStatus = "skipped-invalid"
	// ToolstateAbsent is set when the source namespace has no toolstate at
	// all (it may still hold tool caches, which is not an error under
	// ToolCaches).
	ToolstateAbsent ToolstateStatus = "absent"
)

// CopyOptions shapes CopyProject.
type CopyOptions struct {
	// DryRun decides everything a real copy would and writes nothing.
	DryRun bool
	// ToolCaches additionally copies the cache/ tree of per-tool cache
	// directories. Their contents belong to the tools, not to datamitsu, and
	// carry no portability contract: on diverged branches an add-only mix can
	// assemble state neither tree had, and a tool trusting it may report a
	// false clean that datamitsu would then cache as its own pass. The option
	// is opt-in for same-commit worktrees and users who accept that risk.
	ToolCaches bool
}

// CopyReport is what CopyProject did, in the terms its caller prints.
type CopyReport struct {
	Toolstate        ToolstateStatus
	ImportedEntries  int      // Entries that arrived from the source
	ImportedVerdicts int      // Verdicts that arrived from the source
	ToolCacheFiles   int64    // tool-cache files copied (ToolCaches only)
	ToolCacheBytes   int64    // bytes of those files
	SkippedExisting  int64    // tool-cache names the destination already had
	SkippedSpecial   int64    // tool-cache symlinks, other non-regular files, and directories refused for symlinked destination components
	Warnings         []string // human-readable notes, printed verbatim
}

// CopyProject copies the execution cache of srcRoot into dstRoot's namespace.
//
// Only toolstate.msgpack (and, opted in, the cache/ tree) is copied. The
// source-mode farm and the evaluated-config cache are deliberately left out:
// both are keyed or described by absolute paths of their own root and are cheap
// to rebuild on the next run.
//
// Concurrency: toolstate is published through writeFileAtomic, so a reader sees
// the whole old or the whole new file. A lost update between this copy and a
// run saving in the destination concurrently is possible in exactly the class
// mergeFromDisk already documents for any two writers; a copy made before the
// destination's first run — the workflow the command exists for — cannot race.
// The worst outcome of a race is a colder cache, never a corrupt file. No new
// lock protocol is introduced.
func CopyProject(cacheDir, srcRoot, dstRoot string, opts CopyOptions) (CopyReport, error) {
	if srcRoot == dstRoot {
		return CopyReport{}, ErrSameProject
	}

	srcDir := filepath.Join(cacheDir, "projects", env.HashProjectPath(srcRoot))
	dstDir := filepath.Join(cacheDir, "projects", env.HashProjectPath(dstRoot))

	srcHasState, err := regularFileExists(filepath.Join(srcDir, cacheFileName))
	if err != nil {
		return CopyReport{}, fmt.Errorf("failed to inspect the source cache: %w", err)
	}
	srcHasToolCaches, err := dirExists(filepath.Join(srcDir, toolCacheDirName))
	if err != nil {
		return CopyReport{}, fmt.Errorf("failed to inspect the source cache: %w", err)
	}
	if !srcHasState && (!opts.ToolCaches || !srcHasToolCaches) {
		return CopyReport{}, ErrNoSourceCache
	}

	// Every write below creates its way through parent directories
	// (writeFileAtomic's MkdirAll, the tool-cache copier's per-component
	// creation), and none of them stops at a symlink. An alias pointing
	// projects/<dstHash> at another namespace — the source's, the store's,
	// anywhere — would therefore turn the copy into a rewrite of the aliased
	// cache, the source's included. Refuse before anything is written; the
	// guard inspects the existing components only, so a dry run decides the
	// same way without creating the namespace.
	blocked, err := dirBlockedBySymlink(cacheDir, dstDir)
	if err != nil {
		return CopyReport{}, fmt.Errorf("failed to inspect the destination cache directory: %w", err)
	}
	if blocked {
		return CopyReport{}, fmt.Errorf(
			"refusing to copy into %s: a symlink in its path points at another location", dstDir)
	}
	if !opts.DryRun {
		if err := ensureDirNoLinks(cacheDir, dstDir); err != nil {
			return CopyReport{}, fmt.Errorf("failed to create the destination cache directory: %w", err)
		}
	}
	report := CopyReport{}
	if srcHasState {
		if err := copyToolstate(filepath.Join(srcDir, cacheFileName), filepath.Join(dstDir, cacheFileName), dstRoot, opts.DryRun, &report); err != nil {
			return report, err
		}
	} else {
		report.Toolstate = ToolstateAbsent
	}
	if opts.ToolCaches && srcHasToolCaches {
		if err := copyToolCaches(filepath.Join(srcDir, toolCacheDirName), filepath.Join(dstDir, toolCacheDirName), opts.DryRun, &report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// toolCacheDirName is the per-tool cache subtree inside a project namespace,
// as GetProjectCachePath lays it out ("cache" is the literal segment there).
const toolCacheDirName = "cache"

// copyToolstate moves one project's toolstate into another project's file,
// under the import policy of CopyProject. Unreadable bytes are a warning, not
// an error; a filesystem that cannot be read at all is.
func copyToolstate(srcPath, dstPath, dstRoot string, dryRun bool, report *CopyReport) error {
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("failed to read the source toolstate: %w", err)
	}
	var srcData File
	if err := msgpack.Unmarshal(srcBytes, &srcData); err != nil {
		report.Toolstate = ToolstateSkippedInvalid
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("source toolstate is unreadable and was not copied: %v", err))
		return nil
	}

	dstBytes, err := os.ReadFile(dstPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read the destination toolstate: %w", err)
	}

	var dstData File
	dstPresent := err == nil
	dstDecodes := dstPresent && msgpack.Unmarshal(dstBytes, &dstData) == nil
	switch {
	case !dstDecodes:
		// A destination that is absent, or present but undecodable, gets the
		// source's file verbatim. An undecodable one is what the next run
		// there would reset to fresh; the copy replaces it deliberately and
		// says so.
		imported := srcData
		imported.ProjectPath = dstRoot
		if !dryRun {
			if err := writeToolstate(dstPath, &imported); err != nil {
				return err
			}
		}
		report.Toolstate = ToolstateCopied
		report.ImportedEntries = len(srcData.Entries)
		report.ImportedVerdicts = len(srcData.Verdicts)
		if dstPresent {
			report.Warnings = append(report.Warnings,
				"destination toolstate was unreadable and has been replaced")
		}
		return nil
	case srcData.InvalidationKey != dstData.InvalidationKey:
		report.Toolstate = ToolstateKeptIncompatible
		return nil
	}

	// Import policy — deliberately not mergeFromDisk. That function reconciles
	// one process's memory with the disk it started from; this one imports a
	// foreign file, so the rules differ where the two would not agree:
	//
	//   - an entry absent from the destination is imported whole. Tombstones
	//     live in process memory only, so this can resurrect an entry the
	//     destination pruned or deleted. That is safe: a hit additionally
	//     requires the entry's content hash to match the file on disk, which a
	//     deleted file cannot, and daily pruning removes it again.
	//   - same relPath and same ContentHash: the pass lists are combined — both
	//     were earned over identical bytes.
	//   - same relPath, different ContentHash: the destination entry stays
	//     whole. Passes of one hash must never be attached to another's.
	//   - a verdict key collision keeps whichever entry was validated more
	//     recently (a tie keeps the destination's), so a verdict's age, and
	//     with it its TTL, stays honest; an imported verdict keeps its
	//     original ValidatedAt, expired included.
	//   - Version, LastPruned and the invalidation key stay the destination's.
	importToolstate(&srcData, &dstData, dstRoot, report)
	if !dryRun {
		if err := writeToolstate(dstPath, &dstData); err != nil {
			return err
		}
	}
	report.Toolstate = ToolstateMerged
	return nil
}

// importToolstate folds src's entries and verdicts into dst per the policy
// copyToolstate documents, counting what arrived from src in report.
func importToolstate(src *File, dst *File, dstRoot string, report *CopyReport) {
	if dst.Entries == nil {
		dst.Entries = make(map[string]FileEntry)
	}
	for path, srcEntry := range src.Entries {
		dstEntry, ok := dst.Entries[path]
		if !ok {
			dst.Entries[path] = srcEntry
			report.ImportedEntries++
			continue
		}
		if dstEntry.ContentHash != srcEntry.ContentHash {
			continue
		}
		merged, added := unionEntryPasses(dstEntry, srcEntry)
		if added {
			dst.Entries[path] = merged
			report.ImportedEntries++
		}
	}

	if dst.Verdicts == nil {
		dst.Verdicts = make(map[string]VerdictEntry)
	}
	for key, srcVerdict := range src.Verdicts {
		dstVerdict, ok := dst.Verdicts[key]
		if !ok {
			dst.Verdicts[key] = srcVerdict
			report.ImportedVerdicts++
			continue
		}
		if srcVerdict.ValidatedAt.After(dstVerdict.ValidatedAt) {
			dst.Verdicts[key] = srcVerdict
			report.ImportedVerdicts++
		}
	}

	dst.ProjectPath = dstRoot
}

// unionEntryPasses merges two entries earned over the same content hash,
// reporting whether src added anything dst did not already have.
func unionEntryPasses(dst, src FileEntry) (FileEntry, bool) {
	added := false
	for _, tool := range src.Lint {
		if !slices.Contains(dst.Lint, tool) {
			dst.Lint = append(dst.Lint, tool)
			added = true
		}
	}
	for _, tool := range src.Fix {
		if !slices.Contains(dst.Fix, tool) {
			dst.Fix = append(dst.Fix, tool)
			added = true
		}
	}
	return dst, added
}

func writeToolstate(path string, f *File) error {
	data, err := msgpack.Marshal(f)
	if err != nil {
		return fmt.Errorf("failed to encode toolstate: %w", err)
	}
	return writeFileAtomic(path, data)
}

// readToolstate decodes one toolstate file. A missing file returns an error
// os.IsNotExist matches; anything else is a decode failure.
func readToolstate(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("failed to read toolstate: %w", err)
	}
	var f File
	if err := msgpack.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("failed to decode toolstate: %w", err)
	}
	return f, nil
}

// symlinkDirError reports a destination directory component that is a symlink.
// The copy must stay inside its own namespace: a link planted there — to the
// source namespace, the store, anywhere — would otherwise receive the copy's
// writes.
type symlinkDirError struct{ path string }

func (e *symlinkDirError) Error() string {
	return "refusing to copy through the symlink " + e.path
}

// copyToolCaches deep-copies the per-tool cache subtree with no-replace
// semantics: a name the destination already has is counted and skipped, so a
// concurrently created file is never replaced. Symlinks and other non-regular
// source files are skipped rather than followed, and a destination directory
// component that is a symlink refuses the whole subtree — components are
// checked as they are created, in the same lstat terms a dry run decides with.
// Each file is published under its own name with no intermediate temp file; on
// a write error the partial copy of that one file is removed, and re-running
// converges.
func copyToolCaches(srcDir, dstDir string, dryRun bool, report *CopyReport) error {
	// The namespace directory itself is a destination component like any
	// other: create it from the cache root, refusing to pass through a link.
	cacheRoot := filepath.Dir(filepath.Dir(dstDir))
	blocked, err := dirBlockedBySymlink(cacheRoot, dstDir)
	if err != nil {
		return fmt.Errorf("failed to inspect the destination tool-cache directory: %w", err)
	}
	if blocked {
		report.SkippedSpecial++
		return nil
	}
	if !dryRun {
		if err := ensureDirNoLinks(cacheRoot, dstDir); err != nil {
			return fmt.Errorf("failed to create tool-cache directory: %w", err)
		}
	}
	if err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("failed to make the tool-cache path relative: %w", err)
		}
		target := filepath.Join(dstDir, rel)

		if d.IsDir() {
			blocked, err := dirBlockedBySymlink(dstDir, target)
			if err != nil {
				return fmt.Errorf("failed to inspect the destination tool-cache directory: %w", err)
			}
			if blocked {
				report.SkippedSpecial++
				return filepath.SkipDir
			}
			if !dryRun {
				if err := ensureDirNoLinks(dstDir, target); err != nil {
					return fmt.Errorf("failed to create tool-cache directory: %w", err)
				}
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("failed to inspect the tool-cache entry: %w", err)
		}
		if !d.Type().IsRegular() {
			report.SkippedSpecial++
			return nil
		}
		if dryRun {
			if _, err := os.Lstat(target); err == nil {
				report.SkippedExisting++
			} else {
				report.ToolCacheFiles++
				report.ToolCacheBytes += info.Size()
			}
			return nil
		}
		copied, err := copyFileNoReplace(path, target, info.Mode().Perm())
		switch {
		case err != nil:
			return err
		case copied:
			report.ToolCacheFiles++
			report.ToolCacheBytes += info.Size()
		default:
			report.SkippedExisting++
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to walk the source tool caches: %w", err)
	}
	return nil
}

// copyFileNoReplace creates target only if it does not exist, copies src into
// it preserving the mode, and reports whether the copy happened.
func copyFileNoReplace(src, target string, perm fs.FileMode) (bool, error) {
	in, err := os.Open(src)
	if err != nil {
		return false, fmt.Errorf("failed to read tool-cache file: %w", err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to create tool-cache file: %w", err)
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(target) // never leave our own partial file behind
		return false, fmt.Errorf("failed to copy tool-cache file: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(target)
		return false, fmt.Errorf("failed to close tool-cache file: %w", err)
	}
	return true, nil
}

// dirBlockedBySymlink reports whether any existing component of dir below root
// is a symlink or a non-directory. A missing component ends the walk: nothing
// beyond it can exist yet.
func dirBlockedBySymlink(root, dir string) (bool, error) {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false, fmt.Errorf("failed to make the path relative: %w", err)
	}
	cur := root
	for part := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
		if part == "." || part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		switch {
		case os.IsNotExist(err):
			return false, nil
		case err != nil:
			return false, fmt.Errorf("failed to inspect %s: %w", cur, err)
		case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
			return true, nil
		}
	}
	return false, nil
}

// ensureDirNoLinks creates dir and its missing parents below root, refusing to
// pass through a symlink or a non-directory. The check rides the creation of
// each component; a link swapped in after its component was made is the local
// race every cache writer already lives with.
func ensureDirNoLinks(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return fmt.Errorf("failed to make the path relative: %w", err)
	}
	cur := root
	for part := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
		if part == "." || part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		switch {
		case os.IsNotExist(err):
			if err := os.Mkdir(cur, 0o755); err != nil {
				return fmt.Errorf("failed to create %s: %w", cur, err)
			}
		case err != nil:
			return fmt.Errorf("failed to inspect %s: %w", cur, err)
		case info.Mode()&os.ModeSymlink != 0:
			return &symlinkDirError{path: cur}
		case !info.IsDir():
			return fmt.Errorf("%s exists and is not a directory", cur)
		}
	}
	return nil
}

// regularFileExists reports a path whose own entry is a regular file. Unlike
// the writes it guards, a filesystem that cannot answer is an error, not an
// absence: an unreadable namespace must not masquerade as a missing cache.
func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to inspect %s: %w", path, err)
	}
	return info.Mode().IsRegular(), nil
}

func dirExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to inspect %s: %w", path, err)
	}
	return info.IsDir(), nil
}
