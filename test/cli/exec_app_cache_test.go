package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

func TestExecDependencyToolCacheUsesCheckoutRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executables require a POSIX shell")
	}
	p := clitest.NewProject(t)
	cacheBase := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cacheBase)
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	arch, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{
		"wrapper":      "#!/bin/sh\nprintf 'processed\\n' > \"$1\"\n",
		"cache-helper": "#!/bin/sh\nexit 0\n",
	}
	apps := make(binmanager.MapOfApps, len(bodies))
	for name, body := range bodies {
		info := binmanager.BinaryOsArchInfo{
			URL:         "https://example.invalid/" + name,
			Hash:        digest.SHA256Of([]byte(body)).String(),
			ContentType: binmanager.BinContentTypeBinary,
		}
		apps[name] = binmanager.App{Binary: &binmanager.AppConfigBinary{
			Binaries: binmanager.MapOfBinaries{osType: {arch: {
				"unknown": info,
				"glibc":   info,
				"musl":    info,
			}}},
		}}
	}
	wrapper := apps["wrapper"]
	wrapper.DependsOn = edges("cache-helper")
	apps["wrapper"] = wrapper
	helper := apps["cache-helper"]
	helper.Caches = []string{"providers"}
	helper.RuntimeEnv = map[string]string{"CACHE_DIR": "{toolCache:providers}"}
	apps["cache-helper"] = helper
	data, err := json.Marshal(apps)
	if err != nil {
		t.Fatal(err)
	}
	cfg := p.WriteFile("cache.config.js", `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({version: "1.0.0", apps: `+string(data)+`, tools: {}, runtimes: {}});
globalThis.getMinVersion = () => "0.0.0";
`)
	bm := binmanager.New(apps, nil, nil)
	bm.SetToolCacheRoot(p.Dir)
	for name, body := range bodies {
		path, err := bm.ComputeInstallPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	info, _, err := bm.ResolveCommandInfo("cache-helper")
	if err != nil {
		t.Fatal(err)
	}
	var firstCache os.FileInfo
	for _, subdir := range []string{"first", "nested/second"} {
		dir := filepath.Join(p.Dir, subdir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(dir, "processed.txt")
		res := clitest.Run(t, clitest.RunOptions{Dir: dir, CacheDir: cacheBase},
			"--no-auto-config", "--config", cfg, "exec", "wrapper", "--", output)
		if res.ExitCode != 0 {
			t.Fatalf("exec from %s failed: %s", subdir, res.Stderr)
		}
		if content, err := os.ReadFile(output); err != nil || string(content) != "processed\n" {
			t.Fatalf("wrapper did not process its output: %q, %v", content, err)
		}
		currentCache, err := os.Stat(filepath.Join(info.ToolCacheDir, "providers"))
		if err != nil || !currentCache.IsDir() {
			t.Fatalf("dependency provider cache is not scoped to the checkout: %v", err)
		}
		if firstCache != nil && !os.SameFile(firstCache, currentCache) {
			t.Fatal("subdirectory invocation replaced the dependency cache")
		}
		firstCache = currentCache
		wrongNamespace, err := env.GetProjectCachePath(dir, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(wrongNamespace); !os.IsNotExist(err) {
			t.Fatalf("exec created a subdirectory-scoped cache: %s: %v", wrongNamespace, err)
		}
	}
	cleared := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheBase}, "cache", "clear")
	if cleared.ExitCode != 0 {
		t.Fatalf("cache clear failed: %s", cleared.Stderr)
	}
	if _, err := os.Stat(info.ToolCacheDir); !os.IsNotExist(err) {
		t.Fatalf("checkout cache clear retained the dependency cache: %v", err)
	}
	if _, err := os.Stat(info.Command); err != nil {
		t.Fatalf("cache clear removed the installed dependency: %v", err)
	}
}
