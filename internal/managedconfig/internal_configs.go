package managedconfig

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/datamitsu/datamitsu/internal/config"
)

// InternalConfigsResult reports what WriteInternalConfigs changed, as
// git-root-relative slash paths.
type InternalConfigsResult struct {
	Written   []string
	Unchanged []string
	Removed   []string
}

// Changed reports whether the directory differs from what it held before.
func (r *InternalConfigsResult) Changed() bool {
	return len(r.Written) > 0 || len(r.Removed) > 0
}

// DesiredInternalConfigs returns the rendered content of every
// internal-placement entry, keyed by managed config key.
func DesiredInternalConfigs(configs config.MapOfManagedConfigs) map[string]string {
	desired := make(map[string]string)
	for key, mc := range configs {
		if mc.Placement == config.PlacementInternal && mc.Render != nil {
			desired[key] = mc.Render.Content
		}
	}
	return desired
}

// WriteInternalConfigs makes .datamitsu/configs/ hold exactly the rendered
// content of every internal-placement managed config: files are written,
// changed ones replaced and every other file removed, since nothing but this
// function owns the directory.
//
// Each file is replaced by a rename, never the directory as a whole, so a tool
// or preflight reading concurrently sees the old file or the new one and never
// a missing directory. Writes go through an os.Root, so a symlink inside
// .datamitsu/ cannot redirect them into the store.
func WriteInternalConfigs(gitRoot string, configs config.MapOfManagedConfigs, dryRun bool) (*InternalConfigsResult, error) {
	desired := DesiredInternalConfigs(configs)
	datamitsuDir := filepath.Join(gitRoot, config.DatamitsuDirName)
	target := filepath.Join(datamitsuDir, config.InternalConfigsDir)

	current, err := readInternalConfigs(target)
	if err != nil {
		return nil, err
	}
	result := diffInternalConfigs(current, desired)
	if dryRun || !result.Changed() {
		return result, nil
	}

	release, err := lockDatamitsuDir(gitRoot)
	if err != nil {
		return nil, err
	}
	defer release()

	// Again under the lock: another writer may have changed the directory since
	// the look above, and skipping a file on that stale view would leave a mix
	// of both writers' sets.
	if current, err = readInternalConfigs(target); err != nil {
		return nil, err
	}
	if result = diffInternalConfigs(current, desired); !result.Changed() {
		return result, nil
	}

	if _, statErr := os.Lstat(datamitsuDir); os.IsNotExist(statErr) {
		if err := os.MkdirAll(datamitsuDir, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", datamitsuDir, err)
		}
		if err := createDatamitsuGitignore(datamitsuDir); err != nil {
			return nil, err
		}
	}
	// A link named configs, left by a configuration from before the name was
	// reserved, points into the store.
	if info, statErr := os.Lstat(target); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(target); err != nil {
			return nil, fmt.Errorf("remove link %s: %w", target, err)
		}
	}

	if len(desired) == 0 {
		if err := os.RemoveAll(target); err != nil {
			return nil, fmt.Errorf("remove %s: %w", target, err)
		}
		return result, nil
	}

	root, err := os.OpenRoot(datamitsuDir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", datamitsuDir, err)
	}
	defer func() { _ = root.Close() }()

	// Stale files first: a key that became a directory of another cannot be
	// written while the file of the same name is still there.
	for key := range current {
		if _, keep := desired[key]; keep {
			continue
		}
		if err := root.Remove(internalName(key)); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove stale %s: %w", key, err)
		}
	}
	pruneEmptyDirs(target)

	for key, content := range desired {
		if data, ok := current[key]; ok && data != nil && bytes.Equal(data, []byte(content)) {
			continue
		}
		if err := writeFileAtomic(root, internalName(key), []byte(content)); err != nil {
			return nil, fmt.Errorf("write %s: %w", key, err)
		}
	}
	return result, nil
}

// internalName is a key's path relative to .datamitsu/.
func internalName(key string) string {
	return filepath.Join(config.InternalConfigsDir, filepath.FromSlash(key))
}

// writeFileAtomic replaces name through a sibling temporary file and a rename.
// A leftover temporary file from an interrupted write is just another stale
// entry, removed by the next run.
func writeFileAtomic(root *os.Root, name string, data []byte) error {
	dir := filepath.Dir(name)
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if info, err := root.Lstat(name); err == nil && info.IsDir() {
		if err := root.RemoveAll(name); err != nil {
			return fmt.Errorf("remove directory in the way: %w", err)
		}
	}
	tmp := filepath.Join(dir, ".tmp-"+rand.Text())
	if err := root.WriteFile(tmp, data, 0o644); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("install: %w", err)
	}
	return nil
}

// pruneEmptyDirs removes directories under root left empty by removed files,
// deepest first; root itself stays.
func pruneEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	for _, dir := range slices.Backward(dirs) {
		_ = os.Remove(dir)
	}
}

// copyInternalConfigs copies .datamitsu/configs/ into a .datamitsu/ being
// assembled for a rebuild, so the swap publishes it together with the links.
// The rebuild cannot render it — its content comes from the evaluated config,
// which exec does not hold — and moving it over after the swap would leave
// every internal-placement tool without its config in between. An unreadable
// file is dropped; the preflight reports it and init rewrites it.
func copyInternalConfigs(datamitsuDir, assembling string) error {
	files, err := readInternalConfigs(filepath.Join(datamitsuDir, config.InternalConfigsDir))
	if err != nil || len(files) == 0 {
		return err
	}
	root, err := os.OpenRoot(assembling)
	if err != nil {
		return fmt.Errorf("open %s: %w", assembling, err)
	}
	defer func() { _ = root.Close() }()
	for rel, data := range files {
		if data == nil {
			continue
		}
		name := internalName(rel)
		if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return fmt.Errorf("copy %s: %w", rel, err)
		}
		if err := root.WriteFile(name, data, 0o644); err != nil {
			return fmt.Errorf("copy %s: %w", rel, err)
		}
	}
	return nil
}

// readInternalConfigs returns the regular files under dir keyed by their
// slash path relative to dir. A missing directory is an empty set. Reads go
// through an os.Root so a symlink planted inside dir cannot make it read
// outside.
func readInternalConfigs(dir string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return files, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()

	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := root.ReadFile(path)
		if readErr != nil {
			// Unreadable, or a link out of the directory: record it so it is replaced or removed.
			data = nil
		}
		files[path] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	return files, nil
}

func diffInternalConfigs(current map[string][]byte, desired map[string]string) *InternalConfigsResult {
	result := &InternalConfigsResult{}
	for key, content := range desired {
		rel := config.InternalConfigRelPath(key)
		if data, ok := current[key]; ok && data != nil && bytes.Equal(data, []byte(content)) {
			result.Unchanged = append(result.Unchanged, rel)
		} else {
			result.Written = append(result.Written, rel)
		}
	}
	for key := range current {
		if _, ok := desired[key]; !ok {
			result.Removed = append(result.Removed, config.InternalConfigRelPath(key))
		}
	}
	sort.Strings(result.Written)
	sort.Strings(result.Unchanged)
	sort.Strings(result.Removed)
	return result
}
