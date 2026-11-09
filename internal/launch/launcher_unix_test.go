//go:build !windows

package launch

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLauncherRecordIsReadableOnlyByOwnerAndStoreGroup(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	path, err := Ensure("/bin/echo", nil, map[string]string{"TOKEN": "synthetic-secret"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(path), configName))
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode().Perm()
	if mode&0o440 != 0o440 || mode&0o027 != 0 {
		t.Fatalf("record permissions %o deny the store group or expose private environment outside it", mode)
	}
}

func TestLauncherRetainsCapturedBuildAfterReplacement(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	target := filepath.Join(root, "target")
	buildLauncherFixture(t, first, "first-build")
	buildLauncherFixture(t, second, "second-build")
	if err := os.Link(first, target); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	create := exec.CommandContext(ctx, first, "pause", target)
	create.Env = append(os.Environ(), "DATAMITSU_CACHE_DIR="+filepath.Join(root, "cache"))
	create.Stderr = os.Stderr
	stdout, err := create.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := create.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := create.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = create.Wait()
	})
	reader := bufio.NewReader(stdout)
	computed, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("wait for read-only resolution: %v", err)
	}
	if err := os.Rename(second, first); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(stdin, "continue\n"); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	published, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := create.Wait(); err != nil {
		t.Fatalf("publish after replacement: %v", err)
	}
	path := strings.TrimSpace(computed)
	if strings.TrimSpace(string(published)) != "dev\n"+path {
		t.Fatalf("publication changed the captured identity: %q", published)
	}
	run := exec.CommandContext(ctx, path, "payload")
	run.Env = create.Env
	output, err := run.CombinedOutput()
	if err != nil || string(output) != "first-build\nargument with spaces|bound|payload\n" {
		t.Fatalf("published executable differs from captured build: output=%q err=%v", output, err)
	}
}
