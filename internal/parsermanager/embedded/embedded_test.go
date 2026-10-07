package embedded_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/parsermanager/embedded"
	"github.com/datamitsu/datamitsu/internal/parsermanager/embedded/sourcehash"
)

const stale = "embedded module is stale: run task build:parsers:embedded, or commit the " +
	"embedded-fallback-wasm artifact of the CI run as fallback.wasm and run " +
	"go run ./internal/parsermanager/embedded/cmd/sourcehash"

func root(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	r, err := sourcehash.Root(wd)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestTheModuleMatchesItsSources is the freshness check a contributor without
// Rust gets: a listed source that changed since the module was built moves the
// fingerprint away from the one committed beside it.
func TestTheModuleMatchesItsSources(t *testing.T) {
	r := root(t)
	current, err := sourcehash.Current(r)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := sourcehash.Committed(r)
	if err != nil {
		t.Fatal(err)
	}
	if current != committed {
		t.Fatalf("%s (sources hash %s, committed %s)", stale, current, committed)
	}
}

// testOnly are the crate's files the embedded build compiles only under test.
var testOnly = []string{
	"parsers/datamitsu-parsers/src/contract.rs",
	"parsers/datamitsu-parsers/src/format/fixtures.rs",
}

// TestEverySourceOfTheBuildIsListed keeps the list whole: a new file of the
// format build that is not listed would change the module without moving the
// fingerprint. The tool parsers are not in that build.
func TestEverySourceOfTheBuildIsListed(t *testing.T) {
	r := root(t)
	listed, err := sourcehash.Sources(r)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(r, "parsers", "datamitsu-parsers", "src")
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "tools" {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Ext(path) != ".rs" {
			return nil
		}
		rel, err := filepath.Rel(r, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !slices.Contains(listed, rel) && !slices.Contains(testOnly, rel) {
			t.Errorf("%s is compiled into the embedded module but not listed in %s", rel, sourcehash.ListFile)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range listed {
		if strings.Contains(path, "/src/tools/") {
			t.Errorf("%s is a tool parser, which the embedded module does not carry", path)
		}
	}
}

// TestTheModuleCarriesTheFormatsAlone describes the embedded bytes: the
// sniffer and the format parsers, no tool parser, answering in ABI 2.
func TestTheModuleCarriesTheFormatsAlone(t *testing.T) {
	caps, err := parsermanager.DescribeLocal(context.Background(), embedded.Module())
	if err != nil {
		t.Fatalf("describe the embedded module: %v", err)
	}
	if caps.SchemaVersion != parsermanager.SchemaABI2 || caps.ABI != 2 {
		t.Errorf("schema %d abi %d, want %d and 2", caps.SchemaVersion, caps.ABI, parsermanager.SchemaABI2)
	}
	names := make([]string, 0, len(caps.Tools))
	for _, tool := range caps.Tools {
		names = append(names, tool.Name)
		if tool.Kind != "format" {
			t.Errorf("%s has kind %q; the embedded module carries formats only", tool.Name, tool.Kind)
		}
	}
	for _, want := range []string{"fallback", "sarif", "checkstyle-xml", "gcc"} {
		if !slices.Contains(names, want) {
			t.Errorf("the embedded module does not describe %s: %v", want, names)
		}
	}
}

func TestContentKeyIsStable(t *testing.T) {
	first, second := embedded.ContentKey(), embedded.ContentKey()
	if first != second || len(first) != 32 {
		t.Errorf("ContentKey() = %q, then %q", first, second)
	}
}
