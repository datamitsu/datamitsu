// Package storelock provides inter-process subtree locks over the datamitsu
// store. Lock files live under {cache} — never inside the store itself — so
// `store clear` cannot unlink a held lock's inode and let a second process
// lock a replacement inode underneath the first.
//
// The lock is keyed by (store root, subtree): OCI seeding and a prepare step
// placing the same subtree serialize through it, and a future `store gc`
// takes the same lock before removing anything.
package storelock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
)

type (
	leaseKey struct{}
	lease    struct{ root string }
)

// gateAcquiredHook is set only by helper-process tests to synchronize a waiter
// after it owns the gate and before it blocks on active leases.
var gateAcquiredHook func()

// WithStore enters the store's shared activity lease. Nested calls for the same
// canonical root reuse the lease; callers must propagate the returned context.
func WithStore(ctx context.Context, storeRoot string) (context.Context, func(), error) {
	if err := env.RejectInstallChild("datamitsu provisioning"); err != nil {
		return nil, nil, err
	}
	root, err := canonicalRoot(storeRoot)
	if err != nil {
		return nil, nil, err
	}
	if current, ok := ctx.Value(leaseKey{}).(lease); ok && current.root == root {
		return ctx, func() {}, nil
	}
	release, err := acquireShared(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	return context.WithValue(ctx, leaseKey{}, lease{root}), release, nil
}

// Clear holds exclusive activity ownership until the caller completes removal.
func Clear(ctx context.Context, storeRoot string) (func(), error) {
	if err := env.RejectInstallChild("datamitsu store clear"); err != nil {
		return nil, err
	}
	if current, ok := ctx.Value(leaseKey{}).(lease); ok {
		if root, err := canonicalRoot(storeRoot); err == nil && current.root == root {
			return nil, errors.New("cannot clear store while holding its shared provisioning lease")
		}
	}
	root, err := canonicalRoot(storeRoot)
	if err != nil {
		return nil, err
	}
	return acquireExclusive(ctx, root)
}

func canonicalRoot(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("store root must be absolute: %q", root)
	}
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve store root: %w", err)
	}
	probe := root
	var suffix []string
	for {
		resolved, e := filepath.EvalSymlinks(probe)
		if e == nil {
			for _, s := range slices.Backward(suffix) {
				resolved = filepath.Join(resolved, s)
			}
			return resolved, nil
		}
		if !os.IsNotExist(e) {
			return "", fmt.Errorf("resolve store root: %w", e)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("resolve store root parent: %w", e)
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
}

func leaseFiles(root string) (string, string, error) {
	dir := filepath.Join(env.GetCachePath(), "store-locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("create store lock directory: %w", err)
	}
	key := digest.XXH3Multi([]byte(root), []byte("store-lease-v1")).Hex()
	return filepath.Join(dir, key+".gate"), filepath.Join(dir, key+".activity"), nil
}

func acquireShared(ctx context.Context, root string) (func(), error) {
	gate, activity, err := leaseFiles(root)
	if err != nil {
		return nil, err
	}
	releaseGate, err := lockFile(ctx, gate)
	if err != nil {
		return nil, err
	}
	releaseActivity, err := sharedLockFile(ctx, activity)
	releaseGate()
	if err != nil {
		return nil, err
	}
	return once(releaseActivity), nil
}

func acquireExclusive(ctx context.Context, root string) (func(), error) {
	gate, activity, err := leaseFiles(root)
	if err != nil {
		return nil, err
	}
	releaseGate, err := lockFile(ctx, gate)
	if err != nil {
		return nil, err
	}
	if gateAcquiredHook != nil {
		gateAcquiredHook()
	}
	releaseActivity, err := lockFile(ctx, activity)
	if err != nil {
		releaseGate()
		return nil, err
	}
	return once(func() { releaseActivity(); releaseGate() }), nil
}

func once(fn func()) func() {
	var mu sync.Mutex
	done := false
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if !done {
			done = true
			fn()
		}
	}
}

// ValidateTarget resolves an absolute store-relative target and rejects paths
// that escape the store through traversal or existing symlink parents.
func ValidateTarget(storeRoot, target string) (string, error) {
	root, err := canonicalRoot(storeRoot)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(target) {
		return "", fmt.Errorf("store target must be relative: %q", target)
	}
	clean := filepath.Clean(target)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("store target escapes store: %q", target)
	}
	joined := filepath.Join(root, clean)
	var missingRootAncestor string
	if _, e := filepath.EvalSymlinks(root); os.IsNotExist(e) {
		probeRoot := filepath.Dir(root)
		for {
			if resolved, e := filepath.EvalSymlinks(probeRoot); e == nil {
				missingRootAncestor = resolved
				break
			}
			parent := filepath.Dir(probeRoot)
			if parent == probeRoot {
				return "", errors.New("resolve store root parent")
			}
			probeRoot = parent
		}
	}
	probe := filepath.Dir(joined)
	var suffix []string
	for {
		resolved, e := filepath.EvalSymlinks(probe)
		if e == nil {
			if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) && resolved != missingRootAncestor {
				return "", fmt.Errorf("store target escapes store through symlink: %q", target)
			}
			probe = resolved
			break
		}
		if !os.IsNotExist(e) {
			return "", fmt.Errorf("resolve store target parent: %w", e)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("resolve store target parent: %w", e)
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
	for _, s := range slices.Backward(suffix) {
		probe = filepath.Join(probe, s)
	}
	resolvedTarget := filepath.Join(probe, filepath.Base(joined))
	if actual, e := filepath.EvalSymlinks(resolvedTarget); e == nil {
		if actual != root && !strings.HasPrefix(actual, root+string(filepath.Separator)) {
			return "", fmt.Errorf("store target escapes store through symlink: %q", target)
		}
		resolvedTarget = actual
	}
	return resolvedTarget, nil
}

// TargetIdentity returns the canonical identity used by Lock for a store target.
func TargetIdentity(storeRoot, subtree string) (string, error) {
	root, err := canonicalRoot(storeRoot)
	if err != nil {
		return "", err
	}
	target := subtree
	if filepath.IsAbs(subtree) {
		target, err = filepath.Rel(root, filepath.Clean(subtree))
		if err != nil {
			return "", fmt.Errorf("relativize store target: %w", err)
		}
	}
	resolved, err := ValidateTarget(root, target)
	if err != nil {
		return "", err
	}
	return digest.XXH3Multi([]byte(root), []byte(resolved)).Hex(), nil
}

// Lock blocks until the lock for (storeRoot, subtree) is acquired, the
// context is canceled, or locking fails. The returned release function drops
// the lock and is safe to call more than once.
//
// Waiting is cancelable: a caller whose context expires stops waiting rather
// than blocking indefinitely behind a slow holder (the blocker itself still
// runs to completion; the lock only guards concurrent placement).

// Lock blocks until the lock for (storeRoot, subtree) is acquired, the context
// is canceled, or locking fails.
func Lock(ctx context.Context, storeRoot, subtree string) (release func(), err error) {
	return lock(ctx, storeRoot, subtree, nil)
}

// LockWithWait calls onWait once if acquisition blocks, before polling resumes.
func LockWithWait(ctx context.Context, storeRoot, subtree string, onWait func()) (release func(), err error) {
	return lock(ctx, storeRoot, subtree, onWait)
}

func lock(ctx context.Context, storeRoot, subtree string, onWait func()) (release func(), err error) {
	root, err := canonicalRoot(storeRoot)
	if err != nil {
		return nil, err
	}
	target := subtree
	if filepath.IsAbs(subtree) {
		target, err = filepath.Rel(root, filepath.Clean(subtree))
		if err != nil {
			return nil, fmt.Errorf("relativize store target: %w", err)
		}
	}
	resolved, err := ValidateTarget(root, target)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(env.GetCachePath(), "store-locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create store lock directory: %w", err)
	}
	name := filepath.Join(dir, digest.XXH3Multi([]byte(root), []byte(resolved)).Hex()+".lock")
	return lockFileWithWait(ctx, name, onWait)
}
