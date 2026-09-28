package sourcehash

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHash(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/x.rs", "fn x() {}\n")
	write(t, root, "b.toml", "k = 1\n")
	base, err := Hash(root, []string{"a/x.rs", "b.toml"})
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	t.Run("the order of the list does not matter", func(t *testing.T) {
		if got, _ := Hash(root, []string{"b.toml", "a/x.rs"}); got != base {
			t.Errorf("Hash() = %s, want %s", got, base)
		}
	})
	t.Run("a CRLF checkout hashes the same", func(t *testing.T) {
		crlf := t.TempDir()
		write(t, crlf, "a/x.rs", "fn x() {}\r\n")
		write(t, crlf, "b.toml", "k = 1\r\n")
		if got, _ := Hash(crlf, []string{"a/x.rs", "b.toml"}); got != base {
			t.Errorf("Hash() = %s, want %s", got, base)
		}
	})
	t.Run("an edit moves it", func(t *testing.T) {
		edited := t.TempDir()
		write(t, edited, "a/x.rs", "fn y() {}\n")
		write(t, edited, "b.toml", "k = 1\n")
		if got, _ := Hash(edited, []string{"a/x.rs", "b.toml"}); got == base {
			t.Error("Hash() did not change with a source")
		}
	})
	t.Run("bytes cannot move between files", func(t *testing.T) {
		moved := t.TempDir()
		write(t, moved, "a/x.rs", "fn x() {}\nk = 1\n")
		write(t, moved, "b.toml", "")
		if got, _ := Hash(moved, []string{"a/x.rs", "b.toml"}); got == base {
			t.Error("Hash() did not tell where a file ends")
		}
	})
	t.Run("a missing source is an error", func(t *testing.T) {
		if _, err := Hash(root, []string{"gone.rs"}); err == nil {
			t.Error("Hash() error = nil for a missing file")
		}
	})
}

func TestSourcesAndRoot(t *testing.T) {
	root := t.TempDir()
	write(t, root, ListFile, "# comment\n\nparsers/a.rs\n  parsers/b.rs  \n")
	paths, err := Sources(root)
	if err != nil {
		t.Fatalf("Sources() error = %v", err)
	}
	if len(paths) != 2 || paths[0] != "parsers/a.rs" || paths[1] != "parsers/b.rs" {
		t.Errorf("Sources() = %v", paths)
	}
	nested := filepath.Join(root, "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := Root(nested); err != nil || got != root {
		t.Errorf("Root() = %q, %v; want %q", got, err, root)
	}
	if _, err := Root(t.TempDir()); err == nil {
		t.Error("Root() error = nil outside a repository")
	}
}
