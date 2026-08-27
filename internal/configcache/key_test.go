package configcache

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/engine"
	"github.com/datamitsu/datamitsu/internal/facts"
)

// baseInputs is a fully populated Inputs — every field non-zero, so a mutation
// in the change table is always a change away from something, never away from
// the zero value.
func baseInputs() Inputs {
	return Inputs{
		FormatVersion:  FormatVersion,
		Version:        "1.2.3",
		BinaryIdentity: "0123456789abcdef0123456789abcdef",
		ColorEnabled:   true,
		ChainFiles: []ChainFile{
			{Path: "/repo/datamitsu.config.js", ContentHash: "aaaa", Exists: true},
			{Path: "/repo/pkg/datamitsu.config.js", ContentHash: "bbbb", Exists: true},
		},
		NoAutoConfig: false,
		AutoConfigCandidates: []AutoConfigCandidate{
			{Path: "/repo/datamitsu.config.js", Exists: true},
			{Path: "/repo/datamitsu.config.mjs", Exists: false},
			{Path: "/repo/datamitsu.config.ts", Exists: false},
		},
		RemoteConfigs: []RemoteConfig{
			{URL: "https://example.test/c.js", Hash: "sha256-1"},
		},
		Environ:      []string{"CI=1", "HOME=/home/u", "PATH=/usr/bin"},
		ConfigInputs: ConfigInputs{MinimumReleaseAgeMinutes: 10080},
		Facts: Facts{
			BinaryCommand: "datamitsu",
			BinaryPath:    "/usr/local/bin/datamitsu",
			PackageName:   "datamitsu",
			Version:       "1.2.3",
			OS:            "linux",
			Arch:          "amd64",
			Libc:          "glibc",
			IsInGitRepo:   true,
			IsMonorepo:    true,
		},
		CWD:     "/repo/pkg",
		GitRoot: "/repo",
		GitHead: "ref: refs/heads/main",
	}
}

func TestKeyIsHex128(t *testing.T) {
	k := Key(baseInputs())
	if len(k) != 32 {
		t.Fatalf("expected 32 hex chars, got %d: %s", len(k), k)
	}
	if _, err := hex.DecodeString(k); err != nil {
		t.Fatalf("key is not hex: %s", k)
	}
}

func TestKeyIsStableAcrossCalls(t *testing.T) {
	in := baseInputs()
	first := Key(in)
	for i := range 5 {
		if got := Key(baseInputs()); got != first {
			t.Fatalf("call %d: key changed for identical inputs: %s != %s", i, got, first)
		}
	}
}

func TestKeyIgnoresEnvironOrder(t *testing.T) {
	in := baseInputs()
	shuffled := baseInputs()
	slices.Reverse(shuffled.Environ)
	if Key(in) != Key(shuffled) {
		t.Fatal("key depends on the order of Environ; it must sort")
	}
}

// TestKeyChangesForEveryInput is the table that proves nothing was forgotten:
// one subtest per input the evaluated config can depend on.
func TestKeyChangesForEveryInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Inputs)
	}{
		{"format version", func(in *Inputs) { in.FormatVersion++ }},
		{"binary version", func(in *Inputs) { in.Version = "9.9.9" }},
		// Every local build reports Version "dev", so this is the only input
		// that separates one `go build` from the next.
		{"binary identity", func(in *Inputs) { in.BinaryIdentity = "ffffffffffffffffffffffffffffffff" }},
		{"color enabled", func(in *Inputs) { in.ColorEnabled = false }},

		{"chain file content", func(in *Inputs) { in.ChainFiles[0].ContentHash = "cccc" }},
		{"chain file path", func(in *Inputs) { in.ChainFiles[0].Path = "/repo/other.config.js" }},
		{"chain file existence", func(in *Inputs) {
			in.ChainFiles[1].Exists = false
			in.ChainFiles[1].ContentHash = ""
		}},
		{"chain file order", func(in *Inputs) {
			in.ChainFiles[0], in.ChainFiles[1] = in.ChainFiles[1], in.ChainFiles[0]
		}},
		{"chain file added", func(in *Inputs) {
			in.ChainFiles = append(in.ChainFiles, ChainFile{Path: "/x.js", ContentHash: "dddd", Exists: true})
		}},
		{"chain file removed", func(in *Inputs) { in.ChainFiles = in.ChainFiles[:1] }},

		{"no-auto-config flag", func(in *Inputs) { in.NoAutoConfig = true }},
		{"unchosen auto-config candidate appeared", func(in *Inputs) {
			in.AutoConfigCandidates[1].Exists = true
		}},
		{"auto-config candidate path", func(in *Inputs) {
			in.AutoConfigCandidates[0].Path = "/elsewhere/datamitsu.config.js"
		}},

		{"remote config url", func(in *Inputs) { in.RemoteConfigs[0].URL = "https://example.test/d.js" }},
		{"remote config hash", func(in *Inputs) { in.RemoteConfigs[0].Hash = "sha256-2" }},
		{"remote config added", func(in *Inputs) {
			in.RemoteConfigs = append(in.RemoteConfigs, RemoteConfig{URL: "u", Hash: "h"})
		}},
		{"skip-remote-config flag", func(in *Inputs) { in.SkipRemoteConfig = true }},

		{"environment value", func(in *Inputs) { in.Environ[0] = "CI=0" }},
		{"environment variable added", func(in *Inputs) {
			in.Environ = append(in.Environ, "DATAMITSU_DEV_MODE=1")
		}},
		{"environment variable removed", func(in *Inputs) { in.Environ = in.Environ[1:] }},

		{"minimumReleaseAgeMinutes", func(in *Inputs) { in.ConfigInputs.MinimumReleaseAgeMinutes = 60 }},

		{"facts.binaryCommand", func(in *Inputs) { in.Facts.BinaryCommand = "dm" }},
		{"facts.binaryPath", func(in *Inputs) { in.Facts.BinaryPath = "/opt/datamitsu" }},
		{"facts.packageName", func(in *Inputs) { in.Facts.PackageName = "other" }},
		{"facts.version", func(in *Inputs) { in.Facts.Version = "0.0.1" }},
		{"facts.os", func(in *Inputs) { in.Facts.OS = "darwin" }},
		{"facts.arch", func(in *Inputs) { in.Facts.Arch = "arm64" }},
		{"facts.libc", func(in *Inputs) { in.Facts.Libc = "musl" }},
		{"facts.isInGitRepo", func(in *Inputs) { in.Facts.IsInGitRepo = false }},
		{"facts.isMonorepo", func(in *Inputs) { in.Facts.IsMonorepo = false }},

		{"cwd", func(in *Inputs) { in.CWD = "/repo/other" }},
		{"git root", func(in *Inputs) { in.GitRoot = "/other" }},
		{"git HEAD", func(in *Inputs) { in.GitHead = "ref: refs/heads/feature" }},
	}

	base := Key(baseInputs())
	seen := map[string]string{base: "base"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := baseInputs()
			tt.mutate(&in)
			got := Key(in)
			if got == base {
				t.Fatalf("key unchanged after changing %s", tt.name)
			}
			if other, dup := seen[got]; dup {
				t.Fatalf("key collides with %q after changing %s", other, tt.name)
			}
			seen[got] = tt.name
		})
	}
}

// TestKeyCoversEveryInputsField fails when a field is added to Inputs without a
// row in the change table above: the table is only proof of completeness while
// it enumerates the whole struct.
func TestKeyCoversEveryInputsField(t *testing.T) {
	want := []string{
		"AutoConfigCandidates", "BinaryIdentity", "CWD", "ChainFiles",
		"ColorEnabled", "ConfigInputs", "Environ", "Facts", "FormatVersion",
		"GitHead", "GitRoot", "NoAutoConfig", "RemoteConfigs",
		"SkipRemoteConfig", "Version",
	}
	t.Run("Inputs", func(t *testing.T) { assertFields(t, reflect.TypeFor[Inputs](), want) })
	t.Run("Facts", func(t *testing.T) {
		assertFields(t, reflect.TypeFor[Facts](), []string{
			"Arch", "BinaryCommand", "BinaryPath", "IsInGitRepo", "IsMonorepo",
			"Libc", "OS", "PackageName", "Version",
		})
	})
}

func assertFields(t *testing.T, typ reflect.Type, want []string) {
	t.Helper()
	got := make([]string, 0, typ.NumField())
	for f := range typ.Fields() {
		got = append(got, f.Name)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("%s fields changed: got %v, want %v — add the new input to Key and to the change table", typ.Name(), got, want)
	}
}

// TestConfigInputsMatchEngine is the guard the Runtime Config vs Config Inputs
// Policy asks for: a field added to datamitsuConfigInputs must be folded into
// the cache key, and this fails until it is.
func TestConfigInputsMatchEngine(t *testing.T) {
	injected := engine.ConfigInputKeys()
	mirrored := ConfigInputKeys()
	if !slices.Equal(injected, mirrored) {
		t.Fatalf("datamitsuConfigInputs keys %v are not mirrored by configcache.ConfigInputs %v — every injected config input must enter the cache key", injected, mirrored)
	}
	if len(injected) == 0 {
		t.Fatal("engine.ConfigInputKeys returned nothing; the guard would pass vacuously")
	}
}

func TestHashChainFile(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "datamitsu.config.js")
	if err := os.WriteFile(present, []byte("export default {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("existing file hashes its content", func(t *testing.T) {
		got := HashChainFile(present)
		if !got.Exists || got.ContentHash == "" {
			t.Fatalf("expected an existing hashed file, got %+v", got)
		}
		if got.Path != present {
			t.Fatalf("path: got %s, want %s", got.Path, present)
		}
	})

	t.Run("content change changes the hash", func(t *testing.T) {
		before := HashChainFile(present).ContentHash
		if err := os.WriteFile(present, []byte("export default {a:1}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if after := HashChainFile(present).ContentHash; after == before {
			t.Fatal("hash unchanged after the file's content changed")
		}
	})

	t.Run("missing file records as absent", func(t *testing.T) {
		got := HashChainFile(filepath.Join(dir, "nope.js"))
		if got.Exists || got.ContentHash != "" {
			t.Fatalf("expected an absent entry, got %+v", got)
		}
	})

	t.Run("empty and missing differ", func(t *testing.T) {
		empty := filepath.Join(dir, "empty.js")
		if err := os.WriteFile(empty, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		in := baseInputs()
		in.ChainFiles = []ChainFile{HashChainFile(empty)}
		missing := baseInputs()
		missing.ChainFiles = []ChainFile{{Path: empty}}
		if Key(in) == Key(missing) {
			t.Fatal("an empty file and a missing file produce the same key")
		}
	})
}

func TestFactsFrom(t *testing.T) {
	t.Run("nil facts", func(t *testing.T) {
		if got := FactsFrom(nil); got != (Facts{}) {
			t.Fatalf("expected zero Facts, got %+v", got)
		}
	})

	t.Run("projects the JS-visible fields", func(t *testing.T) {
		f := &facts.Facts{
			PackageName: "datamitsu", Version: "1.2.3",
			BinaryCommand: "datamitsu", BinaryPath: "/usr/bin/datamitsu",
			OS: "linux", Arch: "amd64", Libc: "glibc",
			IsInGitRepo: true, IsMonorepo: true,
			Env: map[string]string{"CI": "1"},
		}
		got := FactsFrom(f)
		want := Facts{
			BinaryCommand: "datamitsu", BinaryPath: "/usr/bin/datamitsu",
			PackageName: "datamitsu", Version: "1.2.3",
			OS: "linux", Arch: "amd64", Libc: "glibc",
			IsInGitRepo: true, IsMonorepo: true,
		}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	// The environment is deliberately not part of Facts: it enters the key
	// whole through Inputs.Environ, because config JS reads all of it.
	t.Run("environment is not carried by Facts", func(t *testing.T) {
		typ := reflect.TypeFor[Facts]()
		if _, ok := typ.FieldByName("Env"); ok {
			t.Fatal("Facts must not carry Env; the whole environment belongs in Inputs.Environ")
		}
	})
}
