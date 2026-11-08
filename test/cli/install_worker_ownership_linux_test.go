//go:build linux

package cli_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/digest"
)

func TestInstallWorkerOwnsInstallerDescendantsAfterOuterCLIExit(t *testing.T) {
	probe := compileInstallerProbe(t)
	probeBytes, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	pin := digest.SHA256Of(probeBytes).String()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(probeBytes)
	}))
	defer server.Close()

	p := clitest.NewProject(t)
	cacheDir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "descendant.pid")
	targetFile := filepath.Join(t.TempDir(), "target.path")
	releaseFile := filepath.Join(t.TempDir(), "release")
	onceFile := filepath.Join(t.TempDir(), "descendant.started")
	invocationFile := filepath.Join(t.TempDir(), "build.invocations")
	storeSentinel := filepath.Join(cacheDir, "store", "ownership-sentinel")
	if err := os.MkdirAll(filepath.Dir(storeSentinel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storeSentinel, []byte("held"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := p.WriteFile("worker.config.js", fmt.Sprintf(`globalThis.getBeforeConfigs = () => [];
const binary = { url: %q, hash: %q, contentType: "binary", binaryPath: "fake-go" };
globalThis.getConfig = () => ({
  version: "1.0.0",
  runtimes: { "fake-go": {
    kind: "go", mode: "managed", go: { goVersion: "1.22.0" },
    managed: { binaries: { %q: { %q: {
      glibc: binary, musl: binary, unknown: binary
    } } } }
  } },
  apps: { probe: {
    go: { packageName: "example.invalid/probe", version: "v1", runtime: "fake-go",
      lockFile: %q }
  } },
  managedConfigs: {}, tools: {}
});
globalThis.getMinVersion = () => "0.0.0";
`, server.URL, pin, runtime.GOOS, runtime.GOARCH,
		`{"mod":"module example.invalid/probe\n\ngo 1.22\n","sum":""}`))
	env := []string{
		"DATAMITSU_OFFLINE=",
		"DATAMITSU_PROBE_PID=" + pidFile,
		"DATAMITSU_PROBE_TARGET=" + targetFile,
		"DATAMITSU_PROBE_RELEASE=" + releaseFile,
		"DATAMITSU_PROBE_ONCE=" + onceFile,
		"DATAMITSU_PROBE_INVOCATIONS=" + invocationFile,
	}
	args := []string{"--no-auto-config", "--config", cfg, "install", "probe"}

	first := clitest.Start(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir, Env: env}, args...)
	pid := waitForPID(t, pidFile, first)
	t.Cleanup(func() {
		if processAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	targetBytes, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read installer target path: %v", err)
	}
	lateWrite := strings.TrimSpace(string(targetBytes)) + ".late"

	replacement := clitest.Start(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir, Env: env}, args...)
	clearProcess := clitest.Start(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir, Env: env}, "store", "clear")
	type completion struct {
		result          clitest.Result
		descendantAlive bool
	}
	replacementDone := make(chan completion, 1)
	clearDone := make(chan completion, 1)
	go func() {
		result := replacement.Wait()
		replacementDone <- completion{result: result, descendantAlive: processAlive(pid)}
	}()
	go func() {
		result := clearProcess.Wait()
		clearDone <- completion{result: result, descendantAlive: processAlive(pid)}
	}()

	ownerDeadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(ownerDeadline) {
		if !processAlive(pid) {
			t.Fatalf("installer descendant %d exited while the outer CLI still owned the install", pid)
		}
		if _, err := os.Stat(storeSentinel); err != nil {
			t.Fatalf("store clear removed the sentinel while installer descendant %d was alive: %v", pid, err)
		}
		if count := lineCount(t, invocationFile); count != 1 {
			t.Fatalf("replacement installer entered the Go build while descendant %d was alive: build count=%d", pid, count)
		}
		select {
		case got := <-replacementDone:
			t.Fatalf("replacement install completed while outer CLI and descendant were alive: exit=%d stderr=%s", got.result.ExitCode, got.result.Stderr)
		case got := <-clearDone:
			t.Fatalf("store clear completed while outer CLI and descendant were alive: exit=%d stderr=%s", got.result.ExitCode, got.result.Stderr)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := first.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan clitest.Result, 1)
	go func() { firstDone <- first.Wait() }()
	select {
	case <-firstDone:
	case <-time.After(10 * time.Second):
		t.Fatal("outer install CLI did not exit after SIGKILL")
	}
	reapDeadline := time.Now().Add(10 * time.Second)
	for processAlive(pid) {
		if _, err := os.Stat(storeSentinel); err != nil {
			t.Fatalf("store clear removed the sentinel before installer descendant %d was reaped: %v", pid, err)
		}
		if count := lineCount(t, invocationFile); count != 1 {
			t.Fatalf("replacement installer entered the Go build before descendant %d was reaped: build count=%d", pid, count)
		}
		if time.Now().After(reapDeadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("installer descendant %d was not reaped before timeout", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}

	await := func(name string, done <-chan completion) clitest.Result {
		t.Helper()
		select {
		case got := <-done:
			if got.descendantAlive {
				t.Errorf("%s completed before installer descendant %d was reaped", name, pid)
			}
			if got.result.ExitCode != 0 {
				t.Errorf("%s exit = %d\nstdout:\n%s\nstderr:\n%s", name, got.result.ExitCode, got.result.Stdout, got.result.Stderr)
			}
			return got.result
		case <-time.After(20 * time.Second):
			t.Fatalf("%s did not proceed after installer descendant was reaped", name)
			return clitest.Result{}
		}
	}
	await("replacement install", replacementDone)
	await("store clear", clearDone)
	if _, err := os.Stat(storeSentinel); !os.IsNotExist(err) {
		t.Errorf("store clear did not remove the protected sentinel: stat err=%v", err)
	}
	if count := lineCount(t, invocationFile); count != 2 {
		t.Errorf("replacement install build count = %d, want 2 total", count)
	}

	if err := os.WriteFile(releaseFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lateWrite); !os.IsNotExist(err) {
		t.Fatalf("installer descendant wrote after the replacement install/clear completed: stat err=%v", err)
	}
}

func compileInstallerProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "probe.go")
	binary := filepath.Join(dir, "fake-go")
	const program = `package main
import (
  "os"
  "os/exec"
  "strconv"
  "time"
)
func main() {
  if len(os.Args) > 1 && os.Args[1] == "--descendant" {
    target := os.Args[2]
    _ = os.WriteFile(os.Getenv("DATAMITSU_PROBE_PID"), []byte(strconv.Itoa(os.Getpid())), 0644)
    for {
      if _, err := os.Stat(os.Getenv("DATAMITSU_PROBE_RELEASE")); err == nil {
        _ = os.WriteFile(target+".late", []byte("late"), 0644)
        return
      }
      time.Sleep(15 * time.Millisecond)
    }
  }
  if len(os.Args) < 2 || os.Args[1] != "build" { return }
  output := ""
  for i := 1; i+1 < len(os.Args); i++ { if os.Args[i] == "-o" { output = os.Args[i+1] } }
  if output == "" { os.Exit(2) }
  invocation, err := os.OpenFile(os.Getenv("DATAMITSU_PROBE_INVOCATIONS"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
  if err != nil { os.Exit(7) }
  _, _ = invocation.WriteString("build\n")
  _ = invocation.Close()
  _ = os.WriteFile(os.Getenv("DATAMITSU_PROBE_TARGET"), []byte(output), 0644)
  once, err := os.OpenFile(os.Getenv("DATAMITSU_PROBE_ONCE"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
  if err == nil {
    _ = once.Close()
    child := exec.Command(os.Args[0], "--descendant", output)
    child.Env = os.Environ()
    if err := child.Start(); err != nil { os.Exit(3) }
    if err := child.Wait(); err != nil { os.Exit(4) }
  }
  data, err := os.ReadFile(os.Args[0]); if err != nil { os.Exit(5) }
  if err := os.WriteFile(output, data, 0755); err != nil { os.Exit(6) }
}

`
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", binary, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile installer probe: %v\n%s", err, out)
	}
	return binary
}

func waitForPID(t *testing.T, path string, process *clitest.Process) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil && pid > 1 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	signalErr := process.Signal(syscall.SIGKILL)
	result := process.Wait()
	t.Fatalf("installer descendant did not publish its pid in %s; killed CLI (signal error=%v, exit=%d)\nstdout:\n%s\nstderr:\n%s", path, signalErr, result.ExitCode, result.Stdout, result.Stderr)
	return 0
}

func processAlive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	end := strings.LastIndex(string(data), ") ")
	return end < 0 || !strings.HasPrefix(string(data)[end+2:], "Z")
}

func lineCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Count(string(data), "\n")
}
