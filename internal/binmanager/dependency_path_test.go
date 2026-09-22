package binmanager

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

// Each stubbed binary is a script that calls the next one by name, so the fixture can also prove
// the chain resolves through PATH when it is actually executed.
var dependencyPathScripts = map[string]string{
	"root": "#!/bin/sh\nexec a\n",
	"a":    "#!/bin/sh\nexec b\n",
	"b":    "#!/bin/sh\necho managed-b\n",
}

// dependencyPathFixture returns root -> {a, sh, runtime} and a -> {b}, where root, a and b are
// binary apps stubbed on disk, sh is a shell app and runtime a runtime-managed one. cacheDir ""
// means a fresh temporary directory.
func dependencyPathFixture(t *testing.T, cacheDir string) (*BinManager, map[string]string) {
	t.Helper()
	if cacheDir == "" {
		cacheDir = t.TempDir()
	}
	t.Setenv("DATAMITSU_CACHE_DIR", cacheDir)
	t.Setenv("DATAMITSU_OFFLINE", "1")
	apps := binaryAppFixture(t, "root")
	binary := apps["root"].Binary
	apps["a"] = App{Binary: binary, DependsOn: []string{"b"}}
	apps["b"] = App{Binary: binary}
	apps["sh"] = App{Shell: &AppConfigShell{Name: "sh"}}
	apps["runtime"] = App{Uv: &AppConfigUV{}}
	apps["root"] = App{Binary: binary, DependsOn: []string{"a", "sh", "runtime"}}
	runtimeTool := filepath.Join(t.TempDir(), "runtime-tool")
	if err := os.WriteFile(runtimeTool, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	mock := &mockRuntimeAppManager{
		resolveCommandInfoFunc: func(string, App) (*CommandInfo, error) { return &CommandInfo{Command: runtimeTool}, nil },
		getCommandInfoFunc: func(context.Context, string, App) (*CommandInfo, error) {
			return &CommandInfo{Command: runtimeTool}, nil
		},
	}
	bm := New(apps, nil, mock)
	paths := map[string]string{}
	for name, script := range dependencyPathScripts {
		path, err := bm.getBinaryPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}
	return bm, paths
}

func mustDependencyPathDir(t *testing.T, links ...dependencyLink) string {
	t.Helper()
	dir, err := dependencyPathDir(links)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func pathHead(t *testing.T, info *CommandInfo) string {
	t.Helper()
	head, _, _ := strings.Cut(info.Env["PATH"], string(os.PathListSeparator))
	if head == "" {
		t.Fatalf("no PATH in env %v", info.Env)
	}
	return head
}

func TestDependencyPathExposesTheBinaryClosureByName(t *testing.T) {
	bm, paths := dependencyPathFixture(t, "")
	info, err := bm.GetCommandInfo(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	dir := pathHead(t, info)
	if !strings.HasPrefix(dir, env.GetDependencyPathRoot()) {
		t.Fatalf("PATH head %q is not under the store", dir)
	}
	if stat, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && stat.Mode().Perm() != 0o755 {
		t.Fatalf("directory mode %v, want the store's 0755", stat.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	// `b` is reached through `a`, which runs straight from the store when called by name, so
	// it has to be on the root's PATH too. Shell and runtime dependencies cannot be linked.
	if want := []string{commandName("a"), commandName("b")}; !slices.Equal(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for _, name := range []string{"a", "b"} {
		target, err := filepath.EvalSymlinks(filepath.Join(dir, commandName(name)))
		if err != nil {
			t.Fatal(err)
		}
		want, err := filepath.EvalSymlinks(paths[name])
		if err != nil {
			t.Fatal(err)
		}
		if target != want {
			t.Fatalf("%s -> %s, want %s", name, target, want)
		}
	}
	if rest := strings.TrimPrefix(info.Env["PATH"], dir+string(os.PathListSeparator)); rest != os.Getenv("PATH") {
		t.Fatalf("inherited PATH not kept after the prefix: %q", info.Env["PATH"])
	}
}

func TestDependencyPathRunsTheHelperChainAheadOfSystemCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub binaries are POSIX shell scripts")
	}
	bm, _ := dependencyPathFixture(t, "")
	// A system `b` earlier on the inherited PATH must not win over the managed one.
	system := t.TempDir()
	if err := os.WriteFile(filepath.Join(system, "b"), []byte("#!/bin/sh\necho system-b\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", system+string(os.PathListSeparator)+os.Getenv("PATH"))

	cmd, err := bm.GetExecCmd(context.Background(), "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("root -> a -> b failed: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "managed-b" {
		t.Fatalf("chain reached %q, want the managed b", got)
	}
}

func TestDependencyPathResolutionNeverWrites(t *testing.T) {
	bm, _ := dependencyPathFixture(t, "")
	info, installed, err := bm.ResolveCommandInfo("root")
	if err != nil {
		t.Fatal(err)
	}
	dir := pathHead(t, info)
	if info.Env["PATH"] != dir {
		t.Fatalf("resolution must record only the prefix, got %q", info.Env["PATH"])
	}
	if _, err := os.Stat(env.GetDependencyPathRoot()); !os.IsNotExist(err) {
		t.Fatalf("read-only resolution created the dependency directory: %v", err)
	}
	// Missing, so the source-mode shim repairs it through an install instead of running a tool
	// that cannot find its helpers.
	if installed {
		t.Fatal("app reported installed without its dependency PATH directory")
	}
	if !slices.Contains(info.RequiredPaths, filepath.Join(dir, commandName("b"))) {
		t.Fatalf("health paths %v lack the PATH entries", info.RequiredPaths)
	}
	if _, err := bm.GetCommandInfo(context.Background(), "root"); err != nil {
		t.Fatal(err)
	}
	again, installed, err := bm.ResolveCommandInfo("root")
	if err != nil || !installed {
		t.Fatalf("after exec: installed=%v err=%v", installed, err)
	}
	if pathHead(t, again) != dir {
		t.Fatal("resolution and exec disagree on the directory")
	}
}

func TestDependencyPathRepairsInPlaceWithoutReplacingTheDirectory(t *testing.T) {
	bm, _ := dependencyPathFixture(t, "")
	info, err := bm.GetCommandInfo(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	dir := pathHead(t, info)
	// A running tool holds this directory on its PATH; a repair that replaced it whole would pull
	// the surviving entries out from under it. The marker shows the directory itself survived.
	marker := filepath.Join(dir, "marker")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, commandName("a"))); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.GetCommandInfo(context.Background(), "root"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, commandName("a"))); err != nil {
		t.Fatalf("entry not restored: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("repair replaced the directory: %v", err)
	}
}

func TestDependencyPathLinksResolveUnderARelativeStore(t *testing.T) {
	t.Chdir(t.TempDir())
	bm, _ := dependencyPathFixture(t, "cache")
	info, err := bm.GetCommandInfo(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	dir := pathHead(t, info)
	if !filepath.IsAbs(dir) {
		t.Fatalf("PATH entry %q is relative", dir)
	}
	// A relative target would resolve against the link's own directory and dangle.
	if _, err := os.Stat(filepath.Join(dir, commandName("b"))); err != nil {
		t.Fatalf("link dangles: %v", err)
	}
}

func TestComposeDependencyPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	join := func(parts ...string) string { return strings.Join(parts, sep) }
	for _, tt := range []struct {
		name, runtimePath, inherited, want string
		set                                bool
	}{
		{name: "no runtime PATH, exec", inherited: "/usr/bin", want: join("/dep", "/usr/bin")},
		{name: "no runtime PATH, resolve", want: "/dep"},
		// A dependency named `node` must not displace the runtime's pinned interpreter.
		{name: "runtime prefix, exec", runtimePath: join("/rt", "/usr/bin"), set: true, inherited: "/usr/bin", want: join("/rt", "/dep", "/usr/bin")},
		{name: "runtime prefix, resolve", runtimePath: "/rt", set: true, want: join("/rt", "/dep")},
		{name: "runtime added nothing", runtimePath: "/usr/bin", set: true, inherited: "/usr/bin", want: join("/dep", "/usr/bin")},
		{name: "runtime composed otherwise", runtimePath: join("/rt", "/other"), set: true, inherited: "/usr/bin", want: join("/rt", "/other", "/dep")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := composeDependencyPath(tt.runtimePath, tt.set, "/dep", tt.inherited); got != tt.want {
				t.Fatalf("PATH = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDependencyPathIsAbsentWithoutBinaryDependencies(t *testing.T) {
	bm, _ := dependencyPathFixture(t, "")
	for _, name := range []string{"b", "sh"} {
		info, err := bm.GetCommandInfo(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		if _, set := info.Env["PATH"]; set {
			t.Fatalf("%s got a PATH with no binary dependency: %q", name, info.Env["PATH"])
		}
	}
}

func TestDependencyPathDirectoryIsContentAddressed(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	one := mustDependencyPathDir(t, dependencyLink{name: "tool", target: "/store/.bin/tool/v1"})
	two := mustDependencyPathDir(t, dependencyLink{name: "tool", target: "/store/.bin/tool/v2"})
	renamed := mustDependencyPathDir(t, dependencyLink{name: "other", target: "/store/.bin/tool/v1"})
	if one == two || one == renamed {
		t.Fatalf("directories collide: %s %s %s", one, two, renamed)
	}
	if one != mustDependencyPathDir(t, dependencyLink{name: "tool", target: "/store/.bin/tool/v1"}) {
		t.Fatal("directory name is not deterministic")
	}
}

func TestCommandNameAddsTheWindowsExtensionOnce(t *testing.T) {
	want := map[string]string{"tool": "tool", "tool.exe": "tool.exe"}
	if runtime.GOOS == "windows" {
		want = map[string]string{"tool": "tool.exe", "tool.exe": "tool.exe", "TOOL.EXE": "TOOL.EXE"}
	}
	for app, name := range want {
		if got := commandName(app); got != name {
			t.Fatalf("commandName(%q) = %q, want %q", app, got, name)
		}
	}
}
