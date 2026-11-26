package managedconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/gitenv"
)

func aiExcludePatterns() []string {
	var patterns []string
	for _, root := range sortedAIKeys(aiRoots) {
		suffix := aiRoots[root]
		if suffix == "" {
			patterns = append(patterns, root+"/dm-*/")
		} else {
			patterns = append(patterns, root+"/dm-*"+suffix, root+"/.datamitsu-owned-dm-*"+suffix+".marker")
		}
	}
	return patterns
}

// ensureAIExclude adds stable excludes for datamitsu's generated AI files to
// the shared Git exclude file. It returns true only when it publishes a change.
func ensureAIExclude(ctx context.Context, gitRoot string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-path", "info/exclude")
	cmd.Dir = gitRoot
	cmd.Env = gitenv.Environ()
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("resolve git exclude path: %w", err)
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return false, errors.New("git returned an empty exclude path")
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(gitRoot, name)
	}
	name = filepath.Clean(name)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return false, fmt.Errorf("create git exclude directory: %w", err)
	}
	return updateAIExclude(name, os.Rename)
}

func updateAIExclude(name string, rename func(string, string) error) (bool, error) {
	lock, err := os.OpenFile(name+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, fmt.Errorf("lock git exclude: %w", err)
	}
	ownsLock := true
	defer func() {
		_ = lock.Close()
		if ownsLock {
			_ = os.Remove(name + ".lock")
		}
	}()

	info, err := os.Lstat(name)
	var current []byte
	mode := os.FileMode(0o644)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, fmt.Errorf("git exclude is not a regular file: %s", name)
		}
		mode = info.Mode().Perm()
		current, err = os.ReadFile(name)
		if err != nil {
			return false, fmt.Errorf("read git exclude: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return false, fmt.Errorf("inspect git exclude: %w", err)
	}

	updated := append([]byte(nil), current...)
	for _, pattern := range aiExcludePatterns() {
		if hasAnchoredPattern(current, pattern) {
			continue
		}
		if len(updated) > 0 && updated[len(updated)-1] != '\n' {
			updated = append(updated, '\n')
		}
		updated = append(updated, []byte("/"+pattern+"\n")...)
	}
	if bytes.Equal(current, updated) {
		return false, nil
	}
	if err := lock.Chmod(mode); err != nil {
		return false, fmt.Errorf("set git exclude permissions: %w", err)
	}
	if _, err := lock.Write(updated); err != nil {
		return false, fmt.Errorf("write git exclude lock: %w", err)
	}
	if err := lock.Sync(); err != nil {
		return false, fmt.Errorf("sync git exclude lock: %w", err)
	}
	if err := lock.Close(); err != nil {
		return false, fmt.Errorf("close git exclude lock: %w", err)
	}
	if err := rename(name+".lock", name); err != nil {
		return false, fmt.Errorf("publish git exclude: %w", err)
	}
	ownsLock = false
	return true, nil
}

func hasAnchoredPattern(data []byte, pattern string) bool {
	for len(data) > 0 {
		line, rest, _ := bytes.Cut(data, []byte{'\n'})
		data = rest
		if bytes.Equal(line, []byte("/"+pattern)) {
			return true
		}
	}
	return false
}
