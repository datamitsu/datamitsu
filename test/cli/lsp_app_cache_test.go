package cli

import (
	"encoding/json"
	"fmt"
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

func TestLspAppToolCacheUsesWorkspaceRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable requires a POSIX shell")
	}
	workspace := clitest.NewProject(t)
	launch := clitest.NewBareDir(t)
	cacheHome := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cacheHome)
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	arch, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\nset -eu\nprintf 'formatted\\n' > \"$FORMATTER_CACHE/result\"\nIFS= read -r text < \"$FORMATTER_CACHE/result\"\nprintf '%s\\n' \"$text\" > \"$1\"\n"
	binary := binmanager.BinaryOsArchInfo{
		URL:         "https://example.invalid/cache-formatter",
		Hash:        digest.SHA256Of([]byte(body)).String(),
		ContentType: binmanager.BinContentTypeBinary,
	}
	apps := binmanager.MapOfApps{"formatter": {
		Binary: &binmanager.AppConfigBinary{Binaries: binmanager.MapOfBinaries{osType: {arch: {
			"unknown": binary,
			"glibc":   binary,
			"musl":    binary,
		}}}},
		RuntimeEnv: map[string]string{"FORMATTER_CACHE": "{toolCache}"},
	}}
	data, err := json.Marshal(apps)
	if err != nil {
		t.Fatal(err)
	}
	cfg := workspace.WriteFile("lsp-cache.config.js", `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  version: "1.0.0", apps: `+string(data)+`, runtimes: {}, managedConfigs: {},
  tools: {fixture: {name: "cache formatter", operations: {
    fix: {app: "formatter", args: ["{file}"], scope: "per-file", globs: ["**/*.txt"]}
  }}}
});
globalThis.getMinVersion = () => "0.0.0";
`)
	bm := binmanager.New(apps, nil, nil)
	bm.SetToolCacheRoot(workspace.Dir)
	info, _, err := bm.ResolveCommandInfo("formatter")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(info.Command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(info.Command, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	file := workspace.WriteFile("sample.txt", "before\n")
	uri := "file://" + file
	lsp := startLsp(t, launch.Dir, []string{"DATAMITSU_CACHE_DIR=" + cacheHome}, "--no-auto-config", "--config", cfg)
	lsp.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":%q}}`, "file://"+workspace.Dir))
	lsp.await("1")
	lsp.send(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	lsp.send(fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":%q,"languageId":"plaintext","version":1,"text":"before\n"}}}`, uri))
	lsp.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"textDocument/formatting","params":{"textDocument":{"uri":%q},"options":{"tabSize":2,"insertSpaces":true}}}`, uri))
	format := lsp.await("2")
	res := lsp.finish()
	if res.ExitCode != 0 {
		t.Fatalf("LSP failed: %s", res.Stderr)
	}
	var edits []json.RawMessage
	if err := json.Unmarshal(format["result"], &edits); err != nil {
		t.Fatalf("decode formatting result %s: %v", format["result"], err)
	}
	if len(edits) != 0 {
		t.Fatalf("clean buffer should reload the formatted file without edits: %s", format["result"])
	}
	if content, err := os.ReadFile(file); err != nil || string(content) != "formatted\n" {
		t.Fatalf("formatter did not update the workspace file: %q, %v; stderr: %s", content, err, res.Stderr)
	}
	if content, err := os.ReadFile(filepath.Join(info.ToolCacheDir, "default", "result")); err != nil || string(content) != "formatted\n" {
		t.Fatalf("formatter did not use the workspace cache: %q, %v", content, err)
	}
	launchCache, err := env.GetProjectCachePath(launch.Dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(launchCache); !os.IsNotExist(err) {
		t.Fatalf("worker created a launch-CWD cache: %v", err)
	}
	cleared := clitest.Run(t, clitest.RunOptions{Dir: workspace.Dir, CacheDir: cacheHome}, "cache", "clear")
	if cleared.ExitCode != 0 {
		t.Fatalf("cache clear failed: %s", cleared.Stderr)
	}
	if _, err := os.Stat(info.ToolCacheDir); !os.IsNotExist(err) {
		t.Fatalf("workspace cache clear retained formatter data: %v", err)
	}
	if _, err := os.Stat(info.Command); err != nil {
		t.Fatalf("cache clear removed the installed formatter: %v", err)
	}
}
