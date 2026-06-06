// Package utils provides small filesystem and path helpers used across datamitsu.
package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Exists checks if a file or directory exists
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDir checks if the path is a directory
func IsDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// IsFile checks if the path is a file
func IsFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// EnsureDir creates a directory if it doesn't exist
func EnsureDir(path string) error {
	if !Exists(path) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", path, err)
		}
	}
	return nil
}

// HomeDir returns the user's home directory
func HomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	return home, nil
}

// ExpandHome replaces ~ with the home directory
func ExpandHome(path string) string {
	if path == "~" {
		home, _ := HomeDir()
		return home
	}
	if len(path) > 2 && path[:2] == "~/" {
		home, _ := HomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

// RenameReplace renames src to dst, replacing dst if it already exists.
// On Unix os.Rename is atomic and replaces the destination. On Windows
// os.Rename fails when dst exists, so we move dst to a backup location
// first, then rename src to dst. If the rename fails, the backup is
// restored so that the original dst content is not lost.
func RenameReplace(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("rename %q to %q: %w", src, dst, err)
	}

	dir := filepath.Dir(dst)
	base := filepath.Base(dst)
	f, tmpErr := os.CreateTemp(dir, base+".~rename~*")
	if tmpErr != nil {
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("rename %q to %q: %w", src, dst, err)
		}
		return nil
	}
	backup := f.Name()
	_ = f.Close()
	_ = os.Remove(backup)

	if mvErr := os.Rename(dst, backup); mvErr != nil {
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("rename %q to %q: %w", src, dst, err)
		}
		return nil
	}
	if err2 := os.Rename(src, dst); err2 != nil {
		_ = os.Rename(backup, dst)
		return fmt.Errorf("rename %q to %q: %w", src, dst, err2)
	}
	_ = os.Remove(backup)
	return nil
}

// ReadFileIfExists reads a file if it exists
func ReadFileIfExists(path string) ([]byte, error) {
	if !Exists(path) {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", path, err)
	}
	return data, nil
}

// WriteFile writes a file, creating intermediate directories if needed
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write file %q: %w", path, err)
	}
	return nil
}
