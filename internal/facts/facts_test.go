package facts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/ldflags"
)

func TestCollectAllEnv(t *testing.T) {
	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				_ = os.Setenv(parts[0], parts[1]) //nolint:usetesting // restoring the full pre-cleared environment; t.Setenv cannot rebuild it
			}
		}
	}()

	os.Clearenv()
	_ = os.Setenv("TEST_VAR1", "value1") //nolint:usetesting // env is cleared first to assert exact count; t.Setenv cannot clear/restore the full env
	_ = os.Setenv("TEST_VAR2", "value2") //nolint:usetesting // env is cleared first to assert exact count; t.Setenv cannot clear/restore the full env
	_ = os.Setenv("OTHER_VAR", "other")  //nolint:usetesting // env is cleared first to assert exact count; t.Setenv cannot clear/restore the full env

	envMap := collectAllEnv()

	if len(envMap) != 3 {
		t.Errorf("len(envMap) = %d, want 3", len(envMap))
	}

	if envMap["TEST_VAR1"] != "value1" {
		t.Errorf("TEST_VAR1 = %q, want %q", envMap["TEST_VAR1"], "value1")
	}

	if envMap["TEST_VAR2"] != "value2" {
		t.Errorf("TEST_VAR2 = %q, want %q", envMap["TEST_VAR2"], "value2")
	}

	if envMap["OTHER_VAR"] != "other" {
		t.Errorf("OTHER_VAR = %q, want %q", envMap["OTHER_VAR"], "other")
	}
}

func TestCollect(t *testing.T) {
	ctx := context.Background()

	facts, _, err := Collect(ctx, "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if facts.PackageName != ldflags.PackageName {
		t.Errorf("PackageName = %q, want %q", facts.PackageName, ldflags.PackageName)
	}

	if facts.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", facts.OS, runtime.GOOS)
	}

	if facts.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", facts.Arch, runtime.GOARCH)
	}

	if facts.BinaryPath == "" {
		t.Error("BinaryPath is empty")
	}

	if facts.Env == nil {
		t.Error("Env is nil")
	}
}

func TestCollectLibcField(t *testing.T) {
	ctx := context.Background()

	facts, _, err := Collect(ctx, "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if facts.Libc == "" {
		t.Error("Libc is empty, should be glibc, musl, or unknown")
	}

	validValues := map[string]bool{"glibc": true, "musl": true, "unknown": true}
	if !validValues[facts.Libc] {
		t.Errorf("Libc = %q, want one of glibc, musl, unknown", facts.Libc)
	}

	if runtime.GOOS != "linux" && facts.Libc != "unknown" {
		t.Errorf("Libc = %q on non-Linux OS %q, want \"unknown\"", facts.Libc, runtime.GOOS)
	}
}

func TestCollectWithBinaryCommandOverride(t *testing.T) {
	ctx := context.Background()
	override := "/custom/binary/path"

	facts, _, err := Collect(ctx, override)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if facts.BinaryCommand != override {
		t.Errorf("BinaryCommand = %q, want %q", facts.BinaryCommand, override)
	}
}

func TestCollectWithEnvOverride(t *testing.T) {
	ctx := context.Background()
	envOverride := "/env/binary/path"
	t.Setenv("DATAMITSU_BINARY_COMMAND", envOverride)

	facts, _, err := Collect(ctx, "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if facts.BinaryCommand != envOverride {
		t.Errorf("BinaryCommand = %q, want %q", facts.BinaryCommand, envOverride)
	}
}

func TestCollectInGitRepo(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to initialize git repo: %v", err)
	}

	t.Chdir(tmpDir)

	ctx := context.Background()
	facts, _, err := Collect(ctx, "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if !facts.IsInGitRepo {
		t.Error("IsInGitRepo = false, want true")
	}

	if facts.IsMonorepo {
		t.Error("IsMonorepo = true, want false (at git root)")
	}
}

func TestCollectInMonorepo(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to initialize git repo: %v", err)
	}

	subdir := filepath.Join(tmpDir, "packages", "app")
	_ = os.MkdirAll(subdir, 0o755)

	t.Chdir(subdir)

	ctx := context.Background()
	facts, _, err := Collect(ctx, "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if !facts.IsInGitRepo {
		t.Error("IsInGitRepo = false, want true")
	}

	if !facts.IsMonorepo {
		t.Error("IsMonorepo = false, want true (in subdirectory)")
	}
}

func TestCollectNotInGitRepo(t *testing.T) {
	tmpDir := t.TempDir()

	t.Chdir(tmpDir)

	ctx := context.Background()
	facts, _, err := Collect(ctx, "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if facts.IsInGitRepo {
		t.Error("IsInGitRepo = true, want false")
	}
}

func TestGetGitRoot(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to initialize git repo: %v", err)
	}

	t.Chdir(tmpDir)

	ctx := context.Background()

	root, err := GetGitRoot(ctx)
	if err != nil {
		t.Fatalf("GetGitRoot() error = %v", err)
	}

	if root == "" {
		t.Error("GetGitRoot() returned empty string")
	}

	absRoot, _ := filepath.EvalSymlinks(root)
	absTmpDir, _ := filepath.EvalSymlinks(tmpDir)

	if absRoot != absTmpDir {
		t.Errorf("GetGitRoot() = %q, want %q", absRoot, absTmpDir)
	}
}

func TestGetGitRootNotGitRepo(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	tmpDir := t.TempDir()

	t.Chdir(tmpDir)

	ctx := context.Background()

	_, err := GetGitRoot(ctx)
	if err == nil {
		t.Error("GetGitRoot() should return error for non-git directory")
	}
}

func isGitAvailable() bool {
	cmd := exec.Command("git", "--version")
	return cmd.Run() == nil
}

// brokenGitContext simulates a repo where git itself cannot run: a .git
// directory exists, but PATH holds no git binary (containers with
// dubious-ownership failures behave the same way at this level).
func brokenGitContext(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tmpDir)
	t.Setenv("PATH", tmpDir)
}

func TestCollectBrokenGitIsFatalByDefault(t *testing.T) {
	brokenGitContext(t)

	_, _, err := Collect(context.Background(), "")
	if err == nil {
		t.Fatal("Collect() should fail when .git exists but git cannot run")
	}
	if !strings.Contains(err.Error(), "git command failed") {
		t.Errorf("err = %v, want the broken-git explanation", err)
	}
}

func TestCollectBrokenGitToleratedDegradesToNoRepo(t *testing.T) {
	brokenGitContext(t)

	facts, gitRoot, err := CollectWithOptions(context.Background(), "", CollectOptions{TolerateGitFailure: true})
	if err != nil {
		t.Fatalf("tolerant Collect failed: %v", err)
	}
	if facts.IsInGitRepo {
		t.Error("IsInGitRepo = true, want degraded no-repo state")
	}
	if gitRoot != "" {
		t.Errorf("gitRoot = %q, want empty in degraded state", gitRoot)
	}
}

// TestCollectUnderSymlinkedGitRoot pins the path comparison behind IsMonorepo.
// git reports the resolved root while os.Getwd reports the path as entered, so
// working under a symlinked directory used to make the two spellings of the same
// directory differ — and every repository there looked like a monorepo. macOS
// hits this constantly (/tmp and /var are symlinks into /private); the symlink
// here reproduces it on any platform.
func TestCollectUnderSymlinkedGitRoot(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	if err := os.Mkdir(realRoot, 0o750); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = realRoot
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to initialize git repo: %v", err)
	}

	link := filepath.Join(base, "link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("cannot symlink here: %v", err)
	}

	t.Chdir(link)
	facts, _, err := Collect(context.Background(), "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if !facts.IsInGitRepo {
		t.Error("IsInGitRepo = false, want true")
	}
	if facts.IsMonorepo {
		t.Error("IsMonorepo = true at the git root reached through a symlink, want false")
	}
}

func TestResolveSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatalf("create target: %v", err)
	}

	t.Run("follows a link", func(t *testing.T) {
		link := filepath.Join(dir, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("cannot symlink here: %v", err)
		}
		// The target itself may sit under a symlinked temp root (macOS), so
		// compare against its resolved form rather than the literal path.
		want, err := filepath.EvalSymlinks(target)
		if err != nil {
			t.Fatalf("resolve target: %v", err)
		}
		if got := resolveSymlinks(link); got != want {
			t.Errorf("resolveSymlinks(link) = %q, want %q", got, want)
		}
	})

	t.Run("unresolvable path is returned as given", func(t *testing.T) {
		// The fallback: comparison then degrades to the textual one it replaced,
		// which is better than dropping the path entirely.
		missing := filepath.Join(dir, "does-not-exist")
		if got := resolveSymlinks(missing); got != missing {
			t.Errorf("resolveSymlinks(missing) = %q, want it unchanged", got)
		}
	})
}

// TestCollectPublishesVersion covers the one build fact a config may want for
// diagnostics. Gating on a minimum core is getMinVersion()'s job, not this.
func TestCollectPublishesVersion(t *testing.T) {
	facts, _, err := Collect(context.Background(), "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if facts.Version != ldflags.Version {
		t.Errorf("Version = %q, want %q", facts.Version, ldflags.Version)
	}
}
