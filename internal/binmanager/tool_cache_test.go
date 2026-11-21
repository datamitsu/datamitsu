package binmanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestRuntimeToolCacheLifecycle(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")
	root := t.TempDir()
	t.Chdir(root)
	apps := binaryAppFixture(t, "tool")
	app := apps["tool"]
	app.Caches = []string{"providers"}
	app.RuntimeEnv = map[string]string{"CACHE": "{toolCache:providers}/entry", "DEFAULT_CACHE": "{toolCache}/entry"}
	apps["tool"] = app
	bm := New(apps, nil, nil)
	bm.SetToolCacheRoot(root)
	before := NetworkDownloads()
	info, installed, err := bm.ResolveCommandInfo("tool")
	if err != nil {
		t.Fatal(err)
	}
	if installed || info.ToolCacheDir == "" {
		t.Fatalf("uninstalled app: installed=%v, cache=%q", installed, info.ToolCacheDir)
	}
	if _, err := os.Stat(info.ToolCacheDir); !os.IsNotExist(err) {
		t.Fatalf("read-only resolution created cache: %v", err)
	}
	if NetworkDownloads() != before {
		t.Fatal("read-only resolution downloaded an app")
	}
	if err := os.MkdirAll(filepath.Dir(info.Command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(info.Command, []byte("installed artifact"), 0o755); err != nil {
		t.Fatal(err)
	}
	prepared, err := bm.GetCommandInfo(context.Background(), "tool")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"CACHE", "DEFAULT_CACHE"} {
		if err := os.WriteFile(prepared.Env[key], []byte(key), 0o600); err != nil {
			t.Fatalf("app cannot write to cache %s: %v", key, err)
		}
	}
	providerSlot := filepath.Dir(prepared.Env["CACHE"])
	defaultSlot := filepath.Dir(prepared.Env["DEFAULT_CACHE"])
	if providerSlot == defaultSlot || filepath.Dir(providerSlot) != filepath.Dir(defaultSlot) {
		t.Fatalf("named and default caches are not distinct siblings: %q %q", providerSlot, defaultSlot)
	}
	for _, key := range []string{"CACHE", "DEFAULT_CACHE"} {
		if content, err := os.ReadFile(prepared.Env[key]); err != nil || string(content) != key {
			t.Fatalf("cache slot %s write was not isolated: %q, %v", key, content, err)
		}
	}
	if err := os.RemoveAll(prepared.ToolCacheDir); err != nil {
		t.Fatal(err)
	}
	_, installed, err = bm.ResolveCommandInfo("tool")
	if err != nil || !installed {
		t.Fatalf("cache removal invalidated the installed app: installed=%v, error=%v", installed, err)
	}
	if _, err := os.Stat(prepared.ToolCacheDir); !os.IsNotExist(err) {
		t.Fatalf("read-only resolution recreated cache: %v", err)
	}
	prepared, err = bm.GetCommandInfo(context.Background(), "tool")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prepared.Env["CACHE"], []byte("restored data"), 0o600); err != nil {
		t.Fatalf("cache was not recreated: %v", err)
	}
	if err := os.WriteFile(prepared.Env["DEFAULT_CACHE"], []byte("restored default"), 0o600); err != nil {
		t.Fatalf("default cache was not recreated: %v", err)
	}
	if NetworkDownloads() != before {
		t.Fatal("cache recreation reinstalled the app")
	}
	artifact, err := os.ReadFile(info.Command)
	if err != nil || string(artifact) != "installed artifact" {
		t.Fatalf("cache recreation changed installed artifact: %q, %v", artifact, err)
	}
	child := filepath.Join(root, "nested")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)
	fromChild, _, err := bm.ResolveCommandInfo("tool")
	if err != nil || fromChild.ToolCacheDir != prepared.ToolCacheDir {
		t.Fatalf("subdirectory selected a different cache: %+v, %v", fromChild, err)
	}
	otherRoot := t.TempDir()
	t.Chdir(otherRoot)
	bm.SetToolCacheRoot(otherRoot)
	other, _, err := bm.ResolveCommandInfo("tool")
	if err != nil || other.ToolCacheDir == prepared.ToolCacheDir {
		t.Fatalf("different checkouts share a cache: %+v, %v", other, err)
	}
	t.Chdir(root)
	bm.SetToolCacheRoot(root)
	app.Caches = []string{"providers", "another"}
	apps["tool"] = app
	reconfigured, _, err := bm.ResolveCommandInfo("tool")
	if err != nil || reconfigured.Command != prepared.Command || reconfigured.ToolCacheDir != prepared.ToolCacheDir {
		t.Fatalf("directory declaration changed app identity: %+v, %v", reconfigured, err)
	}
	if _, err := os.Stat(filepath.Join(prepared.ToolCacheDir, "another")); !os.IsNotExist(err) {
		t.Fatalf("read-only resolution created a named cache: %v", err)
	}
	if _, err := bm.GetCommandInfo(context.Background(), "tool"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepared.ToolCacheDir, "another", "state"), []byte("ready"), 0o600); err != nil {
		t.Fatalf("execution did not create the newly declared cache slot: %v", err)
	}
	for _, byArch := range app.Binary.Binaries {
		for _, byLibc := range byArch {
			for libc, binary := range byLibc {
				binary.URL += "?version=2"
				byLibc[libc] = binary
			}
		}
	}
	upgraded, _, err := bm.ResolveCommandInfo("tool")
	if err != nil || upgraded.ToolCacheDir == prepared.ToolCacheDir {
		t.Fatalf("new binary identity reused old cache: %+v, %v", upgraded, err)
	}
	if _, err := os.Stat(upgraded.ToolCacheDir); !os.IsNotExist(err) {
		t.Fatalf("read-only upgrade resolution created cache: %v", err)
	}
	if data, err := os.ReadFile(prepared.Env["CACHE"]); err != nil || string(data) != "restored data" {
		t.Fatalf("upgrade changed previous cache: %q, %v", data, err)
	}
}

func TestRuntimeToolCachePathsRemainLiteralDuringExpansion(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), "${APP_BIN:literal}-${APP_DIR}-${STORE}-{toolCache:providers}")
	t.Setenv("DATAMITSU_CACHE_DIR", cacheRoot)
	t.Setenv("DATAMITSU_OFFLINE", "1")
	root := t.TempDir()
	t.Chdir(root)
	apps := binaryAppFixture(t, "tool")
	app := apps["tool"]
	app.Caches = []string{"providers"}
	app.RuntimeEnv = map[string]string{
		"VALUE":   "${APP_DIR}|{toolCache:providers}|${STORE}|{toolCache}|{toolCacheEnabled}|${toolCacheEnabled}",
		"ESCAPED": `${value#\{toolCache}{toolCache}`,
		"QUOTED":  `${value#'{'}|${value#'{toolCache:providers}'}|${value#'${'}|${value#'${STORE}'}{toolCache}`,
	}
	apps["tool"] = app
	bm := New(apps, nil, nil)
	bm.SetToolCacheRoot(root)
	info, _, err := bm.ResolveCommandInfo("tool")
	if err != nil {
		t.Fatal(err)
	}
	appDir, err := bm.ComputeInstallPath("tool")
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.Join([]string{
		appDir,
		filepath.Join(info.ToolCacheDir, "providers"),
		env.GetStorePath(),
		filepath.Join(info.ToolCacheDir, "default"),
		"{toolCacheEnabled}",
		"${toolCacheEnabled}",
	}, "|")
	if got := info.Env["VALUE"]; got != expected {
		t.Fatalf("Env[VALUE] = %q, want %q", got, expected)
	}
	if got, want := info.Env["ESCAPED"], `${value#\{toolCache}`+filepath.Join(info.ToolCacheDir, "default"); got != want {
		t.Fatalf("Env[ESCAPED] = %q, want %q", got, want)
	}
	if got, want := info.Env["QUOTED"], `${value#'{'}|${value#'{toolCache:providers}'}|${value#'${'}|${value#'${STORE}'}`+filepath.Join(info.ToolCacheDir, "default"); got != want {
		t.Fatalf("Env[QUOTED] = %q, want %q", got, want)
	}
	if len(info.ToolCacheSlots) != 2 || info.ToolCacheSlots[0] != "providers" || info.ToolCacheSlots[1] != "default" {
		t.Fatalf("ToolCacheSlots = %v, want [providers default]", info.ToolCacheSlots)
	}
	if !strings.Contains(info.ToolCacheDir, "${APP_BIN:literal}-${APP_DIR}-${STORE}-{toolCache:providers}") {
		t.Fatalf("ToolCacheDir lost literal cache-root text: %q", info.ToolCacheDir)
	}
}

func TestRuntimeToolCacheCreationFailure(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")
	t.Chdir(t.TempDir())
	apps := binaryAppFixture(t, "tool")
	app := apps["tool"]
	app.RuntimeEnv = map[string]string{"CACHE": "{toolCache}"}
	apps["tool"] = app
	bm := New(apps, nil, nil)
	info, _, err := bm.ResolveCommandInfo("tool")
	if err != nil {
		t.Fatal(err)
	}
	if expected := filepath.Join(info.ToolCacheDir, "default"); info.Env["CACHE"] != expected {
		t.Fatalf("default cache env = %q, want cache directory %q", info.Env["CACHE"], expected)
	}
	if err := os.MkdirAll(filepath.Dir(info.Command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(info.Command, []byte("installed artifact"), 0o755); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Dir(info.ToolCacheDir)
	if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.GetCommandInfo(context.Background(), "tool"); err == nil {
		t.Fatal("execution preparation ignored an unwritable cache path")
	}
}

func TestRuntimeToolCacheRejectsInvalidDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		caches []string
		value  string
	}{
		{name: "reserved declaration", caches: []string{"default"}, value: "{toolCache}"},
		{name: "unknown reference", caches: []string{"providers"}, value: "{toolCache:other}"},
		{name: "malformed reference", caches: []string{"providers"}, value: "{toolCache:}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cacheRoot := t.TempDir()
			t.Setenv("DATAMITSU_CACHE_DIR", cacheRoot)
			t.Setenv("DATAMITSU_OFFLINE", "1")
			root := t.TempDir()
			t.Chdir(root)
			apps := binaryAppFixture(t, "tool")
			app := apps["tool"]
			app.Caches = tc.caches
			app.RuntimeEnv = map[string]string{"CACHE": tc.value}
			apps["tool"] = app
			bm := New(apps, nil, nil)
			bm.SetToolCacheRoot(root)
			if _, _, err := bm.ResolveCommandInfo("tool"); err == nil {
				t.Fatal("invalid cache declaration or reference was accepted")
			}
			entries, err := os.ReadDir(cacheRoot)
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid cache input changed cache root: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestDeclaredCacheWithoutRuntimeEnvironment(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")
	t.Chdir(t.TempDir())
	apps := binaryAppFixture(t, "tool")
	app := apps["tool"]
	app.Caches = []string{"private"}
	apps["tool"] = app
	bm := New(apps, nil, nil)
	info, _, err := bm.ResolveCommandInfo("tool")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.ToolCacheDir); !os.IsNotExist(err) {
		t.Fatalf("read-only resolution created a cache: %v", err)
	}
	info.ToolCacheSlots[0] = "changed-by-caller"
	if err := os.MkdirAll(filepath.Dir(info.Command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(info.Command, []byte("installed artifact"), 0o755); err != nil {
		t.Fatal(err)
	}
	ready, err := bm.GetCommandInfo(context.Background(), "tool")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ready.ToolCacheDir, "private", "entry"), []byte("ready"), 0o600); err != nil {
		t.Fatalf("cache declaration did not prepare writable state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ready.ToolCacheDir, "changed-by-caller")); !os.IsNotExist(err) {
		t.Fatalf("caller mutation changed the app declaration: %v", err)
	}
}
