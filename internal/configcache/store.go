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
	"github.com/datamitsu/datamitsu/internal/hashutil"
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

// MaxEntriesPerNamespace bounds how many artifacts one project (or one
// machine-level chain) keeps, newest-read first.
//
// Age alone does not bound this tree. An artifact is the whole merged config —
// megabytes for a real chain — and several key inputs produce a fresh one
// without anything about the project changing: CWD (every subdirectory a
// command runs from), the environment (OLDPWD moves on every `cd`), .git/HEAD
// (every commit, and under a detached HEAD every CI checkout), and the
// executable stamp (every rebuild orphans the entire previous set). Left to the
// 14-day cutoff those multiply into hundreds of megabytes, which is the "no GC,
// 7 GB" failure this store exists not to repeat.
//
// The cap is per namespace rather than global so one busy project cannot evict
// another's entries, and the retained set is the most recently *read* one, which
// is what a hit refreshes.
const MaxEntriesPerNamespace = 8

// refreshInterval bounds how often a hit rewrites an entry's mtime. Without it
// every invocation would touch the file; with it the touch costs at most one
// chtimes an hour on the fast path.
//
// It is an hour rather than a day because mtime is the only recency signal the
// per-namespace cap ranks by, and an entry is not refreshed until it is older
// than this. At a day's granularity every entry written since midnight is
// indistinguishable from every other, so the cap degrades to eviction by write
// order: the entry a project is actually built on — written once in the
// morning, hit all day — is the oldest of the set and the first to go, evicted
// by the incidental keys that a few `cd`s and a commit produce. That is exactly
// the entry the cache exists for. An hour keeps a working session's reads
// ordered while leaving the hit path effectively a read.
const refreshInterval = time.Hour

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

// Entry is what one evaluation of the chain produced, beyond the merged config
// itself: everything a caller would otherwise notice missing on a hit.
//
// Warnings and RemoteURLs are carried because a hit must be observationally
// identical to a miss. The validators warn to stderr on every evaluation, and
// `devtools verify-all` prints the remote configs the chain resolved; a hit that
// dropped either would make a command's output depend on whether a cache
// happened to be warm.
type Entry struct {
	Config     *config.Config
	Warnings   []string
	RemoteURLs []string
}

// artifact is the stored envelope. FormatVersion is repeated inside the file
// even though it is already folded into the key: a file whose name says one
// thing and whose body says another is corruption, and corruption must read as
// a miss.
//
// The entry itself is a nested msgpack blob rather than inline fields so that
// PayloadHash can cover it. A rename is atomic against a concurrent reader but
// not against a crash: on ext4 data=ordered a rename that lands before the data
// blocks leaves a present file whose tail is zeros, and trailing zeros decode as
// "0" / "" / "empty collection" — a structurally valid artifact holding a
// silently truncated config, which would then skip every validator. The hash
// turns that into a miss.
type artifact struct {
	FormatVersion int    `msgpack:"formatVersion"`
	PayloadHash   string `msgpack:"payloadHash"`
	Payload       []byte `msgpack:"payload"`
}

// payload is the hashed body of an artifact.
type payload struct {
	Config     *config.Config `msgpack:"config"`
	Warnings   []string       `msgpack:"warnings"`
	RemoteURLs []string       `msgpack:"remoteUrls"`
}

// decode reads data into v, converting a decoder panic into an error. The
// msgpack decoder indexes its input without bounds-checking every read, so a
// truncated file panics rather than erroring — and an escaping panic would both
// fail the command and leave the bad file on disk to fail every later one.
func decode(data []byte, v any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("corrupt config artifact: %v", r)
		}
	}()
	if err := msgpack.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode config artifact: %w", err)
	}
	return nil
}

// Load returns the cached entry for key, and whether it was a hit.
//
// Every failure mode is a miss, never an error: a truncated write, a partially
// deleted cache tree, a file from a binary that encoded a different shape. A
// config-evaluation cache that can fail a command is worse than no cache, so the
// only thing a bad entry earns is removal.
func (s *Store) Load(key string) (*Entry, bool) {
	p, err := s.pathFor(key)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}

	var art artifact
	if err := decode(data, &art); err != nil {
		s.discard(p)
		return nil, false
	}
	if art.FormatVersion != FormatVersion || hashutil.XXH3Hex(art.Payload) != art.PayloadHash {
		s.discard(p)
		return nil, false
	}

	var body payload
	if err := decode(art.Payload, &body); err != nil || body.Config == nil {
		s.discard(p)
		return nil, false
	}

	s.touch(p)
	return &Entry{Config: body.Config, Warnings: body.Warnings, RemoteURLs: body.RemoteURLs}, true
}

// Save writes entry under key.
//
// Setup content is dropped before encoding: ConfigSetup.Content holds a live
// goja value that cannot be serialized and must never be faked. The write goes
// through a sibling temp file and a rename, so a reader sees either the whole
// artifact or none of it. Entries are immutable per key, so two processes
// writing the same key concurrently is harmless — the loser's bytes are
// identical to the winner's.
func (s *Store) Save(key string, entry *Entry) error {
	if entry == nil || entry.Config == nil {
		return errors.New("config must not be nil")
	}
	p, err := s.pathFor(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create config cache directory: %w", err)
	}

	body, err := msgpack.Marshal(payload{
		Config:     withoutSetupContent(entry.Config),
		Warnings:   entry.Warnings,
		RemoteURLs: entry.RemoteURLs,
	})
	if err != nil {
		return fmt.Errorf("encode config artifact: %w", err)
	}
	encoded, err := msgpack.Marshal(artifact{
		FormatVersion: FormatVersion,
		PayloadHash:   hashutil.XXH3Hex(body),
		Payload:       body,
	})
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
	// Flush before the rename: without it a crash can publish a present file
	// whose blocks never landed, and the reader would have to distinguish that
	// from a real artifact.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("flush config artifact: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close config artifact: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install config artifact: %w", err)
	}

	Prune(filepath.Join(env.GetCachePath(), DirName), MaxAge, MaxEntriesPerNamespace)
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

// liveEntry is an artifact that survived the age cutoff, carried through the
// walk so the per-namespace cap can rank a directory's survivors without
// re-stat'ing them.
type liveEntry struct {
	path  string
	mtime time.Time
}

// Prune removes artifacts under root that have not been read for maxAge, plus
// any temp file an interrupted write abandoned, plus — per namespace directory —
// everything past the maxPerDir most recently read artifacts, and then any
// directory the removals emptied. Failures are ignored throughout:
// pruning is opportunistic maintenance on the miss path and must never turn a
// successful evaluation into an error.
func Prune(root string, maxAge time.Duration, maxPerDir int) {
	cutoff := time.Now().Add(-maxAge)
	var stale, dirs []string
	// live[dir] holds the artifacts that survived the age cutoff, so the
	// per-namespace cap is applied to what is actually left rather than to what
	// was found.
	live := make(map[string][]liveEntry)
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
		// Temp files are named ".{key}.{random}", so their extension is the random
		// suffix, not artifactExt. A write killed between CreateTemp and Rename
		// leaves one behind; unpruned it would live forever and keep its namespace
		// directory non-empty, which is the "no GC" failure this store exists to
		// avoid.
		if filepath.Ext(p) != artifactExt && !strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		// An entry that vanished mid-walk needs no pruning, and one younger than
		// the cutoff is still live.
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // see above: pruning must never fail a load
		}
		if !info.ModTime().Before(cutoff) {
			// Only real artifacts count against the cap. A temp file is a write in
			// flight or the debris of one; evicting a live artifact to make room for
			// it would be backwards.
			if filepath.Ext(p) == artifactExt && !strings.HasPrefix(d.Name(), ".") {
				dir := filepath.Dir(p)
				live[dir] = append(live[dir], liveEntry{path: p, mtime: info.ModTime()})
			}
			return nil
		}
		stale = append(stale, p)
		return nil
	})

	for _, entries := range live {
		if maxPerDir <= 0 || len(entries) <= maxPerDir {
			continue
		}
		// Newest read first, so the tail is the least recently used. touch()
		// refreshes an entry's mtime on a hit, which is what makes mtime the
		// read time rather than the write time.
		slices.SortFunc(entries, func(a, b liveEntry) int { return b.mtime.Compare(a.mtime) })
		for _, e := range entries[maxPerDir:] {
			stale = append(stale, e.path)
		}
	}

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
