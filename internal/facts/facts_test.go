package facts

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCollectAllEnv(t *testing.T) {
	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				_ = os.Setenv(parts[0], parts[1])
			}
		}
	}()

	os.Clearenv()
	_ = os.Setenv("TEST_VAR1", "value1")
	_ = os.Setenv("TEST_VAR2", "value2")
	_ = os.Setenv("OTHER_VAR", "other")

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
	originalEnv := os.Getenv("DATAMITSU_BINARY_COMMAND")
	defer func() {
		if originalEnv != "" {
			_ = os.Setenv("DATAMITSU_BINARY_COMMAND", originalEnv)
		} else {
			_ = os.Unsetenv("DATAMITSU_BINARY_COMMAND")
		}
	}()

	ctx := context.Background()
	envOverride := "/env/binary/path"
	_ = os.Setenv("DATAMITSU_BINARY_COMMAND", envOverride)

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

	originalCwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalCwd) }()

	_ = os.Chdir(tmpDir)

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
	_ = os.MkdirAll(subdir, 0755)

	originalCwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalCwd) }()

	_ = os.Chdir(subdir)

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

	originalCwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalCwd) }()

	_ = os.Chdir(tmpDir)

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

	originalCwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalCwd) }()

	_ = os.Chdir(tmpDir)

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

	originalCwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalCwd) }()

	_ = os.Chdir(tmpDir)

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
