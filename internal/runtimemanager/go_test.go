package runtimemanager

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

// makeTestGoRuntimes returns a runtime map with a single managed Go runtime
// usable by Go app install/command-info tests on the host platform.
func makeTestGoRuntimes() config.MapOfRuntimes {
	return config.MapOfRuntimes{
		"go": {
			Kind: config.RuntimeKindGo,
			Mode: config.RuntimeModeManaged,
			Go:   &config.RuntimeConfigGo{GoVersion: "1.22.0"},
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					syslist.OsTypeDarwin: {
						syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/go-darwin-amd64.tar.gz",
							Hash:        "go123",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
						syslist.ArchTypeArm64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/go-darwin-arm64.tar.gz",
							Hash:        "go123arm",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
					},
					syslist.OsTypeLinux: {
						syslist.ArchTypeAmd64: {testLibc: binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/go-linux-amd64.tar.gz",
							Hash:        "go456",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
					},
				},
			},
		},
	}
}

func TestParseGoLockFile_Valid(t *testing.T) {
	goMod := "module datamitsu-govulncheck\n\ngo 1.22\n\nrequire golang.org/x/vuln v1.1.4\n"
	goSum := "golang.org/x/vuln v1.1.4 h1:abc=\ngolang.org/x/vuln v1.1.4/go.mod h1:def=\n"

	jsonStr, err := BuildGoLockFileJSON(goMod, goSum)
	if err != nil {
		t.Fatalf("BuildGoLockFileJSON() error = %v", err)
	}

	gotMod, gotSum, err := parseGoLockFile(jsonStr)
	if err != nil {
		t.Fatalf("parseGoLockFile() error = %v", err)
	}

	if gotMod != goMod {
		t.Errorf("go.mod mismatch: got %q, want %q", gotMod, goMod)
	}
	if gotSum != goSum {
		t.Errorf("go.sum mismatch: got %q, want %q", gotSum, goSum)
	}
}

func TestParseGoLockFile_InvalidJSON(t *testing.T) {
	_, _, err := parseGoLockFile("this is not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseGoLockFile_MalformedJSON(t *testing.T) {
	_, _, err := parseGoLockFile(`{"mod": "x", "sum": `)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestParseGoLockFile_MissingMod(t *testing.T) {
	_, _, err := parseGoLockFile(`{"sum": "golang.org/x/vuln v1.1.4 h1:abc=\n"}`)
	if err == nil {
		t.Error("expected error when mod field is missing")
	}
	if err != nil && !strings.Contains(err.Error(), "mod") {
		t.Errorf("expected error to mention mod field, got: %v", err)
	}
}

func TestParseGoLockFile_MissingSum(t *testing.T) {
	_, _, err := parseGoLockFile(`{"mod": "module datamitsu-x\n\ngo 1.22\n"}`)
	if err == nil {
		t.Error("expected error when sum field is missing")
	}
	if err != nil && !strings.Contains(err.Error(), "sum") {
		t.Errorf("expected error to mention sum field, got: %v", err)
	}
}

func TestParseGoLockFile_EmptyMod(t *testing.T) {
	_, _, err := parseGoLockFile(`{"mod": "", "sum": "x"}`)
	if err == nil {
		t.Error("expected error when mod field is empty")
	}
}

func TestParseGoLockFile_EmptySum(t *testing.T) {
	_, _, err := parseGoLockFile(`{"mod": "module x\ngo 1.22\n", "sum": ""}`)
	if err == nil {
		t.Error("expected error when sum field is empty")
	}
}

func TestBuildGoLockFileJSON_ValidJSON(t *testing.T) {
	goMod := "module datamitsu-x\n\ngo 1.22\n"
	goSum := "golang.org/x/vuln v1.1.4 h1:abc=\n"

	jsonStr, err := BuildGoLockFileJSON(goMod, goSum)
	if err != nil {
		t.Fatalf("BuildGoLockFileJSON() error = %v", err)
	}

	if !strings.Contains(jsonStr, `"mod"`) {
		t.Errorf("expected JSON to contain mod field, got %q", jsonStr)
	}
	if !strings.Contains(jsonStr, `"sum"`) {
		t.Errorf("expected JSON to contain sum field, got %q", jsonStr)
	}

	// Round-trip through parse to confirm well-formed JSON.
	gotMod, gotSum, err := parseGoLockFile(jsonStr)
	if err != nil {
		t.Fatalf("parseGoLockFile() error = %v", err)
	}
	if gotMod != goMod || gotSum != goSum {
		t.Errorf("round-trip mismatch: got mod=%q sum=%q, want mod=%q sum=%q", gotMod, gotSum, goMod, goSum)
	}
}

func TestBuildGoLockFileJSON_PreservesSpecialChars(t *testing.T) {
	// go.mod / go.sum content includes newlines, slashes, quotes-free but
	// JSON escaping must still round-trip exactly.
	goMod := "module example.com/x\n\ngo 1.22\n\nrequire (\n\tgolang.org/x/tools v0.1.0\n)\n"
	goSum := "golang.org/x/tools v0.1.0 h1:abc/def+ghi=\ngolang.org/x/tools v0.1.0/go.mod h1:xyz=\n"

	jsonStr, err := BuildGoLockFileJSON(goMod, goSum)
	if err != nil {
		t.Fatalf("BuildGoLockFileJSON() error = %v", err)
	}

	gotMod, gotSum, err := parseGoLockFile(jsonStr)
	if err != nil {
		t.Fatalf("parseGoLockFile() error = %v", err)
	}
	if gotMod != goMod {
		t.Errorf("go.mod not preserved: got %q, want %q", gotMod, goMod)
	}
	if gotSum != goSum {
		t.Errorf("go.sum not preserved: got %q, want %q", gotSum, goSum)
	}
}

func TestGoLockFile_BuildCompressDecompressParseRoundTrip(t *testing.T) {
	goMod := "module datamitsu-govulncheck\n\ngo 1.22\n\nrequire golang.org/x/vuln v1.1.4\n"
	goSum := strings.Repeat("golang.org/x/vuln v1.1.4 h1:AAAA=\ngolang.org/x/vuln v1.1.4/go.mod h1:BBBB=\n", 50)

	jsonStr, err := BuildGoLockFileJSON(goMod, goSum)
	if err != nil {
		t.Fatalf("BuildGoLockFileJSON() error = %v", err)
	}

	compressed, err := CompressLockFile(jsonStr)
	if err != nil {
		t.Fatalf("CompressLockFile() error = %v", err)
	}
	if !strings.HasPrefix(compressed, brotliPrefix) {
		t.Errorf("compressed should start with %q prefix", brotliPrefix)
	}

	decompressed, err := DecompressLockFile(compressed)
	if err != nil {
		t.Fatalf("DecompressLockFile() error = %v", err)
	}

	gotMod, gotSum, err := parseGoLockFile(decompressed)
	if err != nil {
		t.Fatalf("parseGoLockFile() error = %v", err)
	}

	if gotMod != goMod {
		t.Errorf("round-trip go.mod mismatch: got %q, want %q", gotMod, goMod)
	}
	if gotSum != goSum {
		t.Errorf("round-trip go.sum mismatch: got %q, want %q", gotSum, goSum)
	}
}

func TestGetGoEnvVars(t *testing.T) {
	appEnvPath := "/cache/.apps/go/govulncheck/abc123"
	vars := getGoEnvVars(appEnvPath)

	expected := map[string]string{
		"GOPATH":      filepath.Join(appEnvPath, "gopath"),
		"GOMODCACHE":  filepath.Join(appEnvPath, "gomodcache"),
		"GOBIN":       filepath.Join(appEnvPath, "bin"),
		"GOTOOLCHAIN": "local",
		"GOSUMDB":     "sum.golang.org",
		"GOPRIVATE":   "",
		"GONOPROXY":   "",
		"GONOSUMDB":   "",
		"GOINSECURE":  "",
		"GOFLAGS":     "-mod=readonly -trimpath",
	}

	for key, want := range expected {
		got, ok := vars[key]
		if !ok {
			t.Errorf("missing key %q", key)
			continue
		}
		if got != want {
			t.Errorf("vars[%q] = %q, want %q", key, got, want)
		}
	}

	if len(vars) != len(expected) {
		t.Errorf("vars has %d entries, want %d", len(vars), len(expected))
	}

	// -mod=readonly is the supply chain guarantee: it must be present so a
	// go.sum mismatch fails the build instead of silently rewriting go.sum.
	if !strings.Contains(vars["GOFLAGS"], "-mod=readonly") {
		t.Errorf("GOFLAGS must contain -mod=readonly, got %q", vars["GOFLAGS"])
	}
	// GOTOOLCHAIN=local prevents auto-downloading an unverified toolchain that
	// would bypass the SHA-256-pinned managed SDK.
	if vars["GOTOOLCHAIN"] != "local" {
		t.Errorf("GOTOOLCHAIN must be forced to local, got %q", vars["GOTOOLCHAIN"])
	}
	// GOSUMDB must stay on so checksum-DB verification cannot be disabled via an
	// inherited GOSUMDB=off.
	if vars["GOSUMDB"] != "sum.golang.org" {
		t.Errorf("GOSUMDB must be forced on, got %q", vars["GOSUMDB"])
	}
	// GONOSUMDB must be cleared: it can disable checksum-DB verification on its
	// own, independent of GOSUMDB, so an inherited GONOSUMDB=* must not survive.
	if vars["GONOSUMDB"] != "" {
		t.Errorf("GONOSUMDB must be cleared, got %q", vars["GONOSUMDB"])
	}
}

func TestGoBinaryName(t *testing.T) {
	tests := []struct {
		name        string
		packageName string
		wantUnix    string
	}{
		{"nested command path", "golang.org/x/vuln/cmd/govulncheck", "govulncheck"},
		{"simple module path", "honnef.co/go/tools/cmd/staticcheck", "staticcheck"},
		{"single element", "mytool", "mytool"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := goBinaryName(tt.packageName)
			want := tt.wantUnix
			if runtime.GOOS == "windows" {
				want += ".exe"
			}
			if got != want {
				t.Errorf("goBinaryName(%q) = %q, want %q", tt.packageName, got, want)
			}
		})
	}
}

func TestGetGoBinaryPath(t *testing.T) {
	appEnvPath := "/cache/.apps/go/govulncheck/abc123"

	t.Run("nested package path uses last element", func(t *testing.T) {
		path := getGoBinaryPath(appEnvPath, "golang.org/x/vuln/cmd/govulncheck")
		binName := "govulncheck"
		if runtime.GOOS == "windows" {
			binName += ".exe"
		}
		want := filepath.Join(appEnvPath, "bin", binName)
		if path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
	})

	t.Run("different package", func(t *testing.T) {
		path := getGoBinaryPath(appEnvPath, "honnef.co/go/tools/cmd/staticcheck")
		binName := "staticcheck"
		if runtime.GOOS == "windows" {
			binName += ".exe"
		}
		want := filepath.Join(appEnvPath, "bin", binName)
		if path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
	})
}

func TestBuildGoBuildArgs(t *testing.T) {
	args := buildGoBuildArgs("golang.org/x/vuln/cmd/govulncheck", "/cache/bin/govulncheck")
	want := []string{"build", "-trimpath", "-mod=readonly", "-o", "/cache/bin/govulncheck", "golang.org/x/vuln/cmd/govulncheck"}
	if !equalStringSlices(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}

	// -mod=readonly must always be present so a stale/tampered go.sum fails
	// the build rather than being silently rewritten.
	found := false
	for _, a := range args {
		if a == "-mod=readonly" {
			found = true
			break
		}
	}
	if !found {
		t.Error("-mod=readonly must be present in go build args (supply chain hardening)")
	}
}

func TestInstallGoApp_AlreadyInstalled(t *testing.T) {
	rm := New(makeTestGoRuntimes())

	appConfig := &binmanager.AppConfigGo{
		PackageName: "golang.org/x/vuln/cmd/govulncheck",
		Version:     "v1.1.4",
		Runtime:     "go",
		LockFile:    "irrelevant-when-binary-exists",
	}

	appEnvPath, err := rm.GetGoAppPath("govulncheck", appConfig, nil, nil, "go")
	if err != nil {
		t.Fatalf("GetGoAppPath() error = %v", err)
	}

	binPath := getGoBinaryPath(appEnvPath, appConfig.PackageName)
	if err := os.MkdirAll(filepath.Dir(binPath), 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho ok"), 0755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}
	defer func() { _ = os.RemoveAll(appEnvPath) }()

	if err := rm.InstallGoApp("govulncheck", appConfig, nil, nil, nil); err != nil {
		t.Errorf("InstallGoApp() error = %v, expected nil for already installed app", err)
	}
}

func TestInstallGoApp_InvalidRuntime(t *testing.T) {
	rm := New(makeTestGoRuntimes())

	appConfig := &binmanager.AppConfigGo{
		PackageName: "golang.org/x/vuln/cmd/govulncheck",
		Version:     "v1.1.4",
		Runtime:     "nonexistent",
		LockFile:    "x",
	}

	if err := rm.InstallGoApp("govulncheck", appConfig, nil, nil, nil); err == nil {
		t.Error("expected error for nonexistent runtime, got nil")
	}
}

func TestInstallGoApp_MissingLockFileIsRejected(t *testing.T) {
	rm := New(makeTestGoRuntimes())

	appConfig := &binmanager.AppConfigGo{
		PackageName: "golang.org/x/vuln/cmd/govulncheck",
		Version:     "v1.1.4",
		Runtime:     "go",
		// no LockFile: must be rejected before any download/build
	}

	err := rm.InstallGoApp("govulncheck", appConfig, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error when lockFile is missing, got nil")
	}
	if !strings.Contains(err.Error(), "lockFile") {
		t.Errorf("expected error to mention lockFile, got: %v", err)
	}
}

func TestInstallGoApp_RetriesAfterError(t *testing.T) {
	rm := New(makeTestGoRuntimes())

	// singleflight must not memoize a completed call: once an in-flight Do
	// returns (success or error), the next Do for the same key must re-run the
	// function. Counting executions proves the failed install is actually
	// retried rather than returning a stale cached result — two identical
	// errors alone would not distinguish a re-run from a cached error.
	var calls int32
	sentinel := errors.New("install failed")
	run := func() error {
		_, err, _ := rm.appInstall.Do("go/retry", func() (any, error) {
			atomic.AddInt32(&calls, 1)
			return nil, sentinel
		})
		return err
	}

	if err := run(); !errors.Is(err, sentinel) {
		t.Fatalf("first call: expected sentinel error, got %v", err)
	}
	if err := run(); !errors.Is(err, sentinel) {
		t.Fatalf("retry: expected sentinel error, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("expected function to run twice (retry after error), ran %d time(s)", got)
	}
}

// TestInstallGoApp_ConcurrentSameKeyNoRace launches many goroutines installing
// the same failing app simultaneously. Every goroutine must observe an error
// and the run must be free of data races (verified under -race). The prior
// sync.Once + CompareAndDelete pattern could orphan an in-flight reader when a
// deletion overlapped a concurrent LoadOrStore; singleflight has no deletion
// step, so that race cannot occur.
func TestInstallGoApp_ConcurrentSameKeyNoRace(t *testing.T) {
	rm := New(makeTestGoRuntimes())

	appConfig := &binmanager.AppConfigGo{
		PackageName: "golang.org/x/vuln/cmd/govulncheck",
		Version:     "v1.1.4",
		Runtime:     "nonexistent", // fails fast at ResolveRuntime
		LockFile:    "x",
	}

	const n = 32
	var wg sync.WaitGroup
	errsObserved := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errsObserved[idx] = rm.InstallGoApp("govulncheck", appConfig, nil, nil, nil)
		}(i)
	}
	wg.Wait()

	for i, err := range errsObserved {
		if err == nil {
			t.Errorf("goroutine %d: expected error, got nil", i)
		}
	}
}

// TestSingleflightDeduplicatesConcurrentInstalls verifies the dedup contract the
// install sites rely on: while one call for a key is in flight, concurrent calls
// for the same key do not run the function again and all share the same result.
func TestSingleflightDeduplicatesConcurrentInstalls(t *testing.T) {
	rm := New(makeTestGoRuntimes())

	var calls int32
	start := make(chan struct{})
	sentinel := errors.New("install failed")

	const n = 20
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, err, _ := rm.appInstall.Do("go/concurrent", func() (any, error) {
				atomic.AddInt32(&calls, 1)
				// Hold the call in flight so the other goroutines coalesce onto it.
				time.Sleep(50 * time.Millisecond)
				return nil, sentinel
			})
			results[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected exactly 1 execution for the shared key, got %d", got)
	}
	for i, err := range results {
		if !errors.Is(err, sentinel) {
			t.Errorf("goroutine %d: expected shared sentinel error, got %v", i, err)
		}
	}
}

func TestGetGoGenEnvVars(t *testing.T) {
	workDir := "/cache/.apps/go/govulncheck/abc123"
	vars := getGoGenEnvVars(workDir)

	expected := map[string]string{
		"GOPATH":      filepath.Join(workDir, "gopath"),
		"GOMODCACHE":  filepath.Join(workDir, "gomodcache"),
		"GOBIN":       filepath.Join(workDir, "bin"),
		"GOTOOLCHAIN": "local",
		"GOSUMDB":     "sum.golang.org",
		"GOPRIVATE":   "",
		"GONOPROXY":   "",
		"GONOSUMDB":   "",
		"GOINSECURE":  "",
		"GOFLAGS":     "",
	}

	for key, want := range expected {
		got, ok := vars[key]
		if !ok {
			t.Errorf("missing key %q", key)
			continue
		}
		if got != want {
			t.Errorf("vars[%q] = %q, want %q", key, got, want)
		}
	}

	if len(vars) != len(expected) {
		t.Errorf("vars has %d entries, want %d", len(vars), len(expected))
	}

	// Generation must be allowed to write go.mod/go.sum, so -mod=readonly must
	// NOT be set here (it would make `go get` fail).
	if strings.Contains(vars["GOFLAGS"], "-mod=readonly") {
		t.Errorf("generation GOFLAGS must not contain -mod=readonly, got %q", vars["GOFLAGS"])
	}
	// Checksum DB verification must stay enabled during dependency resolution:
	// this is exactly when go.sum entries are first recorded, so an inherited
	// GOSUMDB=off must not be able to disable it.
	if vars["GOSUMDB"] != "sum.golang.org" {
		t.Errorf("GOSUMDB must be forced on during generation, got %q", vars["GOSUMDB"])
	}
	// GONOSUMDB independently disables checksum-DB validation, so it must also be
	// cleared during generation — this is precisely when go.sum is written, and
	// an inherited GONOSUMDB=* would let unverified checksums be recorded.
	if vars["GONOSUMDB"] != "" {
		t.Errorf("GONOSUMDB must be cleared during generation, got %q", vars["GONOSUMDB"])
	}
	// Generation must also pin the toolchain so resolving a dependency that
	// requires a newer Go fails fast rather than fetching an unverified toolchain.
	if vars["GOTOOLCHAIN"] != "local" {
		t.Errorf("GOTOOLCHAIN must be forced to local during generation, got %q", vars["GOTOOLCHAIN"])
	}
}

func TestGenerateGoLockFiles_MissingPackageName(t *testing.T) {
	rm := New(makeTestGoRuntimes())
	appConfig := &binmanager.AppConfigGo{Version: "v1.1.4", Runtime: "go"}

	err := rm.GenerateGoLockFiles("govulncheck", appConfig, t.TempDir())
	if err == nil {
		t.Fatal("expected error when packageName is empty")
	}
	if !strings.Contains(err.Error(), "packageName") {
		t.Errorf("error should mention packageName, got: %v", err)
	}
}

func TestGenerateGoLockFiles_MissingVersion(t *testing.T) {
	rm := New(makeTestGoRuntimes())
	appConfig := &binmanager.AppConfigGo{PackageName: "golang.org/x/vuln/cmd/govulncheck", Runtime: "go"}

	err := rm.GenerateGoLockFiles("govulncheck", appConfig, t.TempDir())
	if err == nil {
		t.Fatal("expected error when version is empty")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error should mention version, got: %v", err)
	}
}

func TestGenerateGoLockFiles_InvalidRuntime(t *testing.T) {
	rm := New(makeTestGoRuntimes())
	appConfig := &binmanager.AppConfigGo{
		PackageName: "golang.org/x/vuln/cmd/govulncheck",
		Version:     "v1.1.4",
		Runtime:     "nonexistent",
	}

	err := rm.GenerateGoLockFiles("govulncheck", appConfig, t.TempDir())
	if err == nil {
		t.Fatal("expected error for nonexistent runtime")
	}
}

func TestRemoveStaleGoModFiles_CleanWorkDir(t *testing.T) {
	// No go.mod/go.sum present: a missing file must not be treated as an error.
	if err := removeStaleGoModFiles(t.TempDir()); err != nil {
		t.Errorf("expected nil error on a clean workdir, got: %v", err)
	}
}

func TestRemoveStaleGoModFiles_RemovesExisting(t *testing.T) {
	workDir := t.TempDir()
	for _, name := range []string{"go.mod", "go.sum"} {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte("stale"), 0644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	if err := removeStaleGoModFiles(workDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, name := range []string{"go.mod", "go.sum"} {
		if _, err := os.Stat(filepath.Join(workDir, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s should have been removed, stat err = %v", name, err)
		}
	}
}

func TestRemoveStaleGoModFiles_PropagatesNonNotExistError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses directory permission checks")
	}

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("stale"), 0644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	// A read-only parent directory makes os.Remove fail with EACCES rather than
	// ErrNotExist, so the error must be propagated instead of swallowed.
	if err := os.Chmod(workDir, 0o555); err != nil {
		t.Fatalf("chmod workDir: %v", err)
	}
	defer func() { _ = os.Chmod(workDir, 0o755) }()

	err := removeStaleGoModFiles(workDir)
	if err == nil {
		t.Fatal("expected error when go.mod cannot be removed")
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error should not be a NotExist error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "go.mod") {
		t.Errorf("error should name the file it failed to clean, got: %v", err)
	}
}

func TestForceRemoveAll_RemovesReadOnlyModuleCache(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}

	root := t.TempDir()
	// Mirror the layout `go get` leaves under GOMODCACHE: a read-only module
	// directory containing read-only files. A plain os.RemoveAll cannot unlink
	// entries inside a read-only directory and would leak the tree.
	modDir := filepath.Join(root, "gomodcache", "golang.org", "x", "sys@v0.1.0")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatalf("mkdir module dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "LICENSE"), []byte("license"), 0o444); err != nil {
		t.Fatalf("seed read-only file: %v", err)
	}
	if err := os.Chmod(modDir, 0o555); err != nil {
		t.Fatalf("chmod module dir read-only: %v", err)
	}

	// Sanity check: the read-only layout actually defeats a plain os.RemoveAll,
	// so this test would catch a regression back to it.
	if err := os.RemoveAll(modDir); err == nil {
		t.Skip("platform allows removing read-only-dir contents; ForceRemoveAll guard not exercised")
	}

	if err := ForceRemoveAll(root); err != nil {
		t.Fatalf("ForceRemoveAll() error = %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("root %q should have been removed, stat err = %v", root, err)
	}
}

// TestForceRemoveAll_RemovesNoExecuteDirectory pins the fix for the pnpm/
// half-deleted-store failure: a directory left with a file-like mode (0o600 —
// readable and writable but NOT traversable) must still be removed. A directory
// without its execute bit cannot be opened to unlink its entries, so a plain
// os.RemoveAll fails with EACCES on openat. ForceRemoveAll must restore the
// execute bit regardless of the dirent's reported type.
func TestForceRemoveAll_RemovesNoExecuteDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}

	root := t.TempDir()
	inner := filepath.Join(root, "pkg", "node_modules", "commander")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatalf("mkdir inner dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inner, "LICENSE"), []byte("license"), 0o444); err != nil {
		t.Fatalf("seed read-only file: %v", err)
	}
	// Strip the execute bit from the leaf directory (mode 0o600, "drw-------").
	if err := os.Chmod(inner, 0o600); err != nil {
		t.Fatalf("chmod no-execute dir: %v", err)
	}

	if err := os.RemoveAll(inner); err == nil {
		t.Skip("platform allows removing a no-execute directory; guard not exercised")
	}

	if err := ForceRemoveAll(root); err != nil {
		t.Fatalf("ForceRemoveAll() error = %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("root %q should have been removed, stat err = %v", root, err)
	}
}

func TestForceRemoveAll_NonexistentPathIsNoError(t *testing.T) {
	if err := ForceRemoveAll(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Errorf("ForceRemoveAll on a missing path should be a no-op, got: %v", err)
	}
}

func TestGetGoCommandInfo(t *testing.T) {
	rm := New(makeTestGoRuntimes())

	t.Run("returns go command info", func(t *testing.T) {
		appConfig := &binmanager.AppConfigGo{
			PackageName: "golang.org/x/vuln/cmd/govulncheck",
			Version:     "v1.1.4",
			Runtime:     "go",
		}

		info, err := rm.GetGoCommandInfo("govulncheck", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("GetGoCommandInfo() error = %v", err)
		}
		if info.Type != "go" {
			t.Errorf("Type = %q, want %q", info.Type, "go")
		}
		if info.Command == "" {
			t.Error("Command is empty")
		}
		if !strings.HasSuffix(info.Command, goBinaryName(appConfig.PackageName)) {
			t.Errorf("Command %q should end with binary name %q", info.Command, goBinaryName(appConfig.PackageName))
		}
	})

	t.Run("invalid runtime returns error", func(t *testing.T) {
		appConfig := &binmanager.AppConfigGo{
			PackageName: "golang.org/x/vuln/cmd/govulncheck",
			Version:     "v1.1.4",
			Runtime:     "nonexistent",
		}
		if _, err := rm.GetGoCommandInfo("govulncheck", appConfig, nil, nil); err == nil {
			t.Error("expected error for nonexistent runtime, got nil")
		}
	})

	t.Run("deterministic paths", func(t *testing.T) {
		appConfig := &binmanager.AppConfigGo{
			PackageName: "golang.org/x/vuln/cmd/govulncheck",
			Version:     "v1.1.4",
			Runtime:     "go",
		}
		info1, err := rm.GetGoCommandInfo("govulncheck", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		info2, err := rm.GetGoCommandInfo("govulncheck", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}
		if info1.Command != info2.Command {
			t.Errorf("paths not deterministic: %q != %q", info1.Command, info2.Command)
		}
	})

	t.Run("different versions produce different paths", func(t *testing.T) {
		config1 := &binmanager.AppConfigGo{PackageName: "golang.org/x/vuln/cmd/govulncheck", Version: "v1.1.4", Runtime: "go"}
		config2 := &binmanager.AppConfigGo{PackageName: "golang.org/x/vuln/cmd/govulncheck", Version: "v1.1.5", Runtime: "go"}

		info1, err := rm.GetGoCommandInfo("govulncheck", config1, nil, nil)
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		info2, err := rm.GetGoCommandInfo("govulncheck", config2, nil, nil)
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}
		if info1.Command == info2.Command {
			t.Error("different versions should produce different paths")
		}
	})

	t.Run("different lockfiles produce different paths", func(t *testing.T) {
		config1 := &binmanager.AppConfigGo{PackageName: "golang.org/x/vuln/cmd/govulncheck", Version: "v1.1.4", Runtime: "go", LockFile: "lock-v1"}
		config2 := &binmanager.AppConfigGo{PackageName: "golang.org/x/vuln/cmd/govulncheck", Version: "v1.1.4", Runtime: "go", LockFile: "lock-v2"}

		info1, err := rm.GetGoCommandInfo("govulncheck", config1, nil, nil)
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		info2, err := rm.GetGoCommandInfo("govulncheck", config2, nil, nil)
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}
		if info1.Command == info2.Command {
			t.Error("different lockFile contents should produce different paths")
		}
	})

	t.Run("same lockfile but different packageName produces different paths", func(t *testing.T) {
		// Two commands from the same module share a byte-identical lockfile
		// (go.mod/go.sum). The package path is part of the build identity, so
		// their cache directories must not collide.
		lock := "shared-lock"
		config1 := &binmanager.AppConfigGo{PackageName: "golang.org/x/tools/cmd/goimports", Version: "v0.1.0", Runtime: "go", LockFile: lock}
		config2 := &binmanager.AppConfigGo{PackageName: "golang.org/x/tools/cmd/stringer", Version: "v0.1.0", Runtime: "go", LockFile: lock}

		path1, err := rm.GetGoAppPath("tool", config1, nil, nil, "go")
		if err != nil {
			t.Fatalf("first GetGoAppPath error = %v", err)
		}
		path2, err := rm.GetGoAppPath("tool", config2, nil, nil, "go")
		if err != nil {
			t.Fatalf("second GetGoAppPath error = %v", err)
		}
		if path1 == path2 {
			t.Error("different packageName with the same lockFile should produce different cache paths")
		}
	})
}

// TestGoBuildReadonlyFailsOnGoSumMismatch is the supply-chain guarantee in
// action: it drives a real `go build` using the exact flags datamitsu emits
// (buildGoBuildArgs) and the verification-preserving env (getGoEnvVars) against
// a module whose go.sum is missing the required checksum. With -mod=readonly the
// build must fail rather than silently rewrite go.sum, and with GOPROXY=off the
// test stays hermetic (no network). This is what makes a tampered/stale go.sum
// in a Go app's lockFile fail the build instead of installing unverified code.
func TestGoBuildReadonlyFailsOnGoSumMismatch(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not available; skipping supply-chain build test")
	}

	dir := t.TempDir()
	goMod := "module testmod\n\ngo 1.21\n\nrequire rsc.io/quote v1.5.2\n"
	mainGo := "package main\n\nimport _ \"rsc.io/quote\"\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	// Empty go.sum: the required checksum entry is absent, modelling a tampered
	// or stale lockfile. -mod=readonly must refuse to add it.
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), nil, 0644); err != nil {
		t.Fatalf("write go.sum: %v", err)
	}

	// Reuse datamitsu's canonical build flags so the test stays tied to the real
	// implementation. Strip the -o/output and trailing package, build the local
	// module instead.
	canonical := buildGoBuildArgs("rsc.io/quote", filepath.Join(dir, "out"))
	var args []string
	for i := 0; i < len(canonical); i++ {
		if canonical[i] == "-o" {
			i++ // skip the output path too
			continue
		}
		if canonical[i] == "rsc.io/quote" {
			continue
		}
		args = append(args, canonical[i])
	}
	args = append(args, "./...")

	cmd := exec.Command(goBin, args...)
	cmd.Dir = dir
	// getGoEnvVars keeps checksum verification enabled (GOSUMDB forced on) and
	// pins the toolchain (GOTOOLCHAIN=local); GOPROXY=off makes the test hermetic.
	cmd.Env = buildEnvWithOverrides(os.Environ(), getGoEnvVars(dir))
	cmd.Env = append(cmd.Env, "GOPROXY=off")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected build to fail on missing go.sum entry, but it succeeded; output:\n%s", out)
	}
	if !strings.Contains(string(out), "go.sum") {
		t.Errorf("expected go.sum verification failure, got:\n%s", out)
	}
}
