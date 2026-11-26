package managedconfig

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnsureAIExclude(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	path := filepath.Join(root, ".git", "info", "exclude")
	original := []byte("# existing\n/custom\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := ensureAIExclude(context.Background(), root)
	if err != nil || !changed {
		t.Fatalf("ensureAIExclude() = %v, %v; want true, nil", changed, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, original) {
		t.Fatalf("exclude contents lost existing bytes: %q", got)
	}
	for _, name := range []string{".agents/skills/dm-example/SKILL.md", ".codex/agents/dm-review.toml", ".codex/agents/.datamitsu-owned-dm-review.toml.marker"} {
		cmd := exec.Command("git", "-C", root, "check-ignore", "--", name)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("expected %s to be ignored: %v: %s", name, err, out)
		}
	}
	cmd := exec.Command("git", "-C", root, "check-ignore", "--", "dm-notes.md")
	if err := cmd.Run(); err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 {
		t.Fatalf("expected root dm-notes.md not to be ignored: %v", err)
	}
	changed, err = ensureAIExclude(context.Background(), root)
	if err != nil || changed {
		t.Fatalf("second ensureAIExclude() = %v, %v; want false, nil", changed, err)
	}
}

func TestEnsureAIExcludeRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	target := filepath.Join(root, "exclude")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".git", "info", "exclude")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if changed, err := ensureAIExclude(context.Background(), root); err == nil || changed {
		t.Fatalf("ensureAIExclude() = %v, %v; want false, error", changed, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "keep\n" {
		t.Fatalf("symlink target changed: %q, %v", got, err)
	}
}

func TestUpdateAIExcludePreservesPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exclude")
	if err := os.WriteFile(path, []byte("/existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := updateAIExclude(path, os.Rename); err != nil || !changed {
		t.Fatalf("update exclude: changed=%v, err=%v", changed, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Fatalf("permissions changed: %o -> %o", before.Mode().Perm(), after.Mode().Perm())
	}
}

func TestUpdateAIExcludePreservesSuccessorLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exclude")
	changed, err := updateAIExclude(path, func(src, dst string) error {
		if err := os.Rename(src, dst); err != nil {
			return err
		}
		next, err := os.OpenFile(src, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		return next.Close()
	})
	if err != nil || !changed {
		t.Fatalf("publish exclude: changed=%v, err=%v", changed, err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("successor lock did not survive publication cleanup: %v", err)
	}
}
