package sourcefarm

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
)

// farmFixture is a cache root, a fake content-addressed store and a fake
// datamitsu executable — everything a bake needs that is not the plan itself.
type farmFixture struct {
	cacheRoot  string
	farmDir    string
	parent     string
	shimTarget string
	storeDir   string
}

func newFarmFixture(t *testing.T) farmFixture {
	t.Helper()
	base := t.TempDir()

	fx := farmFixture{
		cacheRoot: filepath.Join(base, "cache"),
		storeDir:  filepath.Join(base, "store"),
	}
	fx.parent = filepath.Join(fx.cacheRoot, "projects", "abc123")
	fx.farmDir = filepath.Join(fx.parent, "bin")
	if err := os.MkdirAll(fx.storeDir, 0o755); err != nil {
		t.Fatalf("create store dir: %v", err)
	}
	// Resolved because Materialize records the symlink-resolved executable path,
	// and t.TempDir hands out a path under a symlinked /var on macOS.
	shim := fx.executable(t, "datamitsu")
	resolved, err := filepath.EvalSymlinks(shim)
	if err != nil {
		t.Fatalf("resolve shim target: %v", err)
	}
	fx.shimTarget = resolved
	return fx
}

// executable writes an executable file into the fake store and returns its path.
func (fx farmFixture) executable(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(fx.storeDir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", name, err)
	}
	return path
}

func (fx farmFixture) options() Options {
	return Options{
		ShimTarget:   fx.shimTarget,
		CacheRoot:    fx.cacheRoot,
		LockTimeout:  2 * time.Second,
		PollInterval: time.Millisecond,
		Warn:         func(string) {},
	}
}

func (fx farmFixture) manifestPath() string {
	return filepath.Join(fx.parent, env.ProjectManifestFileName)
}

// plan builds a plan whose symlink entries point at freshly created store files.
func (fx farmFixture) plan(t *testing.T, symlinked []string, shimmed []string) Plan {
	t.Helper()
	plan := Plan{Root: "/repo", FarmDir: fx.farmDir}
	for _, name := range symlinked {
		plan.Entries = append(plan.Entries, Entry{
			Name:      name,
			Kind:      "binary",
			Strategy:  StrategySymlink,
			Command:   fx.executable(t, name+".bin"),
			Installed: true,
		})
	}
	for _, name := range shimmed {
		plan.Entries = append(plan.Entries, Entry{Name: name, Kind: "node", Strategy: StrategyShim})
	}
	sort.Slice(plan.Entries, func(i, j int) bool { return plan.Entries[i].Name < plan.Entries[j].Name })
	return plan
}

func manifestFor(plan Plan, key string) Manifest {
	return Manifest{
		FormatVersion:    ManifestFormatVersion,
		Origin:           OriginGitRoot,
		Root:             plan.Root,
		FarmDir:          plan.FarmDir,
		DatamitsuVersion: "test",
		OS:               "linux",
		Arch:             "arm64",
		StalenessKey:     key,
		Entries:          plan.Entries,
	}
}

// listFarm returns the sorted entry names plus their link targets.
func listFarm(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read farm dir %s: %v", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("readlink %s: %v", e.Name(), err)
		}
		out = append(out, e.Name()+" -> "+target)
	}
	sort.Strings(out)
	return out
}

func TestMaterializeCreatesEntries(t *testing.T) {
	fx := newFarmFixture(t)
	plan := fx.plan(t, []string{"tofu"}, []string{"prettier"})

	if err := MaterializeWithOptions(plan, manifestFor(plan, "k1"), fx.options()); err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}

	got := listFarm(t, fx.farmDir)
	want := []string{
		"prettier -> " + fx.shimTarget,
		"tofu -> " + filepath.Join(fx.storeDir, "tofu.bin"),
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("farm listing = %v, want %v", got, want)
	}

	m, err := Load(fx.manifestPath())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if m.StalenessKey != "k1" || len(m.Entries) != 2 {
		t.Errorf("manifest = %+v, want staleness key k1 and 2 entries", m)
	}
}

func TestMaterializeRemovesStaleEntries(t *testing.T) {
	fx := newFarmFixture(t)
	first := fx.plan(t, []string{"tofu", "kubectl"}, []string{"prettier"})
	if err := MaterializeWithOptions(first, manifestFor(first, "k1"), fx.options()); err != nil {
		t.Fatalf("first Materialize() error = %v", err)
	}

	second := fx.plan(t, []string{"tofu"}, nil)
	if err := MaterializeWithOptions(second, manifestFor(second, "k2"), fx.options()); err != nil {
		t.Fatalf("second Materialize() error = %v", err)
	}

	got := listFarm(t, fx.farmDir)
	if len(got) != 1 {
		t.Fatalf("farm listing = %v, want exactly the one entry still in the plan", got)
	}
	if _, err := os.Lstat(filepath.Join(fx.farmDir, "kubectl")); !os.IsNotExist(err) {
		t.Errorf("kubectl still present after it left the plan: err = %v", err)
	}
}

func TestMaterializeModes(t *testing.T) {
	fx := newFarmFixture(t)
	plan := fx.plan(t, []string{"tofu"}, []string{"prettier"})
	if err := MaterializeWithOptions(plan, manifestFor(plan, "k1"), fx.options()); err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}

	info, err := os.Stat(fx.farmDir)
	if err != nil {
		t.Fatalf("stat farm dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("farm directory mode = %04o, want 0700", perm)
	}

	for _, name := range []string{"tofu", "prettier"} {
		path := filepath.Join(fx.farmDir, name)
		link, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
		if link.Mode()&os.ModeSymlink == 0 {
			t.Errorf("farm entry %s is not a symlink", name)
		}
		// The effective mode of an entry is its target's: the kernel never
		// consults a symlink's own permission bits.
		target, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s through the link: %v", name, err)
		}
		if perm := target.Mode().Perm(); perm != 0o755 {
			t.Errorf("farm entry %s effective mode = %04o, want 0755", name, perm)
		}
	}
}

func TestMaterializeRejectsNonExecutableTarget(t *testing.T) {
	fx := newFarmFixture(t)
	plan := fx.plan(t, []string{"tofu"}, nil)
	if err := os.Chmod(plan.Entries[0].Command, 0o644); err != nil {
		t.Fatalf("chmod target: %v", err)
	}

	if err := MaterializeWithOptions(plan, manifestFor(plan, "k1"), fx.options()); err == nil {
		t.Fatal("Materialize() = nil, want an error for a non-executable target")
	}
}

func TestMaterializeRejectsPathOutsideCacheRoot(t *testing.T) {
	fx := newFarmFixture(t)
	outside := filepath.Join(t.TempDir(), "elsewhere", "bin")

	tests := []struct {
		name    string
		farmDir string
	}{
		{"outside the cache root", outside},
		{"traversal out of the cache root", filepath.Join(fx.cacheRoot, "..", "escape", "bin")},
		{"the cache root itself", fx.cacheRoot},
		{"relative", "relative/bin"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := Plan{Root: "/repo", FarmDir: tt.farmDir}
			if err := MaterializeWithOptions(plan, manifestFor(plan, "k1"), fx.options()); err == nil {
				t.Errorf("Materialize(%q) = nil, want an error", tt.farmDir)
			}
			if _, err := os.Stat(tt.farmDir); tt.farmDir != "" && err == nil {
				t.Errorf("Materialize(%q) created the directory anyway", tt.farmDir)
			}
		})
	}
}

func TestMaterializeUsesDefaultCacheRoot(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(base, "cache"))

	farmDir, err := env.GetProjectBinPath("/repo")
	if err != nil {
		t.Fatalf("GetProjectBinPath() error = %v", err)
	}
	shim := filepath.Join(base, "datamitsu")
	if err := os.WriteFile(shim, []byte("x"), 0o755); err != nil {
		t.Fatalf("write shim target: %v", err)
	}

	plan := Plan{Root: "/repo", FarmDir: farmDir, Entries: []Entry{{Name: "prettier", Kind: "node", Strategy: StrategyShim}}}
	opts := Options{ShimTarget: shim, LockTimeout: time.Second, PollInterval: time.Millisecond, Warn: func(string) {}}
	if err := MaterializeWithOptions(plan, manifestFor(plan, "k1"), opts); err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(farmDir, "prettier")); err != nil {
		t.Errorf("entry missing from the default-cache-root farm: %v", err)
	}
}

func TestMaterializeIsRepeatable(t *testing.T) {
	fx := newFarmFixture(t)
	plan := fx.plan(t, []string{"tofu", "kubectl"}, []string{"prettier"})
	m := manifestFor(plan, "k1")

	if err := MaterializeWithOptions(plan, m, fx.options()); err != nil {
		t.Fatalf("first Materialize() error = %v", err)
	}
	first := listFarm(t, fx.farmDir)
	firstManifest, err := os.ReadFile(fx.manifestPath())
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	if err := MaterializeWithOptions(plan, m, fx.options()); err != nil {
		t.Fatalf("second Materialize() error = %v", err)
	}
	second := listFarm(t, fx.farmDir)
	secondManifest, err := os.ReadFile(fx.manifestPath())
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("listings differ in length: %v vs %v", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("listing entry %d = %q, want %q", i, second[i], first[i])
		}
	}
	if string(firstManifest) != string(secondManifest) {
		t.Error("manifest bytes differ between two identical bakes")
	}
}

func TestMaterializeNeverLeavesDanglingSymlink(t *testing.T) {
	fx := newFarmFixture(t)
	plan := fx.plan(t, []string{"tofu"}, []string{"prettier", "spectral"})
	if err := MaterializeWithOptions(plan, manifestFor(plan, "k1"), fx.options()); err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}

	entries, err := os.ReadDir(fx.farmDir)
	if err != nil {
		t.Fatalf("read farm dir: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("farm has %d entries, want 3", len(entries))
	}
	for _, e := range entries {
		path := filepath.Join(fx.farmDir, e.Name())
		if _, err := os.Stat(path); err != nil {
			t.Errorf("farm entry %s is dangling: %v", e.Name(), err)
		}
	}
}

func TestMaterializeRefusesDanglingSymlinkTarget(t *testing.T) {
	fx := newFarmFixture(t)
	plan := fx.plan(t, []string{"tofu"}, nil)
	if err := os.Remove(plan.Entries[0].Command); err != nil {
		t.Fatalf("remove target: %v", err)
	}

	if err := MaterializeWithOptions(plan, manifestFor(plan, "k1"), fx.options()); err == nil {
		t.Fatal("Materialize() = nil, want an error for a vanished symlink target")
	}
	if _, err := os.Stat(fx.farmDir); !os.IsNotExist(err) {
		t.Errorf("failed bake created a farm directory anyway: err = %v", err)
	}
}

func TestMaterializeFailureKeepsPreviousFarm(t *testing.T) {
	fx := newFarmFixture(t)
	good := fx.plan(t, []string{"tofu"}, []string{"prettier"})
	if err := MaterializeWithOptions(good, manifestFor(good, "k1"), fx.options()); err != nil {
		t.Fatalf("first Materialize() error = %v", err)
	}
	before := listFarm(t, fx.farmDir)
	manifestBefore, err := os.ReadFile(fx.manifestPath())
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	// A bake that fails midway: the second entry's target vanished from the
	// store between planning and baking.
	broken := fx.plan(t, []string{"kubectl", "helm"}, nil)
	if err := os.Remove(broken.Entries[1].Command); err != nil {
		t.Fatalf("remove target: %v", err)
	}

	var warned []string
	opts := fx.options()
	opts.Warn = func(line string) { warned = append(warned, line) }
	if err := MaterializeWithOptions(broken, manifestFor(broken, "k2"), opts); err == nil {
		t.Fatal("Materialize() = nil, want an error")
	}
	if len(warned) != 1 {
		t.Errorf("warn lines = %v, want exactly one", warned)
	}

	after := listFarm(t, fx.farmDir)
	if len(after) != len(before) {
		t.Fatalf("farm after the failed bake = %v, want the previous %v", after, before)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("farm entry %d = %q, want the previous %q", i, after[i], before[i])
		}
	}
	manifestAfter, err := os.ReadFile(fx.manifestPath())
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if string(manifestAfter) != string(manifestBefore) {
		t.Error("failed bake advanced the manifest; it must keep the previous one")
	}
	if leftovers := stagingDirs(t, fx.parent); len(leftovers) != 0 {
		t.Errorf("failed bake left staging directories behind: %v", leftovers)
	}
}

// stagingDirs returns any leftover staging directories in the per-root dir.
func stagingDirs(t *testing.T, parent string) []string {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read %s: %v", parent, err)
	}
	var out []string
	for _, e := range entries {
		if len(e.Name()) > 6 && e.Name()[:7] == ".stage-" {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestMaterializeRefusesUnsafeExistingFarm(t *testing.T) {
	fx := newFarmFixture(t)
	plan := fx.plan(t, []string{"tofu"}, nil)
	if err := MaterializeWithOptions(plan, manifestFor(plan, "k1"), fx.options()); err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	if err := os.Chmod(fx.farmDir, 0o777); err != nil {
		t.Fatalf("chmod farm dir: %v", err)
	}

	err := MaterializeWithOptions(plan, manifestFor(plan, "k2"), fx.options())
	if err == nil {
		t.Fatal("Materialize() = nil, want an error for a world-writable farm directory")
	}
	if _, statErr := os.Stat(filepath.Join(fx.farmDir, "tofu")); statErr != nil {
		t.Errorf("refused bake disturbed the existing farm: %v", statErr)
	}
}

func TestMaterializeRefusesNonDirectoryFarmPath(t *testing.T) {
	fx := newFarmFixture(t)
	if err := os.MkdirAll(fx.parent, 0o700); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if err := os.WriteFile(fx.farmDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write file at farm path: %v", err)
	}

	plan := fx.plan(t, []string{"tofu"}, nil)
	if err := MaterializeWithOptions(plan, manifestFor(plan, "k1"), fx.options()); err == nil {
		t.Fatal("Materialize() = nil, want an error when the farm path is a file")
	}
}

func TestMaterializeRejectsUnusableShimTarget(t *testing.T) {
	fx := newFarmFixture(t)
	opts := fx.options()
	opts.ShimTarget = filepath.Join(fx.storeDir, "does-not-exist")

	plan := fx.plan(t, nil, []string{"prettier"})
	if err := MaterializeWithOptions(plan, manifestFor(plan, "k1"), opts); err == nil {
		t.Fatal("Materialize() = nil, want an error for a missing shim target")
	}
}

func TestMaterializeRejectsEntryNameEscapingFarm(t *testing.T) {
	fx := newFarmFixture(t)
	plan := Plan{
		Root:    "/repo",
		FarmDir: fx.farmDir,
		Entries: []Entry{{Name: "../escaped", Kind: "node", Strategy: StrategyShim}},
	}
	if err := MaterializeWithOptions(plan, manifestFor(plan, "k1"), fx.options()); err == nil {
		t.Fatal("Materialize() = nil, want an error for an escaping entry name")
	}
	if _, err := os.Lstat(filepath.Join(fx.parent, "escaped")); !os.IsNotExist(err) {
		t.Errorf("escaping entry was written outside the farm: err = %v", err)
	}
}

func TestMaterializeConcurrent(t *testing.T) {
	fx := newFarmFixture(t)
	plan := fx.plan(t, []string{"tofu", "kubectl"}, []string{"prettier"})
	m := manifestFor(plan, "k1")

	const workers = 8
	errs := make([]error, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range workers {
		wg.Go(func() {
			<-start
			errs[i] = MaterializeWithOptions(plan, m, fx.options())
		})
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: Materialize() error = %v", i, err)
		}
	}

	got := listFarm(t, fx.farmDir)
	if len(got) != 3 {
		t.Fatalf("farm listing = %v, want 3 entries", got)
	}
	for _, name := range []string{"tofu", "kubectl", "prettier"} {
		if _, err := os.Stat(filepath.Join(fx.farmDir, name)); err != nil {
			t.Errorf("entry %s missing or dangling after concurrent bakes: %v", name, err)
		}
	}
	if leftovers := stagingDirs(t, fx.parent); len(leftovers) != 0 {
		t.Errorf("concurrent bakes left staging directories behind: %v", leftovers)
	}
	if _, err := os.Stat(filepath.Join(fx.parent, env.ProjectLockFileName)); err != nil {
		t.Errorf("lock file must survive the bake, not be unlinked: %v", err)
	}
}

func TestMaterializePeerWinSkipsRebake(t *testing.T) {
	fx := newFarmFixture(t)
	plan := fx.plan(t, []string{"tofu"}, nil)
	m := manifestFor(plan, "k1")

	// Bake once so the manifest a waiting peer would find is already on disk.
	if err := MaterializeWithOptions(plan, m, fx.options()); err != nil {
		t.Fatalf("initial Materialize() error = %v", err)
	}
	info, err := os.Lstat(filepath.Join(fx.farmDir, "tofu"))
	if err != nil {
		t.Fatalf("lstat entry: %v", err)
	}

	// Hold the lock from another open file description, exactly as a peer
	// process would.
	lockPath := filepath.Join(fx.parent, env.ProjectLockFileName)
	held, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock: %v", err)
	}
	defer func() { _ = held.Close() }()
	if err := lockFile(held); err != nil {
		t.Fatalf("lock: %v", err)
	}

	opts := fx.options()
	opts.LockTimeout = 200 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- MaterializeWithOptions(plan, m, opts) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Materialize() error = %v, want a clean peer-won skip", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Materialize() blocked on the lock instead of noticing the peer's manifest")
	}

	after, err := os.Lstat(filepath.Join(fx.farmDir, "tofu"))
	if err != nil {
		t.Fatalf("lstat entry after peer win: %v", err)
	}
	if !os.SameFile(info, after) {
		t.Error("peer-won bake replaced the farm entry instead of leaving it alone")
	}
	unlockFile(held)
}

func TestMaterializeBlocksWhenPeerProducesNothing(t *testing.T) {
	fx := newFarmFixture(t)
	plan := fx.plan(t, []string{"tofu"}, nil)

	lockPath := filepath.Join(fx.parent, env.ProjectLockFileName)
	if err := os.MkdirAll(fx.parent, 0o700); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	held, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock: %v", err)
	}
	if err := lockFile(held); err != nil {
		t.Fatalf("lock: %v", err)
	}

	opts := fx.options()
	opts.LockTimeout = 50 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- MaterializeWithOptions(plan, manifestFor(plan, "k1"), opts) }()

	// Nothing appears while the lock is held, so the waiter must fall through to
	// blocking and bake as soon as the lock is released.
	time.Sleep(150 * time.Millisecond)
	if _, err := os.Stat(fx.farmDir); !os.IsNotExist(err) {
		t.Errorf("farm baked while a peer held the lock: err = %v", err)
	}
	unlockFile(held)
	_ = held.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Materialize() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Materialize() never completed after the lock was released")
	}
	if _, err := os.Stat(filepath.Join(fx.farmDir, "tofu")); err != nil {
		t.Errorf("entry missing after the blocking path baked: %v", err)
	}
}

func TestMaterializeDefaultWarnWritesOneStderrLine(t *testing.T) {
	fx := newFarmFixture(t)
	plan := fx.plan(t, []string{"tofu"}, nil)
	if err := os.Remove(plan.Entries[0].Command); err != nil {
		t.Fatalf("remove target: %v", err)
	}

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	original := os.Stderr
	os.Stderr = write
	defer func() { os.Stderr = original }()

	opts := fx.options()
	opts.Warn = nil
	bakeErr := MaterializeWithOptions(plan, manifestFor(plan, "k1"), opts)
	_ = write.Close()
	os.Stderr = original

	if bakeErr == nil {
		t.Fatal("Materialize() = nil, want an error")
	}
	buf := make([]byte, 4096)
	n, _ := read.Read(buf)
	out := string(buf[:n])
	if n == 0 {
		t.Fatal("default Warn wrote nothing to stderr")
	}
	if count := countNewlines(out); count != 1 {
		t.Errorf("default Warn wrote %d lines to stderr, want exactly 1: %q", count, out)
	}
}

func countNewlines(s string) int {
	n := 0
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}
	return n
}

// The bake must stay silent: the caller's stdout is a shell script being piped
// into eval, and any package-level UI primitive can put a byte on it. Asserting
// on the import graph is what makes that structural rather than a convention.
func TestPackageDoesNotImportUI(t *testing.T) {
	sources, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fileSet := token.NewFileSet()
	for _, source := range sources {
		name := source.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasSuffix(path, "/internal/ui") || strings.Contains(path, "/internal/ui/") {
				t.Errorf("%s imports %s; the farm bake must be silent", name, path)
			}
		}
	}
}
