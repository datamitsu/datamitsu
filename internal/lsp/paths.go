package lsp

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// uriToPath converts a file:// LSP document URI to a filesystem path.
// url.Parse already percent-decodes the path (e.g. %20 -> space, and VS Code's
// %3A after a Windows drive letter).
func uriToPath(uri string) (string, error) {
	return uriToPathFor(uri, runtime.GOOS)
}

// uriToPathFor is uriToPath for the given GOOS, so the Windows spelling is
// testable anywhere.
func uriToPathFor(uri, goos string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("parse uri %q: %w", uri, err)
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("unsupported uri scheme %q (only file:// is supported)", u.Scheme)
	}
	if u.Path == "" {
		return "", fmt.Errorf("uri %q has no path", uri)
	}
	if goos != "windows" {
		return u.Path, nil
	}
	path := u.Path
	switch {
	case u.Host != "" && !strings.EqualFold(u.Host, "localhost"):
		path = "//" + u.Host + path // file://server/share/x is a UNC path
	case len(path) >= 3 && path[0] == '/' && isDriveLetter(path[1]) && path[2] == ':':
		path = path[1:] // file:///C:/x names C:/x, not /C:/x
	}
	return strings.ReplaceAll(path, "/", `\`), nil
}

func isDriveLetter(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// canonicalPath is the one spelling every path the server compares is in:
// absolute, cleaned, symlinks resolved. The root comes from git, which reports
// the physical path, while an editor names documents the way the workspace was
// opened; compared unresolved, every file of a workspace opened through a
// symlink would be outside the root. A path that does not exist keeps its own
// name under its resolved parent, and a dangling symlink resolves to the file
// it names: that is the file a write creates, and the one the root must hold.
func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	return resolvePath(abs, maxLinkHops)
}

// maxLinkHops bounds how many dangling links resolvePath follows, so a cycle
// ends.
const maxLinkHops = 40

func resolvePath(abs string, hops int) string {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	dir, base := filepath.Split(abs)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if target, err := os.Readlink(abs); err == nil && hops > 0 {
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		return resolvePath(filepath.Clean(target), hops-1)
	}
	return filepath.Join(dir, base)
}

// within reports whether path is root or lies under it. Both are canonical.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
