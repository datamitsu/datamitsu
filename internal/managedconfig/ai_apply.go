package managedconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/gitenv"
)

const aiDirectoryMarker = ".datamitsu-owned"

// applyAI publishes generated AI artifacts without replacing unrelated files.
func applyAI(ctx context.Context, gitRoot string, artifacts map[string]aiArtifact, beforePublish func() (bool, error), diagnostic func(string, ...any)) (bool, error) {
	root, err := os.OpenRoot(gitRoot)
	if err != nil {
		return false, fmt.Errorf("open repository root: %w", err)
	}
	defer func() { _ = root.Close() }()

	tracked, err := trackedPaths(ctx, gitRoot)
	if err != nil {
		return false, err
	}
	desired := make(map[string]aiArtifact, len(artifacts))
	for _, artifact := range artifacts {
		path := filepath.ToSlash(filepath.Clean(filepath.FromSlash(artifact.path)))
		if path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
			return false, fmt.Errorf("invalid AI artifact path %q", artifact.path)
		}
		if !underManagedRoot(path, artifact.files != nil) {
			return false, fmt.Errorf("AI artifact path %q is not under a managed root", path)
		}
		if _, exists := desired[path]; exists {
			return false, fmt.Errorf("duplicate AI artifact path %q", path)
		}
		artifact.path = path
		desired[path] = artifact
	}

	for path := range desired {
		if err := checkAIAncestors(root, path); err != nil {
			return false, err
		}
		if trackedExactOrAncestor(tracked, path) || trackedDescendant(tracked, path) {
			return false, fmt.Errorf("refusing to replace tracked AI artifact %s", path)
		}
		if err := rejectAIParentAliases(root, path); err != nil {
			return false, err
		}
		if desired[path].files != nil {
			marker := filepath.ToSlash(filepath.Join(path, aiDirectoryMarker))
			if err := rejectAICaseAlias(root, marker); err != nil {
				return false, err
			}
			continue
		}
		marker := sidecar(path)
		if trackedExactOrAncestor(tracked, marker) || trackedDescendant(tracked, marker) {
			return false, fmt.Errorf("refusing to replace tracked AI marker %s", marker)
		}
		if err := checkAIAncestors(root, marker); err != nil {
			return false, err
		}
	}
	current, err := scanAI(root, desired, tracked)
	if err != nil {
		return false, err
	}
	if beforePublish == nil {
		beforePublish = func() (bool, error) { return false, nil }
	}
	changed, err := beforePublish()
	if err != nil {
		return false, err
	}

	paths := make([]string, 0, len(desired))
	for path := range desired {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		artifact := desired[path]
		old, hasOld := current[path]
		if aiArtifactEqual(old, artifact) {
			continue
		}
		if err := checkAIAncestors(root, path); err != nil {
			return changed, err
		}
		if err := publishAI(root, path, artifact, hasOld, old); err != nil {
			return changed, err
		}
		changed = true
	}

	stale := make([]string, 0)
	for path, existing := range current {
		if _, ok := desired[path]; ok {
			continue
		}
		if !strings.HasPrefix(filepath.Base(path), "dm-") {
			continue
		}
		if trackedExactOrAncestor(tracked, path) || trackedDescendant(tracked, path) {
			continue
		}
		if existing.directory {
			if !existing.markerPresent || !existing.markerExact || len(existing.marker) != 0 {
				continue
			}
			if trackedExactOrAncestor(tracked, filepath.Join(path, aiDirectoryMarker)) {
				continue
			}
			stale = append(stale, path)
			continue
		}
		marker := sidecar(path)
		if trackedExactOrAncestor(tracked, marker) {
			continue
		}
		if len(existing.marker) == 0 || string(existing.marker) != digest.XXH3Of(existing.data).String() {
			if diagnostic != nil {
				diagnostic("preserving AI file with missing or changed ownership marker: %s", path)
			}
			continue
		}
		stale = append(stale, path)
	}
	sort.Strings(stale)
	for _, path := range stale {
		existing := current[path]
		if existing.directory {
			marker, err := readAIRegular(root, filepath.Join(filepath.FromSlash(path), aiDirectoryMarker))
			if err != nil || len(marker) != 0 {
				if diagnostic != nil {
					diagnostic("preserving AI directory with changed ownership marker: %s", path)
				}
				continue
			}
		} else {
			data, err := readAIRegular(root, filepath.FromSlash(path))
			marker, markerErr := readAIRegular(root, filepath.FromSlash(sidecar(path)))
			if err != nil || markerErr != nil || !bytes.Equal(data, existing.data) || string(marker) != digest.XXH3Of(data).String() {
				if diagnostic != nil {
					diagnostic("preserving AI file changed after ownership check: %s", path)
				}
				continue
			}
		}
		if err := removeAI(root, path, existing); err != nil {
			return changed, fmt.Errorf("remove stale AI artifact %s: %w", path, err)
		}
		changed = true
	}
	return changed, nil
}

type aiCurrent struct {
	data          []byte
	marker        []byte
	directory     bool
	files         map[string]aiFile
	markerPresent bool
	markerExact   bool
}

func trackedPaths(ctx context.Context, gitRoot string) (map[string]bool, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached")
	cmd.Dir = gitRoot
	cmd.Env = gitenv.Environ()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	tracked := make(map[string]bool)
	for path := range bytes.SplitSeq(out, []byte{0}) {
		if len(path) != 0 {
			tracked[filepath.ToSlash(string(path))] = true
		}
	}
	return tracked, nil
}

func trackedExactOrAncestor(tracked map[string]bool, path string) bool {
	path = filepath.ToSlash(path)
	for item := range tracked {
		if matches, _ := aiPathWithin(path, item); matches {
			return true
		}
	}
	return false
}

func trackedDescendant(tracked map[string]bool, path string) bool {
	path = filepath.ToSlash(path)
	for item := range tracked {
		if matches, descendant := aiPathWithin(item, path); matches && descendant {
			return true
		}
	}
	return false
}

func rejectAICaseAlias(root *os.Root, path string) error {
	parent := filepath.Dir(filepath.FromSlash(path))
	entries, err := fs.ReadDir(root.FS(), parent)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read AI parent %s: %w", parent, err)
	}
	base := filepath.Base(filepath.FromSlash(path))
	for _, entry := range entries {
		if entry.Name() != base && strings.EqualFold(entry.Name(), base) {
			return fmt.Errorf("AI destination %s has an existing case alias %s", path, filepath.ToSlash(filepath.Join(parent, entry.Name())))
		}
	}
	return nil
}

func aiPathWithin(path, ancestor string) (matches, descendant bool) {
	for {
		component, rest, more := strings.Cut(path, "/")
		parentComponent, parentRest, parentMore := strings.Cut(ancestor, "/")
		if !strings.EqualFold(component, parentComponent) {
			return false, false
		}
		if !parentMore {
			return true, more
		}
		if !more {
			return false, false
		}
		path, ancestor = rest, parentRest
	}
}

func rejectAIParentAliases(root *os.Root, path string) error {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, wanted := range parts {
		parent := "."
		if i > 0 {
			parent = strings.Join(parts[:i], "/")
		}
		entries, err := fs.ReadDir(root.FS(), parent)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return fmt.Errorf("read AI parent %s: %w", parent, err)
		}
		for _, entry := range entries {
			if entry.Name() != wanted && strings.EqualFold(entry.Name(), wanted) {
				return fmt.Errorf("AI destination %s has case-aliased parent component %s", path, filepath.ToSlash(filepath.Join(filepath.ToSlash(parent), entry.Name())))
			}
		}
	}
	return nil
}

func underManagedRoot(path string, directory bool) bool {
	for root, suffix := range aiRoots {
		root = filepath.ToSlash(root)
		if filepath.ToSlash(filepath.Dir(path)) == root && ((directory && suffix == "") || (!directory && suffix != "" && filepath.Ext(path) == suffix)) {
			return true
		}
	}
	return false
}

func checkAIAncestors(root *os.Root, path string) error {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := 1; i <= len(parts); i++ {
		name := filepath.FromSlash(strings.Join(parts[:i], "/"))
		info, err := root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect AI path %s: %w", name, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink in AI path %s", name)
		}
		if i < len(parts) && !info.IsDir() {
			return fmt.Errorf("AI path component is not a directory: %s", name)
		}
	}
	return nil
}

func aiArtifactEqual(current aiCurrent, desired aiArtifact) bool {
	if desired.files != nil {
		return current.directory && current.markerPresent && current.markerExact && len(current.marker) == 0 && equalAIFiles(current.files, desired.files)
	}
	return !current.directory && bytes.Equal(current.data, desired.data) && string(current.marker) == digest.XXH3Of(desired.data).String()
}

func equalAIFiles(a, b map[string]aiFile) bool {
	if len(a) != len(b) {
		return false
	}
	for path, file := range a {
		other, ok := b[path]
		if !ok || file.mode&0o111 != other.mode&0o111 || !bytes.Equal(file.data, other.data) {
			return false
		}
	}
	return true
}

func sidecar(path string) string {
	return filepath.ToSlash(filepath.Join(filepath.Dir(path), ".datamitsu-owned-"+filepath.Base(path)+".marker"))
}

func scanAI(root *os.Root, desired map[string]aiArtifact, tracked map[string]bool) (map[string]aiCurrent, error) {
	out := make(map[string]aiCurrent)
	for managedRoot, suffix := range aiRoots {
		base := filepath.ToSlash(managedRoot)
		if err := checkAIAncestors(root, base); err != nil {
			return nil, fmt.Errorf("check AI root %s: %w", base, err)
		}
		entries, err := fs.ReadDir(root.FS(), base)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read AI root %s: %w", base, err)
		}
		for _, entry := range entries {
			name := filepath.ToSlash(filepath.Join(base, entry.Name()))
			for target, artifact := range desired {
				if artifact.files == nil {
					marker := sidecar(target)
					if marker != name && strings.EqualFold(marker, name) {
						return nil, fmt.Errorf("AI destination %s has an existing case alias %s", marker, name)
					}
				} else {
					marker := filepath.ToSlash(filepath.Join(target, aiDirectoryMarker))
					if marker != name && strings.EqualFold(marker, name) {
						return nil, fmt.Errorf("AI directory %s has marker case alias %s", marker, name)
					}
				}
				if target != name && strings.EqualFold(target, name) {
					return nil, fmt.Errorf("AI destination %s has an existing case alias %s", target, name)
				}
			}
			if !strings.HasPrefix(entry.Name(), "dm-") || (suffix != "" && filepath.Ext(entry.Name()) != suffix) {
				continue
			}
			_, wanted := desired[name]
			if !wanted && (trackedExactOrAncestor(tracked, name) || trackedDescendant(tracked, name) || entry.Type()&fs.ModeSymlink != 0) {
				continue
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				return nil, fmt.Errorf("refusing symlink AI artifact %s", name)
			}
			if entry.IsDir() {
				children, err := fs.ReadDir(root.FS(), name)
				if err != nil {
					return nil, fmt.Errorf("read AI directory marker %s: %w", name, err)
				}
				var marker []byte
				markerExact := false
				for _, child := range children {
					if child.Name() == aiDirectoryMarker {
						marker, err = readAIRegular(root, filepath.Join(name, aiDirectoryMarker))
						if err != nil {
							return nil, fmt.Errorf("read AI directory marker %s: %w", name, err)
						}
						markerExact = true
					}
				}
				if !wanted && (!markerExact || len(marker) != 0) {
					continue
				}
				files, err := readAIDirectory(root, name)
				if err != nil {
					return nil, fmt.Errorf("read AI directory %s: %w", name, err)
				}
				out[name] = aiCurrent{directory: true, marker: marker, files: files, markerPresent: markerExact, markerExact: markerExact, data: nil}
				continue
			}
			if !entry.Type().IsRegular() {
				if !wanted {
					continue
				}
				return nil, fmt.Errorf("non-regular AI artifact %s", name)
			}
			if !wanted {
				info, err := root.Lstat(filepath.FromSlash(sidecar(name)))
				if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
					continue
				}
				if err != nil {
					return nil, fmt.Errorf("read AI ownership marker %s: %w", name, err)
				}
			}
			if err := checkAIAncestors(root, sidecar(name)); err != nil {
				if !wanted {
					continue
				}
				return nil, fmt.Errorf("check AI marker ancestors %s: %w", name, err)
			}
			data, err := root.ReadFile(filepath.FromSlash(name))
			if err != nil {
				return nil, fmt.Errorf("read AI artifact %s: %w", name, err)
			}
			marker, err := readAIRegular(root, filepath.FromSlash(sidecar(name)))
			markerPresent := err == nil
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("read AI ownership marker %s: %w", name, err)
			}
			out[name] = aiCurrent{data: data, marker: marker, markerPresent: markerPresent, directory: false, files: nil, markerExact: false}
		}
	}
	return out, nil
}

func readAIRegular(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("stat AI file %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular AI file: %s", name)
	}
	data, err := root.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read AI file %s: %w", name, err)
	}
	return data, nil
}

func readAIDirectory(root *os.Root, base string) (map[string]aiFile, error) {
	files := make(map[string]aiFile)
	err := fs.WalkDir(root.FS(), base, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == base {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink AI artifact %s", path)
		}
		if entry.IsDir() || filepath.Base(path) == aiDirectoryMarker {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular AI resource %s", path)
		}
		data, err := root.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read AI resource %s: %w", path, err)
		}
		info, err := root.Stat(path)
		if err != nil {
			return fmt.Errorf("stat AI resource %s: %w", path, err)
		}
		rel := strings.TrimPrefix(path, base+"/")
		files[filepath.ToSlash(rel)] = aiFile{data: data, mode: info.Mode().Perm()}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk AI directory %s: %w", base, err)
	}
	return files, nil
}

func publishAI(root *os.Root, path string, artifact aiArtifact, hasOld bool, _ aiCurrent) error {
	return publishAIWithRename(root, path, artifact, hasOld, root.Rename)
}

func publishAIWithRename(root *os.Root, path string, artifact aiArtifact, hasOld bool, rename func(string, string) error) error {
	dest := filepath.FromSlash(path)
	parent := filepath.Dir(dest)
	if err := root.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create AI parent: %w", err)
	}
	stage, err := makeAIStage(root, parent)
	if err != nil {
		return err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = root.RemoveAll(stage)
		}
	}()
	staged := filepath.Join(stage, "artifact")
	if artifact.files != nil {
		if err := root.Mkdir(staged, 0o755); err != nil {
			return fmt.Errorf("create staged AI artifact: %w", err)
		}
		for key, file := range artifact.files {
			if !filepath.IsLocal(filepath.FromSlash(key)) || !fs.ValidPath(key) || key == "." || strings.Contains(key, "\\") {
				return fmt.Errorf("invalid AI resource path %q", key)
			}
			name := filepath.Join(staged, filepath.FromSlash(key))
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return fmt.Errorf("create AI resource parent: %w", err)
			}
			mode := file.mode
			if mode == 0 {
				mode = 0o644
			}
			if err := root.WriteFile(name, file.data, mode); err != nil {
				return fmt.Errorf("write AI resource: %w", err)
			}
		}
		if err := root.WriteFile(filepath.Join(staged, aiDirectoryMarker), nil, 0o644); err != nil {
			return fmt.Errorf("write AI directory marker: %w", err)
		}
	} else {
		if err := root.WriteFile(staged, artifact.data, 0o644); err != nil {
			return fmt.Errorf("write AI profile: %w", err)
		}
		if err := root.WriteFile(filepath.Join(stage, "marker"), []byte(digest.XXH3Of(artifact.data).String()), 0o644); err != nil {
			return fmt.Errorf("write AI profile marker: %w", err)
		}
	}
	marker := filepath.FromSlash(sidecar(path))
	oldPath := filepath.Join(stage, "old")
	if hasOld {
		if err := rename(dest, oldPath); err != nil {
			return err
		}
	}
	restore := func(publishErr error, published bool) error {
		var restoreErr error
		if hasOld {
			restoreErr = root.Rename(oldPath, dest)
		} else if published {
			restoreErr = root.Remove(dest)
		}
		if restoreErr != nil {
			keepStage = true
			return errors.Join(publishErr, fmt.Errorf("restore %s (staging retained at %s): %w", dest, stage, restoreErr))
		}
		return publishErr
	}
	if err := rename(staged, dest); err != nil {
		return restore(err, false)
	}
	if artifact.files == nil {
		if err := rename(filepath.Join(stage, "marker"), marker); err != nil {
			return restore(err, true)
		}
	}
	return nil
}

func makeAIStage(root *os.Root, parent string) (string, error) {
	for i := range 20 {
		name := filepath.Join(parent, fmt.Sprintf(".datamitsu-stage-%d", i))
		if err := root.Mkdir(name, 0o700); err == nil {
			return name, nil
		} else if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create AI staging directory: %w", err)
		}
	}
	return "", errors.New("cannot allocate AI staging directory")
}

func removeAI(root *os.Root, path string, current aiCurrent) error {
	if current.directory {
		if err := root.RemoveAll(filepath.FromSlash(path)); err != nil {
			return fmt.Errorf("prune AI directory: %w", err)
		}
		return nil
	}
	if err := root.Remove(filepath.FromSlash(path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("prune AI profile: %w", err)
	}
	if err := root.Remove(filepath.FromSlash(sidecar(path))); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("prune AI sidecar: %w", err)
	}
	return nil
}
