package ocibundle

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLayer(t *testing.T, entries []struct {
	name string
	kind byte
	body string
},
) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "layer.tar.gz")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.kind, Mode: 0o755, Size: int64(len(e.body))}
		if e.kind == tar.TypeSymlink {
			h.Linkname = e.body
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.kind == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestExtractLayerSubtreeRejectsUnsafeStageAndEntryParents(t *testing.T) {
	subtree := ".bin/tool/" + strings.Repeat("a", 32)
	blob := writeLayer(t, []struct {
		name string
		kind byte
		body string
	}{
		{"dm/store/" + subtree, tar.TypeDir, ""},
		{"dm/store/" + subtree + "/payload", tar.TypeReg, "valid"},
	})
	for _, tc := range []struct {
		name    string
		prepare func(string, string) error
	}{
		{"staging-parent-file", func(base, _ string) error { return os.WriteFile(filepath.Join(base, "stage"), []byte("file"), 0o600) }},
		{"symlink-escape", func(base, _ string) error {
			outside := filepath.Join(base, "outside")
			if err := os.Mkdir(outside, 0o755); err != nil {
				return err
			}
			return os.Symlink(outside, filepath.Join(base, "stage", "content"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			stage := filepath.Join(base, "stage")
			if tc.name == "symlink-escape" {
				if err := os.Mkdir(stage, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := tc.prepare(base, stage); err != nil {
				t.Fatal(err)
			}
			_, err := extractLayerSubtree(blob, compressionGzip, layerExtractOptions{Subtree: subtree, BuilderStoreRoot: "/dm/store", ConsumerStoreRoot: filepath.Join(base, "store")}, stage)
			if err == nil {
				t.Fatal("unsafe staging path accepted")
			}
			entries, _ := os.ReadDir(filepath.Join(base, "outside"))
			if len(entries) != 0 {
				t.Fatalf("outside data created: %v", entries)
			}
		})
	}
}

func TestPlaceSubtreeDestinationConflictPreservesPayloadAndStaging(t *testing.T) {
	root := t.TempDir()
	subtree := ".bin/tool/" + strings.Repeat("b", 32)
	dest := filepath.Join(root, filepath.FromSlash(subtree))
	staged := filepath.Join(root, "tmp", "staged")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "payload"), []byte("destination"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "payload"), []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := placeSubtree(staged, dest, subtree, root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "payload"))
	if err != nil || string(got) != "destination" {
		t.Fatalf("destination = %q, %v", got, err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staging remains after winning payload: %v", err)
	}
}

func TestExtractLayerReadFailureLeavesNoEscapedWrites(t *testing.T) {
	stage := t.TempDir()
	_, err := extractLayerSubtree(filepath.Join(stage, "missing"), compressionGzip, layerExtractOptions{Subtree: ".bin/tool/" + strings.Repeat("c", 32), BuilderStoreRoot: "/dm/store"}, stage)
	if err == nil {
		t.Fatal("missing blob read succeeded")
	}
	entries, readErr := os.ReadDir(stage)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed read left staging artifacts: %v", entries)
	}
}
