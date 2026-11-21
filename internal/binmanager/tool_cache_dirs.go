package binmanager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validateCacheSlot(name string) error {
	if name == defaultToolCacheSlot {
		return nil
	}
	return validateCacheName(name)
}

// EnsureToolCacheSlots creates declared cache slots without following aliases.
func EnsureToolCacheSlots(cacheRoot, toolCacheParent string, slots []string) error {
	seen := make(map[string]struct{}, len(slots))
	for _, slot := range slots {
		if err := validateCacheSlot(slot); err != nil {
			return err
		}
		if _, exists := seen[slot]; exists {
			return fmt.Errorf("duplicate tool cache slot %q", slot)
		}
		seen[slot] = struct{}{}
	}
	if len(slots) == 0 {
		return nil
	}
	if !filepath.IsAbs(cacheRoot) || !filepath.IsAbs(toolCacheParent) {
		return errors.New("managed and app cache roots must be absolute")
	}
	managed, err := filepath.Abs(cacheRoot)
	if err != nil {
		return fmt.Errorf("resolve managed cache root: %w", err)
	}
	parent, err := filepath.Abs(toolCacheParent)
	if err != nil {
		return fmt.Errorf("resolve tool cache parent: %w", err)
	}
	rel, err := filepath.Rel(managed, parent)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("tool cache parent %q must be a strict descendant of managed cache root %q", parent, managed)
	}
	if err := os.MkdirAll(managed, 0o700); err != nil {
		return fmt.Errorf("create managed cache root: %w", err)
	}
	root, err := os.OpenRoot(managed)
	if err != nil {
		return fmt.Errorf("open managed cache root: %w", err)
	}
	defer func() { _ = root.Close() }()
	prefix := ""
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		info, statErr := root.Lstat(prefix)
		if os.IsNotExist(statErr) {
			break
		}
		if statErr != nil {
			return fmt.Errorf("inspect tool cache parent: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("tool cache parent must not contain symlinks: %q", prefix)
		}
	}
	if err := root.MkdirAll(filepath.ToSlash(rel), 0o700); err != nil {
		return fmt.Errorf("create tool cache parent: %w", err)
	}
	parentRoot, err := root.OpenRoot(filepath.ToSlash(rel))
	if err != nil {
		return fmt.Errorf("open tool cache parent: %w", err)
	}
	defer func() { _ = parentRoot.Close() }()
	for _, slot := range slots {
		info, statErr := parentRoot.Lstat(slot)
		if os.IsNotExist(statErr) {
			if err := parentRoot.Mkdir(slot, 0o700); err != nil && !os.IsExist(err) {
				return fmt.Errorf("create tool cache slot %q: %w", slot, err)
			}
			// Another invocation may create the slot after Lstat; inspect its result too.
			info, statErr = parentRoot.Lstat(slot)
		}
		if statErr != nil {
			return fmt.Errorf("inspect tool cache slot %q: %w", slot, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("tool cache slot %q must not be a symlink", slot)
		}
		if !info.IsDir() {
			return fmt.Errorf("tool cache slot %q is not a directory", slot)
		}
		opened, err := parentRoot.OpenRoot(slot)
		if err != nil {
			return fmt.Errorf("open tool cache slot %q: %w", slot, err)
		}
		_ = opened.Close()
	}
	return nil
}
