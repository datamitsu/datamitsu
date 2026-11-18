package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestConfigContentBridgeReentrantDeadline(t *testing.T) {
	p := clitest.NewProject(t)
	base := p.WriteFile("base.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({
  version: "base", apps: {}, runtimes: {}, tools: {},
  managedConfigs: { "child.json": { content: function(ctx) {
    if (ctx.again) { try { ctx.again({}); } catch (_) {} }
    while (true) {}
  } } }
});`)
	consumer := p.WriteFile("consumer.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = input => {
  const content = input.managedConfigs["child.json"].content;
  try { content({ again: content }); } catch (_) {}
  return input;
};`)
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Timeout: 25 * time.Second},
		"--no-auto-config", "--before-config", base, "--config", consumer, "config", "show")
	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "execution timeout") {
		t.Fatalf("reentrant callback escaped deadline: exit %d\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
}

func TestConfigContentBridgeReceiver(t *testing.T) {
	p := clitest.NewProject(t)
	base := p.WriteFile("base.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({
  version: "base", apps: {}, runtimes: {}, tools: {},
  managedConfigs: { "child.json": { content: function() { "use strict"; return this.value; } } }
});`)
	consumer := p.WriteFile("consumer.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = input => merge(input, {
  version: input.managedConfigs["child.json"].content.call({ value: "receiver-preserved" }, {})
});`)
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "--no-auto-config", "--before-config", base,
		"--config", consumer, "config", "show")
	if res.ExitCode != 0 {
		t.Fatalf("receiver callback: exit %d\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	var shown struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Version != "receiver-preserved" {
		t.Fatalf("callback receiver result = %q", shown.Version)
	}
}

func TestConfigContentBridgeRejectsNonCallableContent(t *testing.T) {
	p := clitest.NewProject(t)
	base := p.WriteFile("base.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({
  version: "base", apps: {}, runtimes: {}, tools: {},
  managedConfigs: { "child.json": { content: 42 } }
});`)
	consumer := p.WriteFile("consumer.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = input => ({ ...input });`)
	p.WriteFile("child.json", `{"keep":"original"}`)
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "--no-auto-config", "--before-config", base,
		"--config", consumer, "config", "reconcile", "--skip-fix")
	if res.ExitCode == 0 || !strings.Contains(res.Stdout+res.Stderr, "content is not a callable function") {
		t.Fatalf("invalid callback accepted: exit %d\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	data, err := os.ReadFile(filepath.Join(p.Dir, "child.json"))
	if err != nil || string(data) != `{"keep":"original"}` {
		t.Fatalf("invalid callback changed file: %q, %v", data, err)
	}
}
