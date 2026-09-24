package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestURIToPathFor(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		goos    string
		want    string
		wantErr bool
	}{
		{name: "percent-decoded", uri: "file:///home/u/a%20b.go", goos: "linux", want: "/home/u/a b.go"},
		{name: "localhost host", uri: "file://localhost/home/u/a.go", goos: "darwin", want: "/home/u/a.go"},
		{name: "windows drive", uri: "file:///C:/Users/u/a.go", goos: "windows", want: `C:\Users\u\a.go`},
		{name: "windows drive, colon escaped as VS Code does", uri: "file:///c%3A/Users/u/a.go", goos: "windows", want: `c:\Users\u\a.go`},
		{name: "windows UNC", uri: "file://server/share/a.go", goos: "windows", want: `\\server\share\a.go`},
		{name: "not a file uri", uri: "untitled:Untitled-1", goos: "linux", wantErr: true},
		{name: "no path", uri: "file://", goos: "linux", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := uriToPathFor(tt.uri, tt.goos)
			if (err != nil) != tt.wantErr {
				t.Fatalf("uriToPathFor(%q) error = %v, wantErr %v", tt.uri, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("uriToPathFor(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

// Every spelling of one file canonicalizes to the same path, whether it exists
// or not.
func TestCanonicalPath(t *testing.T) {
	realDir := tempRoot(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeFile(t, filepath.Join(realDir, "a.txt"), "")
	if err := os.Mkdir(filepath.Join(realDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := tempRoot(t)
	for name, target := range map[string]string{
		"dangling.txt": filepath.Join("sub", "note.txt"),
		"out.txt":      filepath.Join(outside, "target.txt"),
		"loop-1":       "loop-2",
		"loop-2":       "loop-1",
	} {
		if err := os.Symlink(target, filepath.Join(realDir, name)); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct{ in, want string }{
		{in: filepath.Join(link, "a.txt"), want: filepath.Join(realDir, "a.txt")},
		{in: filepath.Join(link, "sub", "..", "a.txt"), want: filepath.Join(realDir, "a.txt")},
		{in: filepath.Join(link, "new.txt"), want: filepath.Join(realDir, "new.txt")},
		{in: link, want: realDir},
		// A dangling link names the file a write would create.
		{in: filepath.Join(link, "dangling.txt"), want: filepath.Join(realDir, "sub", "note.txt")},
		{in: filepath.Join(link, "out.txt"), want: filepath.Join(outside, "target.txt")},
		// A cycle ends, on a link the write then refuses.
		{in: filepath.Join(link, "loop-1"), want: filepath.Join(realDir, "loop-1")},
	}
	for _, tt := range tests {
		if got := canonicalPath(tt.in); got != tt.want {
			t.Errorf("canonicalPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWithin(t *testing.T) {
	root := filepath.FromSlash("/repo")
	tests := []struct {
		path string
		want bool
	}{
		{path: "/repo", want: true},
		{path: "/repo/a.go", want: true},
		{path: "/repo/..a/b", want: true},
		{path: "/repository/a.go", want: false},
		{path: "/other/a.go", want: false},
		{path: "/", want: false},
	}
	for _, tt := range tests {
		if got := within(root, filepath.FromSlash(tt.path)); got != tt.want {
			t.Errorf("within(%q, %q) = %v, want %v", root, tt.path, got, tt.want)
		}
	}
}
