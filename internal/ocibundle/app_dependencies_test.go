package ocibundle

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestExpectedSubtreesAppDependencies(t *testing.T) {
	for _, kind := range []string{"binary", "shell"} {
		t.Run(kind, func(t *testing.T) {
			storeRoot := testStore(t)
			cfg, want := testUVConfig(t)
			root := binaryReVerifyConfig(t).Apps["tool"]
			root.DependsOn = []string{"middle"}
			if kind == "shell" {
				root.Binary = nil
				root.Shell = &binmanager.AppConfigShell{Name: "echo"}
			}
			cfg.Apps["root"] = root
			cfg.Apps["middle"] = binmanager.App{Binary: binaryReVerifyConfig(t).Apps["tool"].Binary, DependsOn: []string{"mytool"}}
			dep := cfg.Apps["mytool"]
			dep.Required = false
			dep.Lazy = true
			cfg.Apps["mytool"] = dep
			got := expectedSubtrees(cfg, storeRoot, []string{"root", "unknown"}, nil)
			for subtree, owner := range want {
				if got[subtree] != owner {
					t.Errorf("missing dependency subtree %q (%s): %v", subtree, owner, got)
				}
			}
			owners := map[string]bool{}
			for _, owner := range got {
				owners[owner] = true
			}
			if !owners["middle"] || (kind == "binary" && !owners["root"]) {
				t.Fatalf("missing app subtrees: %v", got)
			}
		})
	}
}

func TestSeedAppDependencyClosure(t *testing.T) {
	storeRoot := testStore(t)
	cfg, expected := testUVConfig(t)
	cfg.Apps["root"] = binmanager.App{Shell: &binmanager.AppConfigShell{Name: "echo"}, DependsOn: []string{"mytool"}}
	src := newFakeSource()
	layers := make([]ocispec.Descriptor, 0, len(expected))
	for subtree := range expected {
		prefix := strings.TrimPrefix(testBuilderRoot, "/") + "/" + subtree
		entry := fileEntry(prefix+"/payload", []byte("fixture"))
		if strings.HasPrefix(subtree, ".runtimes/") {
			entry = fileEntry(prefix, []byte("uv binary"))
		}
		layer := subtreeLayer(t, subtree, []tarEntry{entry})
		layers = append(layers, src.addLayer(layer, subtree))
	}
	digest := src.addManifest(t, layers, nil)
	if err := seedFrom(t.Context(), cfg, src, "test/bundle", digest, nil, Options{Needed: []string{"root"}}); err != nil {
		t.Fatal(err)
	}
	if got := expectedSubtrees(cfg, storeRoot, []string{"root"}, nil); !maps.Equal(got, expected) {
		t.Fatalf("seed closure = %v, want %v", got, expected)
	}
	for subtree := range expected {
		if _, err := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(subtree))); err != nil {
			t.Errorf("dependency subtree not seeded: %v", err)
		}
	}
}
