package facts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// benchRepo builds a repository with a nested subdirectory to resolve from and
// returns that subdirectory. It skips when git is unavailable so both
// benchmarks below measure the same fixture or neither runs.
func benchRepo(b *testing.B) string {
	b.Helper()

	dir := b.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		b.Skipf("git is not available: %v", err)
	}
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		b.Fatal(err)
	}
	return sub
}

// BenchmarkGitRootPure and BenchmarkGitRootSubprocess are the pair that
// justifies the pure-Go walk: they resolve the same fixture the same number of
// times, with and without forking git. Neither is memoized, so this is the
// first-call cost the memo in GetGitRoot amortizes away.
func BenchmarkGitRootPure(b *testing.B) {
	sub := benchRepo(b)

	b.ReportAllocs()
	for b.Loop() {
		if _, ok := gitRootPure(sub); !ok {
			b.Fatal("gitRootPure() declined for a plain repository")
		}
	}
}

func BenchmarkGitRootSubprocess(b *testing.B) {
	sub := benchRepo(b)

	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := resolveGitRootViaGit(ctx, sub); err != nil {
			b.Fatalf("resolveGitRootViaGit() error = %v", err)
		}
	}
}
