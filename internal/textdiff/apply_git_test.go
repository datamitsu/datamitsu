package textdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/datamitsu/datamitsu/internal/gitenv"
)

// TestUnifiedGitApplies: the version control tool the patch is documented for
// takes it, names with spaces, tabs and quotes included.
func TestUnifiedGitApplies(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	names := []string{"plain.txt", "new dir/with space.txt", `quote".txt`}
	if runtime.GOOS != "windows" {
		names = append(names, "tab\tname.txt", `back\slash.txt`)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			const before, after = "one\ntwo\nthree\n", "one\nTWO\nthree\n"
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			patch := filepath.Join(t.TempDir(), "fix.patch")
			if err := os.WriteFile(patch, []byte(Unified(name, before, after)), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("git", "apply", patch)
			cmd.Dir = dir
			cmd.Env = append(gitenv.Environ(), "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git apply: %v\n%s\npatch:\n%s", err, out, Unified(name, before, after))
			}
			if got, _ := os.ReadFile(path); string(got) != after {
				t.Errorf("after git apply the file holds %q, want %q", got, after)
			}
		})
	}
}
