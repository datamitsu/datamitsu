package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/prepare"
)

func TestPrepareFailureDiagnosticsAndRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable is a POSIX shell script")
	}
	payload := []byte("#!/bin/sh\ncase \"$1\" in\n  materialize)\n    printf 'PREPARE_DISTINCTIVE_STDERR\\n' >&2\n    printf 'PREPARE_STDOUT_MUST_BE_HIDDEN\\n'\n    n=0\n    if [ -f \"$COUNT_FILE\" ]; then read n < \"$COUNT_FILE\"; fi\n    n=$((n + 1)); printf '%s\\n' \"$n\" > \"$COUNT_FILE\"\n    if [ -f \"$FAIL_FILE\" ]; then exit 4; fi\n    printf ready > \"$OUT/result\"\n    ;;\nesac\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	project := clitest.NewProject(t)
	count := filepath.Join(project.Dir, "prepare-count")
	failFile := filepath.Join(project.Dir, "fail-prepare")
	if err := os.WriteFile(failFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`globalThis.getBeforeConfigs = () => [];
 const binary = {url:%q,hash:%q,contentType:"binary",binaryPath:"fixture"};
globalThis.getConfig = () => ({version:"fixture",projectTypes:{fixture:{markers:["fixture.marker"]}},apps:{fixture:{binary:{version:"1",binaries:{%q:{%q:{glibc:binary,musl:binary,unknown:binary}}}},prepare:{args:["materialize"],network:false,env:{COUNT_FILE:%q,FAIL_FILE:%q,OUT:"${APP_PREPARED}"}}}},tools:{"lint-only":{name:"lint-only",operations:{lint:{app:"fixture",args:[],scope:"repository"}}}},runtimes:{},managedConfigs:{}});
 globalThis.getMinVersion = () => "0.5.0";`, server.URL, digest.SHA256Of(payload).String(), runtime.GOOS, runtime.GOARCH, count, failFile)
	project.WriteFile("fixture.marker", "")
	cfg := project.WriteFile("provision.config.js", config)
	cache := t.TempDir()
	args := []string{"--no-auto-config", "--config", cfg, "check", "--tools", "lint-only"}
	options := clitest.RunOptions{Dir: project.Dir, CacheDir: cache, Env: []string{"DATAMITSU_OFFLINE="}}

	failed := clitest.Run(t, options, args...)
	if failed.ExitCode != 1 {
		t.Fatalf("prepare failure exit = %d, want 1; stderr:\n%s", failed.ExitCode, failed.Stderr)
	}
	if !strings.Contains(failed.Stderr, "PREPARE_DISTINCTIVE_STDERR") {
		t.Fatalf("prepare stderr was not preserved:\n%s", failed.Stderr)
	}
	if !strings.Contains(failed.Stderr, "exit status 4") {
		t.Fatalf("prepare error omitted child exit status 4:\n%s", failed.Stderr)
	}
	if strings.Contains(failed.Stdout, "PREPARE_STDOUT_MUST_BE_HIDDEN") || strings.Contains(failed.Stderr, "PREPARE_STDOUT_MUST_BE_HIDDEN") {
		t.Fatalf("prepare stdout escaped its capture:\nstdout: %s\nstderr: %s", failed.Stdout, failed.Stderr)
	}
	store := filepath.Join(cache, "store")
	markers, _ := filepath.Glob(filepath.Join(store, ".prepared", "fixture", "*", prepare.MarkerName))
	if len(markers) != 0 {
		t.Fatalf("failed prepare published readiness marker: %v", markers)
	}
	if got := readProvisionRecord(t, count); got != "1\n" {
		t.Fatalf("prepare invocation count after failure = %q, want 1", got)
	}

	for _, mode := range [][]string{{"--log-format=jsonl"}, {"--output=agent"}} {
		modeArgs := append(append([]string(nil), args...), mode...)
		modeResult := clitest.Run(t, options, modeArgs...)
		if modeResult.ExitCode != 1 || !strings.Contains(modeResult.Stderr, "PREPARE_DISTINCTIVE_STDERR") {
			t.Fatalf("prepare stderr missing in mode %v: %+v", mode, modeResult)
		}
		if mode[0] == "--log-format=jsonl" {
			clitest.MustParseJSONL(t, modeResult.Stderr)
			if strings.Contains(modeResult.Stdout, "PREPARE_STDOUT_MUST_BE_HIDDEN") {
				t.Fatalf("JSONL mode leaked prepare stdout: %s", modeResult.Stdout)
			}
		}
		if strings.Contains(modeResult.Stdout, "PREPARE_STDOUT_MUST_BE_HIDDEN") || strings.Contains(modeResult.Stderr, "PREPARE_STDOUT_MUST_BE_HIDDEN") {
			t.Fatalf("prepare stdout escaped in mode %v:\nstdout: %s\nstderr: %s", mode, modeResult.Stdout, modeResult.Stderr)
		}
	}
	if got := readProvisionRecord(t, count); got != "3\n" {
		t.Fatalf("prepare invocations after output modes = %q, want 3", got)
	}
	if err := os.Remove(failFile); err != nil {
		t.Fatal(err)
	}
	retried := clitest.Run(t, options, args...)
	if retried.ExitCode != 0 {
		t.Fatalf("prepare retry failed: %+v", retried)
	}
	if strings.Contains(retried.Stderr, "PREPARE_DISTINCTIVE_STDERR") {
		t.Fatalf("successful prepare stderr was published:\n%s", retried.Stderr)
	}
	if got := readProvisionRecord(t, count); got != "4\n" {
		t.Fatalf("prepare invocation count after retry = %q, want 4", got)
	}
	markers, _ = filepath.Glob(filepath.Join(store, ".prepared", "fixture", "*", prepare.MarkerName))
	if len(markers) != 1 {
		t.Fatalf("successful retry readiness markers = %v, want one", markers)
	}

	ready := clitest.Run(t, options, args...)
	if ready.ExitCode != 0 {
		t.Fatalf("ready rerun failed: %+v", ready)
	}
	if got := readProvisionRecord(t, count); got != "4\n" {
		t.Fatalf("ready rerun executed prepare again; count = %q", got)
	}
	if err := os.WriteFile(failFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	quietOptions := options
	quietOptions.CacheDir = t.TempDir()
	quiet := clitest.Run(t, quietOptions, "--no-auto-config", "--config", cfg, "install", "--quiet", "fixture")
	if quiet.ExitCode != 1 || !strings.Contains(quiet.Stderr, "PREPARE_DISTINCTIVE_STDERR") || !strings.Contains(quiet.Stderr, "exit status 4") {
		t.Fatalf("quiet install suppressed actionable prepare diagnostics: %+v", quiet)
	}
	if strings.Contains(quiet.Stdout, "PREPARE_STDOUT_MUST_BE_HIDDEN") || strings.Contains(quiet.Stderr, "PREPARE_STDOUT_MUST_BE_HIDDEN") {
		t.Fatalf("quiet install leaked prepare stdout:\n%s\nstderr: %s", quiet.Stdout, quiet.Stderr)
	}
	if strings.Contains(quiet.Stderr, "Installing ") || strings.Contains(quiet.Stdout, "Installing ") {
		t.Fatalf("quiet install printed routine progress:\nstdout: %s\nstderr: %s", quiet.Stdout, quiet.Stderr)
	}
}

func readProvisionRecord(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture record: %v", err)
	}
	return string(b)
}

func TestRuntimeAppNamedProgressWaitsForSharedDependencyAndReusesReadyApps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture runtime uses POSIX shell utilities")
	}
	payload := []byte("#!/bin/sh\nif [ \"$1\" = build ]; then\n  echo \"$6\" >> \"$BUILD_LOG\"\n  if [ \"$6\" = example.invalid/shared ]; then\n    : > \"$STARTED_FILE\"\n    while [ ! -e \"$RELEASE_FILE\" ]; do sleep 0.01; done\n  fi\n  /bin/mkdir -p \"${5%/*}\"\n  /bin/cp /bin/true \"$5\"\nfi\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	project := clitest.NewProject(t)
	cache := t.TempDir()
	buildLog := filepath.Join(t.TempDir(), "build.log")
	started := filepath.Join(t.TempDir(), "shared-started")
	release := filepath.Join(t.TempDir(), "release-shared")
	lock := func(module string) string {
		return fmt.Sprintf(`{"mod":%q,"sum":""}`, "module "+module+"\n\ngo 1.22\n")
	}
	app := func(name, dependency string) string {
		dep := ""
		if dependency != "" {
			dep = `,dependsOn:["` + dependency + `"]`
		}
		return fmt.Sprintf(`%q:{go:{packageName:%q,version:"v1",runtime:"fake-go",lockFile:%q}%s,env:{BUILD_LOG:%q,STARTED_FILE:%q,RELEASE_FILE:%q}}`,
			name, "example.invalid/"+name, lock("example.invalid/"+name), dep, buildLog, started, release)
	}
	config := fmt.Sprintf(`globalThis.getBeforeConfigs = () => [];
const binary = {url:%q,hash:%q,contentType:"binary",binaryPath:"go"};
globalThis.getConfig = () => ({version:"fixture",runtimes:{"fake-go":{kind:"go",mode:"managed",go:{goVersion:"1.22.0"},managed:{binaries:{%q:{%q:{glibc:binary,musl:binary,unknown:binary}}}}}},apps:{%s,%s,%s},tools:{},managedConfigs:{}});
globalThis.getMinVersion = () => "0.5.0";`, server.URL, digest.SHA256Of(payload).String(), runtime.GOOS, runtime.GOARCH,
		app("shared", ""), app("alpha", "shared"), app("beta", "shared"))
	cfg := project.WriteFile("runtime-progress.config.js", config)
	opts := clitest.RunOptions{Dir: project.Dir, CacheDir: cache, Env: []string{"DATAMITSU_OFFLINE="}}
	args := []string{"--no-auto-config", "--config", cfg, "install", "--no-verify"}
	first := clitest.Start(t, opts, append(append([]string(nil), args...), "alpha")...)
	var second *exec.Cmd
	var secondStderr *os.File
	var secondWaited bool
	var secondStdout bytes.Buffer
	var secondCancel context.CancelFunc
	defer func() {
		_ = os.WriteFile(release, nil, 0o600)
		if first != nil {
			_ = first.Wait()
		}
		if second != nil && !secondWaited {
			_ = second.Wait()
		}
		if secondCancel != nil {
			secondCancel()
		}
		if secondStderr != nil {
			_ = secondStderr.Close()
		}
	}()
	waitForProvisionFile(t, started)
	secondStderr, err := os.CreateTemp(t.TempDir(), "waiter-stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	secondCtx, cancel := context.WithTimeout(context.Background(), clitest.DefaultTimeout)
	secondCancel = cancel
	second = exec.CommandContext(secondCtx, clitest.BuildOnce(t), append(append([]string(nil), args...), "beta")...)
	second.Dir = opts.Dir
	second.Env = append(clitest.BaseEnv(cache), opts.Env...)
	second.Stdout = &secondStdout
	second.Stderr = secondStderr
	if err := second.Start(); err != nil {
		t.Fatalf("start second runtime app install: %v", err)
	}
	waitForProvisionText(t, secondStderr.Name(), "Waiting for installation: app shared")
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	firstResult := first.Wait()
	first = nil
	secondErr := second.Wait()
	secondWaited = true
	if err := secondStderr.Close(); err != nil {
		t.Fatal(err)
	}
	secondStderrBytes, err := os.ReadFile(secondStderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.ExitCode != 0 {
		t.Fatalf("first runtime app install failed: %+v", firstResult)
	}
	if got := clitest.ExitCodeOf(secondErr); got != 0 {
		t.Fatalf("second runtime app install exited %d: stdout=%s stderr=%s", got, secondStdout.String(), secondStderrBytes)
	}
	if !strings.Contains(string(secondStderrBytes), "Waiting for installation: app shared") {
		t.Fatalf("waiter did not report the shared app by name:\n%s", secondStderrBytes)
	}
	want := map[string]int{"example.invalid/shared": 1, "example.invalid/alpha": 1, "example.invalid/beta": 1}
	checkBuildLog(t, buildLog, want)
	ready := clitest.Run(t, opts, append(append([]string(nil), args...), "alpha")...)
	if ready.ExitCode != 0 {
		t.Fatalf("ready runtime app rerun failed: %+v", ready)
	}
	checkBuildLog(t, buildLog, want)
}

func waitForProvisionFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for fixture marker %s", path)
}

func waitForProvisionText(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read waiter stderr: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("timed out waiting for %q in waiter stderr:\n%s", want, data)
}

func checkBuildLog(t *testing.T, path string, want map[string]int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake Go build log: %v", err)
	}
	got := make(map[string]int)
	for pkg := range strings.FieldsSeq(string(data)) {
		got[pkg]++
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("fake Go builds = %v, want %v", got, want)
	}
}

func TestPNPMRuntimeAppEmitsNamedPackageProgressAndReusesReadyInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture pnpm runtime uses POSIX shell utilities")
	}
	payload := []byte("#!/bin/sh\necho install >> \"$INSTALL_LOG\"\n/bin/mkdir -p node_modules/fixture-pkg\nprintf '{\"name\":\"fixture-pkg\"}\\n' > node_modules/fixture-pkg/package.json\nprintf '#!/bin/sh\\nexit 0\\n' > node_modules/fixture-pkg/entry.js\nchmod +x node_modules/fixture-pkg/entry.js\nprintf '{\"name\":\"pnpm:progress\",\"status\":\"resolved\",\"packageId\":\"a@1\"}\\n' >&2\n/bin/sleep 1.1\nprintf '{\"name\":\"pnpm:progress\",\"status\":\"fetched\",\"packageId\":\"a@1\"}\\n' >&2\n/bin/sleep 1.1\nprintf '{\"name\":\"pnpm:progress\",\"status\":\"resolved\",\"packageId\":\"b@1\"}\\n' >&2\n")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	project := clitest.NewProject(t)
	installLog := filepath.Join(t.TempDir(), "pnpm-installs")
	lock := "lockfileVersion: '9.0'\n"
	config := fmt.Sprintf(`globalThis.getBeforeConfigs = () => [];
const pnpmBinary = {url:%q,hash:%q,contentType:"binary",binaryPath:"pnpm"};
globalThis.getConfig = () => ({version:"fixture",runtimes:{node:{kind:"node",mode:"system",system:{command:"/bin/true",systemVersion:"26.0.0"},node:{nodeVersion:"26.0.0",pnpmRuntime:"pnpm"}},pnpm:{kind:"pnpm",mode:"managed",pnpm:{pnpmVersion:"12.0.0"},managed:{binaries:{%q:{%q:{glibc:pnpmBinary,musl:pnpmBinary,unknown:pnpmBinary}}}}}},apps:{"readable-tool":{node:{packageName:"fixture-pkg",version:"1.0.0",runtime:"node",binPath:"node_modules/fixture-pkg/entry.js",lockFile:%q},env:{INSTALL_LOG:%q}}},tools:{},managedConfigs:{}});
globalThis.getMinVersion = () => "0.5.0";`, server.URL, digest.SHA256Of(payload).String(), runtime.GOOS, runtime.GOARCH, lock, installLog)
	cfg := project.WriteFile("pnpm-progress.config.js", config)
	opts := clitest.RunOptions{Dir: project.Dir, CacheDir: t.TempDir(), Env: []string{"DATAMITSU_OFFLINE="}}
	args := []string{"--no-auto-config", "--config", cfg, "install", "--no-verify", "readable-tool"}
	first := clitest.Run(t, opts, args...)
	if first.ExitCode != 0 {
		t.Fatalf("pnpm runtime app install failed: %+v", first)
	}
	progressLines := 0
	for line := range strings.SplitSeq(first.Stderr, "\n") {
		if strings.HasPrefix(line, "Installing app readable-tool: ") {
			progressLines++
		}
	}
	if progressLines != 2 || strings.Count(first.Stderr, "Installing app readable-tool: install (1 packages)") != 1 {
		t.Fatalf("pnpm progress lines = %d; want exactly two including the 1-package counter:\n%s", progressLines, first.Stderr)
	}
	if strings.Contains(first.Stderr, "Installing app readable-tool: hash") || strings.Contains(first.Stderr, "hash install") {
		t.Fatalf("pnpm exposed an internal hash as a progress label:\n%s", first.Stderr)
	}
	if got := strings.TrimSpace(readProvisionRecord(t, installLog)); got != "install" {
		t.Fatalf("pnpm install invocations = %q, want one", got)
	}
	if requests.Load() != 1 {
		t.Fatalf("pnpm runtime download requests = %d, want one", requests.Load())
	}
	ready := clitest.Run(t, opts, args...)
	if ready.ExitCode != 0 || ready.Stderr != "" {
		t.Fatalf("ready pnpm app rerun should be quiet: %+v", ready)
	}
	if got := strings.TrimSpace(readProvisionRecord(t, installLog)); got != "install" {
		t.Fatalf("ready rerun repeated pnpm installation: %q", got)
	}
	if requests.Load() != 1 {
		t.Fatalf("ready rerun re-downloaded pnpm runtime: %d requests", requests.Load())
	}
}
