package runtimemanager

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// systemGoRuntimes returns runtimes with a system-mode Go resolved from PATH,
// or nil when no go toolchain is on this machine (the test then skips).
func systemGoRuntimes(t *testing.T) config.MapOfRuntimes {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil
	}
	abs, err := filepath.Abs(goBin)
	if err != nil {
		return nil
	}
	return config.MapOfRuntimes{
		"go": {
			Kind:   config.RuntimeKindGo,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: abs},
		},
	}
}

// sourceModuleTar builds an uncompressed tar of a small stdlib-only Go module
// with a main package under cmd/<name>.
func sourceModuleTar(t *testing.T, modulePath, name, message string) []byte {
	t.Helper()
	main := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(" + quoteGoString(message) + ") }\n"
	gomod := "module " + modulePath + "\n\ngo 1.22\n"
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	write := func(path, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: path, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("tar header %q: %v", path, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body %q: %v", path, err)
		}
	}
	write("go.mod", gomod)
	write("cmd/"+name+"/main.go", main)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func quoteGoString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestInstallGoApp_FromInlineArchive builds a Go app whose source ships as an
// inline tar.br archive: the installer stages it, overlays the lockfile's
// go.mod/go.sum, builds with -mod=readonly, and the binary runs.
func TestInstallGoApp_FromInlineArchive(t *testing.T) {
	runtimes := systemGoRuntimes(t)
	if runtimes == nil {
		t.Skip("no go toolchain on PATH; the source-blob build cannot be exercised")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(runtimes)

	inline, err := binmanager.CompressArchive(sourceModuleTar(t, "example.com/mygen", "mygen", "blob-built"))
	if err != nil {
		t.Fatal(err)
	}
	// The lock's go.mod matches the shipped module (module path + go
	// version); its go.sum is EMPTY (stdlib-only source).
	lockJSON, err := BuildGoLockFileJSON("module example.com/mygen\n\ngo 1.22\n", "")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := CompressLockFile(lockJSON)
	if err != nil {
		t.Fatal(err)
	}

	appConfig := &binmanager.AppConfigGo{
		PackageName: "example.com/mygen/cmd/mygen",
		Version:     "v0.1.0",
		Runtime:     "go",
		LockFile:    lock,
	}
	archives := map[string]*binmanager.ArchiveSpec{"main": {Inline: inline}}

	if err := rm.InstallGoApp(context.Background(), "my-gen", appConfig, nil, nil, archives); err != nil {
		t.Fatalf("InstallGoApp from inline archive: %v", err)
	}

	appPath, err := rm.GetGoAppPath("my-gen", appConfig, nil, archives, "go")
	if err != nil {
		t.Fatal(err)
	}
	binName := "mygen"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	out, err := exec.Command(filepath.Join(appPath, "bin", binName)).Output()
	if err != nil {
		t.Fatalf("built binary failed to run: %v", err)
	}
	if strings.TrimSpace(string(out)) != "blob-built" {
		t.Fatalf("binary output = %q, want the staged source's message", out)
	}
}

// TestInstallGoApp_ArchiveChangeChangesInstallPath pins the identity rule:
// the archive content folds into the install path (HashFilesAndArchives), so
// new source means a new install directory.
func TestInstallGoApp_ArchiveChangeChangesInstallPath(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(makeTestGoRuntimes())
	appConfig := &binmanager.AppConfigGo{
		PackageName: "example.com/mygen/cmd/mygen",
		Version:     "v0.1.0",
		Runtime:     "go",
		LockFile:    "br:irrelevant-for-path",
	}

	inline1, err := binmanager.CompressArchive(sourceModuleTar(t, "example.com/mygen", "mygen", "one"))
	if err != nil {
		t.Fatal(err)
	}
	inline2, err := binmanager.CompressArchive(sourceModuleTar(t, "example.com/mygen", "mygen", "two"))
	if err != nil {
		t.Fatal(err)
	}
	p1, err := rm.GetGoAppPath("my-gen", appConfig, nil, map[string]*binmanager.ArchiveSpec{"main": {Inline: inline1}}, "go")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := rm.GetGoAppPath("my-gen", appConfig, nil, map[string]*binmanager.ArchiveSpec{"main": {Inline: inline2}}, "go")
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatalf("install path did not change with the archive: %q", p1)
	}
}

// TestGenerateGoLockFiles_FromSource proves the source-aware flow: staging
// the module, tidying against ITS go.mod, and an empty go.sum for a
// stdlib-only source. The generated lock then builds the blob on install.
func TestGenerateGoLockFiles_FromSource(t *testing.T) {
	runtimes := systemGoRuntimes(t)
	if runtimes == nil {
		t.Skip("no go toolchain on PATH; the source-aware generation cannot be exercised")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(runtimes)

	inline, err := binmanager.CompressArchive(sourceModuleTar(t, "example.com/mygen2", "mygen2", "generated"))
	if err != nil {
		t.Fatal(err)
	}
	appConfig := &binmanager.AppConfigGo{
		PackageName: "example.com/mygen2/cmd/mygen2",
		Version:     "v0.1.0",
		Runtime:     "go",
	}
	archives := map[string]*binmanager.ArchiveSpec{"main": {Inline: inline}}

	workDir := t.TempDir()
	if err := rm.GenerateGoLockFilesFromSource(context.Background(), "my-gen2", appConfig, nil, archives, workDir, 0); err != nil {
		t.Fatalf("GenerateGoLockFilesFromSource: %v", err)
	}
	gomod, err := os.ReadFile(filepath.Join(workDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gomod), "module example.com/mygen2") {
		t.Fatalf("generated go.mod lost the shipped module path:\n%s", gomod)
	}
	// `go mod tidy` may leave NO go.sum at all for a stdlib-only module —
	// the empty lock is exactly that.
	gosum := ""
	if data, err := os.ReadFile(filepath.Join(workDir, "go.sum")); err == nil {
		gosum = string(data)
	}
	if len(strings.TrimSpace(gosum)) != 0 {
		t.Fatalf("stdlib-only source must generate an empty (or absent) go.sum, got:\n%s", gosum)
	}

	// End to end: the generated lock installs and the blob builds.
	lockJSON, err := BuildGoLockFileJSON(string(gomod), gosum)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := CompressLockFile(lockJSON)
	if err != nil {
		t.Fatal(err)
	}
	appConfig.LockFile = lock
	if err := rm.InstallGoApp(context.Background(), "my-gen2", appConfig, nil, nil, archives); err != nil {
		t.Fatalf("InstallGoApp with the generated source lock: %v", err)
	}
	binName := "mygen2"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	appPath, err := rm.GetGoAppPath("my-gen2", appConfig, nil, archives, "go")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join(appPath, "bin", binName)).Output()
	if err != nil {
		t.Fatalf("built blob failed to run: %v", err)
	}
	if strings.TrimSpace(string(out)) != "generated" {
		t.Fatalf("binary output = %q, want the staged source's message", out)
	}
}

// TestGenerateGoLockFiles_FromSourceRequiresGoMod pins the guard: a source
// blob without go.mod cannot be locked (nothing names the module packageName
// points inside).
func TestGenerateGoLockFiles_FromSourceRequiresGoMod(t *testing.T) {
	runtimes := systemGoRuntimes(t)
	if runtimes == nil {
		t.Skip("no go toolchain on PATH")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(runtimes)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := "package main\nfunc main() {}\n"
	if err := tw.WriteHeader(&tar.Header{Name: "main.go", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	inline, err := binmanager.CompressArchive(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	appConfig := &binmanager.AppConfigGo{
		PackageName: "example.com/x/cmd/x",
		Version:     "v0.1.0",
		Runtime:     "go",
	}
	err = rm.GenerateGoLockFilesFromSource(context.Background(), "x", appConfig, nil,
		map[string]*binmanager.ArchiveSpec{"main": {Inline: inline}}, t.TempDir(), 0)
	if err == nil || !strings.Contains(err.Error(), "go.mod") {
		t.Fatalf("err = %v, want the missing-go.mod refusal", err)
	}
}

// depTar builds a source tar importing golang.org/x/mod/semver with the
// dependency PINNED in go.mod (so the failure, when it comes, is about the
// missing checksum, not a missing module). extra entries (e.g. go.work) are
// appended verbatim.
func depTar(t *testing.T, extra ...tarEntry) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	write := func(path, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: path, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("tar header %q: %v", path, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body %q: %v", path, err)
		}
	}
	write("go.mod", "module example.com/needsdep\n\ngo 1.22\n\nrequire golang.org/x/mod v0.17.0\n")
	write("main.go", "package main\n\nimport _ \"golang.org/x/mod/semver\"\n\nfunc main() {}\n")
	for _, e := range extra {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body))}); err != nil {
			t.Fatalf("tar header %q: %v", e.name, err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatalf("tar body %q: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	inline, err := binmanager.CompressArchive(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return inline
}

type tarEntry struct{ name, body string }

// refuseInstallWithEmptySum proves the missing-checksum refusal. The module
// cache is warmed FIRST (a source-aware generation whose go.sum is then
// DISCARDED): with the dependency already local, the readonly build cannot
// blame a download and must fail on the absent checksum itself.
func refuseInstallWithEmptySum(t *testing.T, rm *RuntimeManager, inline string) {
	t.Helper()
	appConfig := &binmanager.AppConfigGo{
		PackageName: "example.com/needsdep",
		Version:     "v0.1.0",
		Runtime:     "go",
	}
	archives := map[string]*binmanager.ArchiveSpec{"main": {Inline: inline}}

	// Warm the module cache under a shared cache dir via a real generation;
	// the generated go.sum is thrown away — the install gets an empty one.
	// Go module caches hold read-only files, so cleanup needs ForceRemoveAll.
	shared, mkErr := os.MkdirTemp("", "go-refusal-shared-*") //nolint:usetesting // a Go module cache holds read-only files; t.TempDir's plain RemoveAll cannot clean it
	if mkErr != nil {
		t.Fatal(mkErr)
	}
	defer func() { _ = ForceRemoveAll(shared) }()
	t.Setenv("DATAMITSU_CACHE_DIR", shared)
	genRoot, mkErr := os.MkdirTemp("", "go-refusal-gen-*") //nolint:usetesting // a Go module cache holds read-only files; t.TempDir's plain RemoveAll cannot clean it
	if mkErr != nil {
		t.Fatal(mkErr)
	}
	defer func() { _ = ForceRemoveAll(genRoot) }()
	if err := rm.GenerateGoLockFilesFromSource(context.Background(), "needsdep", appConfig, nil, archives, genRoot, 0); err != nil {
		t.Fatalf("warm-up generation (network) failed: %v", err)
	}

	// The lock FIRST: it contributes to the install-path hash, so the path
	// computed for cache seeding must be the one the install will use.
	lockJSON, err := BuildGoLockFileJSON("module example.com/needsdep\n\ngo 1.22\n\nrequire golang.org/x/mod v0.17.0\n", "")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := CompressLockFile(lockJSON)
	if err != nil {
		t.Fatal(err)
	}
	appConfig.LockFile = lock

	// The install and the reproduced build each derive their OWN GOMODCACHE
	// (under the app dir / the build dir): DATAMITSU_CACHE_DIR does not
	// connect them to the generation's cache. Seed both from the warmed one
	// so the dependency is provably local and the only possible refusal is
	// the missing checksum.
	warmed := filepath.Join(genRoot, "gomodcache")
	depMarker := filepath.Join(warmed, "golang.org", "x", "mod@v0.17.0")
	if _, err := os.Stat(depMarker); err != nil {
		t.Fatalf("warm-up generation did not cache the dependency: %v", err)
	}
	appPath, err := rm.GetGoAppPath("needsdep", appConfig, nil, archives, "go")
	if err != nil {
		t.Fatal(err)
	}
	if err := copyTree(warmed, filepath.Join(appPath, "gomodcache")); err != nil {
		t.Fatalf("seed the install module cache: %v", err)
	}

	err = rm.InstallGoApp(context.Background(), "needsdep", appConfig, nil, nil, archives)
	if err == nil {
		t.Fatal("install accepted a pinned dependency with an empty go.sum")
	}

	// The install's error chain hides the compiler's message (it is printed
	// to the UI, not returned), so prove WHICH refusal it was by reproducing
	// the exact build the installer runs — same env, same -mod=readonly —
	// against the same warmed cache and the empty go.sum. The dir carries a
	// Go module cache (read-only files), so it is removed with ForceRemoveAll
	// rather than t.TempDir's plain RemoveAll.
	buildRoot, mkErr := os.MkdirTemp("", "go-refusal-build-*") //nolint:usetesting // a Go module cache holds read-only files; t.TempDir's plain RemoveAll cannot clean it
	if mkErr != nil {
		t.Fatal(mkErr)
	}
	defer func() { _ = ForceRemoveAll(buildRoot) }()
	buildDir := filepath.Join(buildRoot, "src")
	if err := binmanager.WriteAppFiles(context.Background(), buildDir, nil, archives); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "go.mod"), []byte("module example.com/needsdep\n\ngo 1.22\n\nrequire golang.org/x/mod v0.17.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// go.sum is deliberately NOT written: the empty lock. The build's module
	// cache is seeded from the warmed one too, so the refusal cannot be a
	// download either.
	if err := copyTree(warmed, filepath.Join(buildDir, "gomodcache")); err != nil {
		t.Fatalf("seed the reproduced build's module cache: %v", err)
	}
	goBin, _ := exec.LookPath("go")
	buildEnv := buildEnvWithOverrides(os.Environ(), getGoEnvVars(buildDir))
	var buildOut bytes.Buffer
	cmd := exec.Command(goBin, "build", "-trimpath", "-mod=readonly", "-o", filepath.Join(buildDir, "bin", "needsdep"), "example.com/needsdep")
	cmd.Dir = buildDir
	cmd.Env = buildEnv
	cmd.Stdout = &buildOut
	cmd.Stderr = &buildOut
	if cmd.Run() == nil {
		t.Fatal("the readonly build itself accepted the empty go.sum (the install refusal then had another cause)")
	}
	if !strings.Contains(buildOut.String(), "go.sum") {
		t.Fatalf("the readonly build's refusal must name go.sum, got:\n%s", buildOut.String())
	}
}

// TestInstallGoApp_DependencyWithoutChecksumRefused pins the verification
// contract: a source that imports an external module (pinned in go.mod) with
// an EMPTY go.sum is refused by the -mod=readonly build, not silently
// resolved — and the error says so.
func TestInstallGoApp_DependencyWithoutChecksumRefused(t *testing.T) {
	runtimes := systemGoRuntimes(t)
	if runtimes == nil {
		t.Skip("no go toolchain on PATH")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(runtimes)
	refuseInstallWithEmptySum(t, rm, depTar(t))
}

// TestInstallGoApp_GoWorkCannotBypassChecksums pins GOWORK=off: a go.work
// shipped in the source must not switch the build into workspace mode, where
// go.work.sum could satisfy the dependency despite the empty go.sum.
func TestInstallGoApp_GoWorkCannotBypassChecksums(t *testing.T) {
	runtimes := systemGoRuntimes(t)
	if runtimes == nil {
		t.Skip("no go toolchain on PATH")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(runtimes)
	inline := depTar(t, tarEntry{
		name: "go.work",
		body: "go 1.22\n\nuse .\n",
	})
	refuseInstallWithEmptySum(t, rm, inline)
}

// copyTree copies a directory tree (module caches carry read-only files, so
// permissions are widened on the copies).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
