package configcache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/shamaton/msgpack/v2"
)

// DirName is the cache subtree holding evaluated-config artifacts. It lives
// under GetCachePath(), the tree that is safe to delete, never under the store.
const DirName = "config-eval"

// artifactExt is the file extension of a stored artifact. Included in the file
// name rather than implied, so a stray file in the tree is recognizable.
const artifactExt = ".msgpack"

// MaxAge is how long an unread artifact is kept. A hit refreshes the entry's
// mtime (see refreshInterval), so "unread" is what actually expires, not
// "unwritten". Pruning happens on the write path, which is the miss path: the
// store must not repeat the binary store's "no GC, 7 GB" mistake, and it must
// not pay for garbage collection on the fast path either.
const MaxAge = 14 * 24 * time.Hour

// refreshInterval bounds how often a hit rewrites an entry's mtime. Without it
// every invocation would touch the file; with it the touch happens at most once
// a day while still keeping a used entry alive indefinitely.
const refreshInterval = 24 * time.Hour

// ProjectNamespace returns the namespace of a repository chain,
// "projects/{XXH3-128(gitRoot)}" — the same identity the source-mode farm uses,
// so a project's artifacts sit beside its farm rather than in a second scheme.
func ProjectNamespace(gitRoot string) (string, error) {
	if gitRoot == "" {
		return "", errors.New("gitRoot must not be empty")
	}
	if !filepath.IsAbs(gitRoot) {
		return "", fmt.Errorf("gitRoot must be absolute: %q", gitRoot)
	}
	return path2(env.ProjectFarmsDirName, env.HashProjectPath(filepath.Clean(gitRoot))), nil
}

// ChainNamespace returns the namespace of a machine-level --config chain,
// "configs/{XXH3-128(resolved chain)}", mirroring the explicit-config farm
// identity. There is deliberately no fall back to cwd: two directories sharing
// one namespace is exactly how a cache serves another directory's config.
func ChainNamespace(configPaths []string) (string, error) {
	identity, err := env.ConfigFarmIdentity(configPaths)
	if err != nil {
		return "", err
	}
	return path2(env.ConfigFarmsDirName, identity), nil
}

func path2(a, b string) string {
	return a + "/" + b
}

// Store is the on-disk artifact store for one namespace.
type Store struct {
	// dir is {cache}/config-eval/{namespace}.
	dir string
}

// NewStore returns the store for a namespace produced by ProjectNamespace or
// ChainNamespace. The directory is not created until something is written.
func NewStore(namespace string) (*Store, error) {
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	root := filepath.Join(env.GetCachePath(), DirName)
	return &Store{dir: filepath.Join(root, filepath.FromSlash(namespace))}, nil
}

// Dir returns the directory this store writes into. Exported for tests and for
// diagnostics; nothing outside the package should construct paths from it.
func (s *Store) Dir() string { return s.dir }

func validateNamespace(namespace string) error {
	if namespace == "" {
		return errors.New("namespace must not be empty")
	}
	kind, id, ok := strings.Cut(namespace, "/")
	if !ok || id == "" {
		return fmt.Errorf("namespace must be {kind}/{identity}: %q", namespace)
	}
	if kind != env.ProjectFarmsDirName && kind != env.ConfigFarmsDirName {
		return fmt.Errorf("unknown namespace kind %q", kind)
	}
	if !isHex(id) {
		return fmt.Errorf("namespace identity must be a hex digest: %q", id)
	}
	return nil
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// artifact is the stored shape. FormatVersion is repeated inside the file even
// though it is already folded into the key: a file whose name says one thing and
// whose body says another is corruption, and corruption must read as a miss.
type artifact struct {
	FormatVersion int            `msgpack:"formatVersion"`
	Config        *config.Config `msgpack:"config"`
}

// Load returns the cached config for key, and whether it was a hit.
//
// Every failure mode is a miss, never an error: a truncated write, a partially
// deleted cache tree, a file from a binary that encoded a different shape. A
// config-evaluation cache that can fail a command is worse than no cache, so the
// only thing a bad entry earns is removal.
func (s *Store) Load(key string) (*config.Config, bool) {
	p, err := s.pathFor(key)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}

	var art artifact
	if err := msgpack.Unmarshal(data, &art); err != nil {
		s.discard(p)
		return nil, false
	}
	if art.FormatVersion != FormatVersion || art.Config == nil {
		s.discard(p)
		return nil, false
	}

	s.touch(p)
	return art.Config, true
}

// Save writes cfg under key.
//
// Setup content is dropped before encoding: ConfigSetup.Content holds a live
// goja value that cannot be serialized and must never be faked. The write goes
// through a sibling temp file and a rename, so a reader sees either the whole
// artifact or none of it. Entries are immutable per key, so two processes
// writing the same key concurrently is harmless — the loser's bytes are
// identical to the winner's.
func (s *Store) Save(key string, cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config must not be nil")
	}
	p, err := s.pathFor(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create config cache directory: %w", err)
	}

	encoded, err := msgpack.Marshal(artifact{FormatVersion: FormatVersion, Config: withoutSetupContent(cfg)})
	if err != nil {
		return fmt.Errorf("encode config artifact: %w", err)
	}

	f, err := os.CreateTemp(s.dir, "."+key+".*")
	if err != nil {
		return fmt.Errorf("create config artifact temp file: %w", err)
	}
	tmp := f.Name()
	if _, err := f.Write(encoded); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write config artifact: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close config artifact: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install config artifact: %w", err)
	}

	Prune(filepath.Join(env.GetCachePath(), DirName), MaxAge)
	return nil
}

func (s *Store) pathFor(key string) (string, error) {
	if !isHex(key) {
		return "", fmt.Errorf("cache key must be a hex digest: %q", key)
	}
	return filepath.Join(s.dir, key+artifactExt), nil
}

// discard removes an entry that could not be used. A failure to remove is
// ignored: the entry is already being treated as absent.
func (s *Store) discard(path string) {
	_ = os.Remove(path)
}

// touch keeps a read entry alive against MaxAge, at most once per
// refreshInterval so the hit path stays a read.
func (s *Store) touch(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if time.Since(info.ModTime()) < refreshInterval {
		return
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

// withoutSetupContent returns a shallow copy of cfg whose Setup entries carry no
// Content. The copy leaves the caller's config untouched: it is the live one the
// command is about to run with.
func withoutSetupContent(cfg *config.Config) *config.Config {
	clone := *cfg
	if len(cfg.Setup) == 0 {
		return &clone
	}
	setup := make(config.MapOfConfigSetup, len(cfg.Setup))
	for name, entry := range cfg.Setup {
		entry.Content = nil
		setup[name] = entry
	}
	clone.Setup = setup
	return &clone
}

// Prune removes artifacts under root that have not been read for maxAge, and
// then any directory the removals emptied. Failures are ignored throughout:
// pruning is opportunistic maintenance on the miss path and must never turn a
// successful evaluation into an error.
func Prune(root string, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	var stale, dirs []string
	// The walk only collects; the removals happen after it. Mutating a tree
	// while walking it is how a symlinked subtree turns into a delete somewhere
	// else entirely.
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal: pruning must never fail a load
		}
		if d.IsDir() {
			if p != root {
				dirs = append(dirs, p)
			}
			return nil
		}
		if filepath.Ext(p) != artifactExt {
			return nil
		}
		// An entry that vanished mid-walk needs no pruning, and one younger than
		// the cutoff is still live.
		if info, err := d.Info(); err != nil || !info.ModTime().Before(cutoff) {
			return nil //nolint:nilerr // see above: pruning must never fail a load
		}
		stale = append(stale, p)
		return nil
	})

	for _, p := range stale {
		_ = os.Remove(p)
	}
	// Deepest first, so a namespace directory emptied by its own removals goes
	// too. os.Remove refuses a non-empty directory, which is the test we want.
	for _, dir := range slices.Backward(dirs) {
		_ = os.Remove(dir)
	}
}

// ClearAll removes the whole config-eval tree under cacheDir. Called by
// `datamitsu cache clear --all`, which must leave no evaluated config behind.
func ClearAll(cacheDir string) error {
	if cacheDir == "" {
		return errors.New("cacheDir must not be empty")
	}
	dir := filepath.Join(cacheDir, DirName)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove config evaluation cache: %w", err)
	}
	return nil
}

// ClearProject removes the config-eval artifacts of one git root, for
// `datamitsu cache clear` without --all.
func ClearProject(cacheDir string, gitRoot string) error {
	if cacheDir == "" {
		return errors.New("cacheDir must not be empty")
	}
	namespace, err := ProjectNamespace(gitRoot)
	if err != nil {
		return err
	}
	dir := filepath.Join(cacheDir, DirName, filepath.FromSlash(namespace))
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove project config evaluation cache: %w", err)
	}
	return nil
}
