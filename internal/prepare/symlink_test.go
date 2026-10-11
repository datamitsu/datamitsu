package prepare

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateOutputSymlinkChainEscape(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "inner"), "x")
	// alias -> . ; chain -> alias/../.. escapes through the inner link,
	// although a lexical Clean accepts it.
	if err := os.Symlink(".", filepath.Join(out, "alias")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink("alias/../../escape", filepath.Join(out, "chain")); err != nil {
		t.Skip("symlinks unavailable")
	}
	err := validateOutput(out)
	if err == nil {
		t.Fatal("chain through inner symlink accepted")
	}
}

func TestValidateOutputDanglingInsideSymlinkAllowed(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "real"), "x")
	if err := os.Symlink("not-created-yet", filepath.Join(out, "later")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := validateOutput(out); err != nil {
		t.Fatalf("dangling in-tree symlink rejected: %v", err)
	}
}

func TestValidateOutputChainThroughInnerLinkDanglingTail(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "inner"), "x")
	// down -> . and link -> down/missing: lexically inside, and the
	// traversal of the components that exist (down resolves to out itself)
	// stays inside — the missing tail is a dangling link, allowed.
	if err := os.Symlink(".", filepath.Join(out, "down")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink("down/missing", filepath.Join(out, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := validateOutput(out); err != nil {
		t.Fatalf("dangling tail through an in-tree link rejected: %v", err)
	}
	// The same shape with an escaping intermediate: escape -> ../../x is
	// itself an escaping symlink and is rejected on its own walk visit, so
	// a chain through it never gets a chance to pass.
	if err := os.Symlink("../../x", filepath.Join(out, "escape")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink("escape/missing", filepath.Join(out, "chain")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := validateOutput(out); err == nil {
		t.Fatal("chain through an escaping link accepted")
	}
}

func TestValidateOutputSymlinkedRootKeepsInsideLinks(t *testing.T) {
	// The store root (a t.TempDir under a symlinked /var on macOS, or a
	// user-configured symlink) must not make in-tree links read as escapes:
	// the canonical root comparison accepts them.
	base := t.TempDir()
	realRoot := filepath.Join(base, "real-store")
	out := filepath.Join(realRoot, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(out, "data"), "x")
	if err := os.Symlink("data", filepath.Join(out, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := validateOutput(out); err != nil {
		t.Fatalf("in-tree link under a symlinked root rejected: %v", err)
	}
}

func TestValidateOutputDotLinkThenParentOutsideRejected(t *testing.T) {
	// The kernel resolves each symlink component BEFORE applying a later
	// "..": with alias -> . inside out/, the target alias/../outside lands
	// NEXT TO out/, not inside it. A lexical Clean cannot see this — the
	// component-wise traversal must.
	base := t.TempDir()
	out := filepath.Join(base, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(out, "inner"), "x")
	writeFile(t, filepath.Join(base, "outside"), "x")
	if err := os.Symlink(".", filepath.Join(out, "alias")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink("alias/../outside", filepath.Join(out, "chain")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := validateOutput(out); err == nil {
		t.Fatal("dot-link then .. escape accepted")
	}
	// The same shape with a missing outside target: still an escape — the
	// existing components already route the traversal outside.
	if err := os.Remove(filepath.Join(base, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := validateOutput(out); err == nil {
		t.Fatal("dot-link then .. escape to a missing target accepted")
	}
}

func TestValidateOutputNestedLinkThenParentOutsideRejected(t *testing.T) {
	// One more indirection: dot -> ., alias -> dot, chain ->
	// alias/../outside. A Join-based rewriter collapses alias/../outside to
	// outside before alias is resolved, accepting out/outside; the kernel
	// resolves alias (-> dot -> out/) first, then .. climbs to the sibling
	// outside out/. The uncleaned component queue must catch both.
	base := t.TempDir()
	out := filepath.Join(base, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(out, "inner"), "x")
	writeFile(t, filepath.Join(base, "outside"), "x")
	for _, link := range []struct{ name, target string }{
		{"dot", "."},
		{"alias", "dot"},
		{"chain", "alias/../outside"},
	} {
		if err := os.Symlink(link.target, filepath.Join(out, link.name)); err != nil {
			t.Skip("symlinks unavailable")
		}
	}
	if err := validateOutput(out); err == nil {
		t.Fatal("nested dot-link then .. escape accepted")
	}
	if err := os.Remove(filepath.Join(base, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := validateOutput(out); err == nil {
		t.Fatal("nested dot-link then .. escape to a missing target accepted")
	}
}
