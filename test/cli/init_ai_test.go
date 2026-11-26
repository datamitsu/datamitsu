package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestInitAIPruneAndUnavailableBundle(t *testing.T) {
	p := clitest.NewProject(t)
	const source = `.codex/agents/dm-reviewer.toml`
	const configPrefix = `globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({ version: "test",`
	write := func(settings string) string {
		return p.WriteFile("ai.config.js", configPrefix+settings+"});")
	}
	run := func(settings string, wantSuccess bool) clitest.Result {
		t.Helper()
		res := clitest.Run(t, clitest.RunOptions{CacheDir: t.TempDir(), Env: nil, Stdin: "", Timeout: 0, Dir: p.Dir},
			"--no-auto-config", "--config", write(settings), "init", "--skip-download")
		if (res.ExitCode == 0) != wantSuccess {
			t.Fatalf("init exit = %d: %s\n%s", res.ExitCode, res.Stderr, res.Stdout)
		}
		return res
	}
	assertInstalled := func() {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(p.Dir, source))
		if err != nil || !strings.Contains(string(b), "Check changes.") {
			t.Fatalf("installed profile = %q, %v", b, err)
		}
	}
	run(`ai: {agents: {"dm-reviewer": {description: "Review", instructions: "Check changes.", targets: {codex: {}, antigravity: {}, opencode: {}}}}}`, true)
	for path, expected := range map[string]string{
		".agents/agents/dm-reviewer/agent.md": "Check changes.",
		".opencode/agents/dm-reviewer.md":     "mode: subagent",
	} {
		data, err := os.ReadFile(filepath.Join(p.Dir, path))
		if err != nil || !strings.Contains(string(data), expected) {
			t.Fatalf("%s = %q, %v; want %q", path, data, err, expected)
		}
	}
	assertInstalled()
	run(``, true)
	assertInstalled()
	res := run(`bundles: {assets: {archives: {data: {url: "https://example.invalid/archive.tar", hash: "sha256:0000000000000000000000000000000000000000000000000000000000000000", format: "tar"}}}},
		ai: {skills: {"dm-review": {source: {bundle: "assets", path: "skills/dm-review"}, targets: {agents: {}}}}}`, false)
	if !strings.Contains(res.Stderr+res.Stdout, "not installed") {
		t.Fatalf("missing bundle diagnostic: %s\n%s", res.Stderr, res.Stdout)
	}
	assertInstalled()
	run(`ai: {}`, true)
	if _, err := os.Lstat(filepath.Join(p.Dir, ".agents/agents/dm-reviewer")); !os.IsNotExist(err) {
		t.Fatalf("stale Antigravity directory still exists: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(p.Dir, source)); !os.IsNotExist(err) {
		t.Fatalf("stale profile still exists: %v", err)
	}
}
