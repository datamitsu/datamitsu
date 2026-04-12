package utils

import (
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
		return os.MkdirAll(path, 0755)
	}
	return nil
}

// HomeDir returns the user's home directory
func HomeDir() (string, error) {
	return os.UserHomeDir()
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
	if err == nil || runtime.GOOS != "windows" {
		return err
	}

	dir := filepath.Dir(dst)
	base := filepath.Base(dst)
	f, tmpErr := os.CreateTemp(dir, base+".~rename~*")
	if tmpErr != nil {
		return os.Rename(src, dst)
	}
	backup := f.Name()
	_ = f.Close()
	_ = os.Remove(backup)

	if mvErr := os.Rename(dst, backup); mvErr != nil {
		return os.Rename(src, dst)
	}
	if err2 := os.Rename(src, dst); err2 != nil {
		_ = os.Rename(backup, dst)
		return err2
	}
	_ = os.Remove(backup)
	return nil
}

// ReadFileIfExists reads a file if it exists
func ReadFileIfExists(path string) ([]byte, error) {
	if !Exists(path) {
		return nil, nil
	}
	return os.ReadFile(path)
}

// WriteFile writes a file, creating intermediate directories if needed
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
