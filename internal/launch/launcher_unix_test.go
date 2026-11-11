//go:build !windows

package launch

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDispatchWithSelectsManagedPathExecutableInRealChild(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)
	store := filepath.Join(cache, "store")
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	ambient := filepath.Join(root, "ambient")
	for dir, value := range map[string]string{managed: "managed", ambient: "ambient"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "choice"), []byte("#!/bin/sh\nprintf '%s' '"+value+"' > \"$RESULT\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ambientResult := filepath.Join(root, "ambient-result")
	if err := os.WriteFile(filepath.Join(ambient, "ambient-only"), []byte("#!/bin/sh\nprintf 'ambient found' > \"$AMBIENT_RESULT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(root, "result")
	record := command{Executable: "/bin/sh", Args: []string{"-c", "choice && ambient-only"}, Env: map[string]string{"PATH": managed, "RESULT": result, "AMBIENT_RESULT": ambientResult}, Identity: strings.Repeat("a", 32)}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	key := commandKey(data)
	launcher := filepath.Join(store, ".app-launchers", key, launcherName)
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(launcher), configName), data, 0o640); err != nil {
		t.Fatal(err)
	}
	code, handled := DispatchWith([]string{launcher}, []string{"PATH=" + ambient}, launcher, func(executable string, args, environ []string) int {
		cmd := exec.Command(executable, args[1:]...)
		cmd.Env = environ
		if err := cmd.Run(); err != nil {
			t.Errorf("run selected child: %v", err)
			return 1
		}
		return 0
	})
	if !handled || code != 0 {
		t.Fatalf("handled=%v code=%d", handled, code)
	}
	selected, err := os.ReadFile(result)
	if err != nil || string(selected) != "managed" {
		t.Fatalf("selected executable=%q err=%v", selected, err)
	}
	preserved, err := os.ReadFile(ambientResult)
	if err != nil || string(preserved) != "ambient found" {
		t.Fatalf("ambient tool unavailable: output=%q err=%v", preserved, err)
	}
}

func TestExecAppCommandReturns126ForMissingExecutable(t *testing.T) {
	if code := execAppCommand(filepath.Join(t.TempDir(), "missing"), []string{"missing"}, os.Environ()); code != 126 {
		t.Fatalf("missing executable exit code = %d, want 126", code)
	}
}

func TestPublishExecutableUsesCapturedBytesAfterSourceReplacement(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source")
	replacementPath := filepath.Join(dir, "replacement")
	target := filepath.Join(dir, "published")
	if err := os.WriteFile(sourcePath, []byte("captured bytes"), 0o751); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	source := &executableSource{path: sourcePath, file: file, info: info}
	if err := os.WriteFile(replacementPath, []byte("replacement bytes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, sourcePath); err != nil {
		t.Fatal(err)
	}
	if err := publishExecutable(source, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "captured bytes" {
		t.Fatalf("published bytes=%q err=%v", data, err)
	}
	result, err := os.Stat(target)
	if err != nil || result.Mode().Perm() != info.Mode().Perm() {
		t.Fatalf("published mode=%v err=%v, want %v", result, err, info.Mode().Perm())
	}
}

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
