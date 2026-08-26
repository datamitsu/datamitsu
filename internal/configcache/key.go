// Package configcache computes the cache key for an evaluated config chain.
//
// The key is the whole risk of caching config evaluation: a key that is too
// narrow serves a wrong config silently, while a key that is too wide only
// costs a miss. Every input config JS can observe is folded in — the bytes of
// every chain file, the shape of the chain, the declared hash of every remote
// config, the entire environment, the allowlisted datamitsuConfigInputs, the
// JS-visible facts, cwd and git root, .git/HEAD, and the running version.
package configcache

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/datamitsu/datamitsu/internal/facts"
	"github.com/datamitsu/datamitsu/internal/hashutil"
)

// FormatVersion is the schema version of the stored artifact. Bump it whenever
// the encoded artifact's shape changes; every old entry then misses rather than
// decoding into a struct that no longer means the same thing.
const FormatVersion = 1

// ChainFile is one on-disk file of the resolved config chain. A file that does
// not exist is recorded with Exists=false and an empty hash: its appearance is
// what must change the key, and a missing file is a legitimate chain entry
// (a --config path pointing at a file a branch switch deleted).
type ChainFile struct {
	// Path is the absolute path, as recorded by setConfigChainFiles.
	Path string
	// ContentHash is the XXH3-128 hex digest of the file's bytes, empty when
	// the file does not exist. Content, not mtime+size: the cache must survive
	// a `git checkout`, a `git stash` and any rebuild that resets timestamps.
	ContentHash string
	// Exists distinguishes an empty file from a missing one.
	Exists bool
}

// AutoConfigCandidate is one of the file names config discovery stats at the
// git root. Every candidate is recorded, not just the one that was chosen:
// discovery refuses to load when more than one exists, so a tree that gains a
// second candidate stops being loadable while every other input is unchanged.
// Mirrors sourcefarm.WatchPaths.
type AutoConfigCandidate struct {
	Path   string
	Exists bool
}

// RemoteConfig is one getRemoteConfigs() entry. Remote configs are
// content-addressed by construction — the declared SHA-256 is verified before
// the content is used — so hashing the URL and the declared hash is equivalent
// to hashing the content.
type RemoteConfig struct {
	URL  string
	Hash string
}

// ConfigInputs mirrors the fields injected into the VM as the frozen
// datamitsuConfigInputs global (internal/engine/configinputs.go). Every field
// there IS a config evaluation input and therefore MUST appear here; the
// agreement is pinned by TestConfigInputsMatchEngine.
type ConfigInputs struct {
	MinimumReleaseAgeMinutes int `json:"minimumReleaseAgeMinutes"`
}

// Facts is the JS-visible subset of internal/facts.Facts. The environment is
// deliberately NOT here — it enters the key through Inputs.Environ, whole and
// sorted, because config JS reads all of it through facts().env.
type Facts struct {
	BinaryCommand string
	BinaryPath    string
	PackageName   string
	Version       string
	OS            string
	Arch          string
	Libc          string
	IsInGitRepo   bool
	IsMonorepo    bool
}

// FactsFrom projects the JS-visible fields out of collected facts.
func FactsFrom(f *facts.Facts) Facts {
	if f == nil {
		return Facts{}
	}
	return Facts{
		BinaryCommand: f.BinaryCommand,
		BinaryPath:    f.BinaryPath,
		PackageName:   f.PackageName,
		Version:       f.Version,
		OS:            f.OS,
		Arch:          f.Arch,
		Libc:          f.Libc,
		IsInGitRepo:   f.IsInGitRepo,
		IsMonorepo:    f.IsMonorepo,
	}
}

// Inputs is everything the evaluated config is a function of.
type Inputs struct {
	// FormatVersion is the artifact schema version; a bump invalidates every
	// entry written by an older binary.
	FormatVersion int
	// Version is ldflags.Version. It is not optional: validation runs before
	// the artifact is stored, so a binary that validates differently must
	// produce a different key.
	Version string

	// ChainFiles are the chain's files in chain order. Order is significant —
	// the same set of files merged in a different order is a different config.
	ChainFiles []ChainFile
	// NoAutoConfig records the --no-auto-config flag, which changes the chain
	// shape without changing any file.
	NoAutoConfig bool
	// AutoConfigCandidates are every candidate name at the git root, chosen or
	// not, with its existence.
	AutoConfigCandidates []AutoConfigCandidate
	// RemoteConfigs are the getRemoteConfigs() entries in resolution order.
	//
	// A caller that computes the key BEFORE evaluating the chain cannot know
	// them and leaves this empty, which is sound: the declared entries are a
	// pure function of the chain bytes and of the other inputs the config reads
	// to declare them, all of which are already here. It is carried for callers
	// that do know the chain up front, and because a key that names its inputs
	// is worth more than one that relies on that derivation being remembered.
	RemoteConfigs []RemoteConfig
	// SkipRemoteConfig records --skip-remote-config, which drops every remote
	// layer from the merged result. Unlike the declared entries it is NOT
	// derivable from the chain bytes, so it must be hashed explicitly.
	SkipRemoteConfig bool

	// Environ is the ENTIRE environment as "NAME=VALUE" strings, not the
	// DATAMITSU_*-only env.Environ(): the shared config branches on CI,
	// and a DATAMITSU_*-only key is defeated by that alone. Sorted by Key, so
	// an unordered slice cannot produce an unstable key.
	Environ []string

	// ConfigInputs are the allowlisted runtime values injected into the VM.
	ConfigInputs ConfigInputs
	// Facts are the JS-visible host facts.
	Facts Facts

	// CWD and GitRoot are hashed separately: isMonorepo is derived from their
	// relationship, and setup content receives paths computed relative to cwd.
	CWD     string
	GitRoot string

	// GitHead is the content of the resolved .git/HEAD, empty outside a
	// repository. A branch switch can add, delete or change chain files.
	GitHead string
}

// Key returns the XXH3-128 hex digest of in.
//
// Every part is labeled and length-delimited through XXH3Multi's separator, so
// no two differently-shaped inputs can serialize to the same byte stream.
func Key(in Inputs) string {
	parts := make([][]byte, 0, 24+len(in.ChainFiles)+len(in.AutoConfigCandidates)+len(in.RemoteConfigs)+len(in.Environ))

	parts = append(parts,
		[]byte("v"), []byte(strconv.Itoa(in.FormatVersion)),
		[]byte("version"), []byte(in.Version),
		[]byte("noAutoConfig"), []byte(strconv.FormatBool(in.NoAutoConfig)),
		[]byte("skipRemoteConfig"), []byte(strconv.FormatBool(in.SkipRemoteConfig)),
		[]byte("cwd"), []byte(in.CWD),
		[]byte("gitRoot"), []byte(in.GitRoot),
		[]byte("gitHead"), []byte(in.GitHead),
	)

	parts = append(parts, []byte("chain"), []byte(strconv.Itoa(len(in.ChainFiles))))
	for _, f := range in.ChainFiles {
		parts = append(parts, fmt.Appendf(nil, "%s\x1f%s\x1f%t", f.Path, f.ContentHash, f.Exists))
	}

	parts = append(parts, []byte("auto"), []byte(strconv.Itoa(len(in.AutoConfigCandidates))))
	for _, c := range in.AutoConfigCandidates {
		parts = append(parts, fmt.Appendf(nil, "%s\x1f%t", c.Path, c.Exists))
	}

	parts = append(parts, []byte("remote"), []byte(strconv.Itoa(len(in.RemoteConfigs))))
	for _, r := range in.RemoteConfigs {
		parts = append(parts, fmt.Appendf(nil, "%s\x1f%s", r.URL, r.Hash))
	}

	parts = append(parts, []byte("configInputs"),
		fmt.Appendf(nil, "minimumReleaseAgeMinutes=%d", in.ConfigInputs.MinimumReleaseAgeMinutes))

	f := in.Facts
	parts = append(parts, []byte("facts"), fmt.Appendf(nil,
		"%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%t\x1f%t",
		f.BinaryCommand, f.BinaryPath, f.PackageName, f.Version,
		f.OS, f.Arch, f.Libc, f.IsInGitRepo, f.IsMonorepo))

	sortedEnv := append([]string(nil), in.Environ...)
	sort.Strings(sortedEnv)
	parts = append(parts, []byte("env"), []byte(strconv.Itoa(len(sortedEnv))))
	for _, kv := range sortedEnv {
		parts = append(parts, []byte(kv))
	}

	return hashutil.XXH3Multi(parts...)
}

// HashChainFile reads path and returns its ChainFile entry. A file that cannot
// be read — missing, or unreadable — records as absent rather than failing the
// load: the key then differs from the key a readable file produces, which is a
// miss, never a wrong hit.
func HashChainFile(path string) ChainFile {
	data, err := os.ReadFile(path)
	if err != nil {
		return ChainFile{Path: path, Exists: false}
	}
	return ChainFile{Path: path, ContentHash: hashutil.XXH3Hex(data), Exists: true}
}

// ConfigInputKeys returns the JSON key names of ConfigInputs, sorted. It is
// compared against the engine's injected key set so a new
// datamitsuConfigInputs field cannot silently skip the cache key.
func ConfigInputKeys() []string {
	t := reflect.TypeFor[ConfigInputs]()
	keys := make([]string, 0, t.NumField())
	for f := range t.Fields() {
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			tag = tag[:comma]
		}
		if tag == "" {
			continue
		}
		keys = append(keys, tag)
	}
	sort.Strings(keys)
	return keys
}
