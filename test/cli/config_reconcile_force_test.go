package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestConfigReconcileForce(t *testing.T) {
	p := clitest.NewProject(t)
	base := p.WriteFile("base.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({
  apps: { shell: { shell: { name: "sh" } } }, runtimes: {},
  projectTypes: { sample: { markers: ["**/fixture.marker"] } },
  tools: {
    first: { operations: { lint: { app: "shell", args: [], scope: "repository" } } },
    second: { operations: { lint: { app: "shell", args: [], scope: "repository" } } }
  },
  managedConfigs: {
    "root.json": { scope: "git-root", tools: ["first"], content: ctx => JSON.stringify(merge({ value: "default" }, JSON.parse(ctx.originalContent || "{}"))) },
    "child.json": { scope: "project", projectTypes: ["sample"], tools: ["first"], content: ctx => JSON.stringify(merge({ value: "default" }, JSON.parse(ctx.originalContent || "{}"))) },
    "other.json": { scope: "git-root", tools: ["second"], content: () => "new" }
  }
});`)
	project := p.WriteFile("project.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = base => ({ ...base, managedConfigs: { ...base.managedConfigs,
  "root.json": { ...base.managedConfigs["root.json"], content: ctx => JSON.stringify(merge(JSON.parse(ctx.existingContent || "{}"), { layer: "project" })) },
  "child.json": { ...base.managedConfigs["child.json"], content: ctx => JSON.stringify(merge(JSON.parse(ctx.existingContent || "{}"), { layer: "project" })) }
}});`)
	p.WriteFile("nested/fixture.marker", "")
	p.WriteFile("root.json", `{"value":"custom"}`)
	p.WriteFile("nested/child.json", `{"value":"custom"}`)
	p.WriteFile("other.json", "keep")
	args := []string{"--no-auto-config", "--before-config", base, "--config", project, "config", "reconcile", "--skip-fix", "--tools", "first"}
	run := func(extra ...string) {
		t.Helper()
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, append(args, extra...)...)
		if res.ExitCode != 0 {
			t.Fatalf("reconcile: %d\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
		}
	}
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(p.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	run()
	if !strings.Contains(read("root.json"), `"value":"custom"`) {
		t.Fatal("normal reconcile lost custom settings")
	}
	before := read("root.json")
	run("--force", "--dry-run")
	if read("root.json") != before {
		t.Fatal("forced dry run wrote a file")
	}
	run("--force")
	if read("root.json") != `{"value":"default","layer":"project"}` {
		t.Fatalf("root: %s", read("root.json"))
	}
	if read("nested/child.json") != `{"value":"default","layer":"project"}` {
		t.Fatalf("child: %s", read("nested/child.json"))
	}
	if read("other.json") != "keep" {
		t.Fatal("force escaped tool selection")
	}
}

func TestConfigReconcileForceKeepsChainHashGate(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("before.config.js", chainDriftBeforeConfigJS)
	p.WriteFile("datamitsu.config.js", chainDriftRootConfigJS)
	p.WriteFile("drift.txt", "keep customized content")
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "config", "reconcile", "--force", "--skip-fix")
	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "chain-hash verification failed") {
		t.Fatalf("force bypassed pin: %d, %s", res.ExitCode, res.Stderr)
	}
	data, err := os.ReadFile(filepath.Join(p.Dir, "drift.txt"))
	if err != nil || string(data) != "keep customized content" {
		t.Fatalf("failed verification changed the file: %q, %v", data, err)
	}
}

func TestForcedReconcileReplaysFinalOwnerAndSourceVMs(t *testing.T) {
	p := clitest.NewProject(t)
	base := p.WriteFile("base.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({ apps: { shell: { shell: { name: "sh" } } }, runtimes: {},
  bundles: { base: { files: { "base.txt": "base" }, links: { "base.txt": "base.txt" } } },
  tools: { first: { operations: { lint: { app: "shell", args: [], scope: "repository" } } },
           second: { operations: { lint: { app: "shell", args: [], scope: "repository" } } } },
  managedConfigs: { "root.json": { scope: "git-root", tools: ["first"], content: ctx =>
    JSON.stringify(merge({ value: "default", link: tools.Config.linkPath("base", "base.txt", ctx.cwdPath) }, JSON.parse(ctx.originalContent || "{}"))) } }
});`)
	project := p.WriteFile("project.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = base => ({ ...base, managedConfigs: { "root.json": {
  ...base.managedConfigs["root.json"], tools: ["second"], content: ctx =>
    JSON.stringify(merge(JSON.parse(ctx.existingContent || "{}"), { layer: "project" }))
}}});`)
	p.WriteFile("root.json", `{"value":"custom"}`)
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "--no-auto-config", "--before-config", base, "--config", project,
		"config", "reconcile", "--force", "--tools", "second", "--skip-fix")
	if res.ExitCode != 0 {
		t.Fatalf("forced source replay: %d\n%s", res.ExitCode, res.Stderr)
	}
	data, err := os.ReadFile(filepath.Join(p.Dir, "root.json"))
	if err != nil || !strings.Contains(string(data), `"value":"default"`) || !strings.Contains(string(data), `"layer":"project"`) || !strings.Contains(string(data), "base.txt") {
		t.Fatalf("lost layer output: %q, %v", data, err)
	}
}

func TestForcedReconcileAcceptsValidPin(t *testing.T) {
	p := clitest.NewProject(t)
	base := p.WriteFile("base.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({ apps: {}, runtimes: {}, tools: {},
  managedConfigs: { "root.json": { scope: "git-root", content: ctx => JSON.stringify(merge({value:"default"}, JSON.parse(ctx.originalContent || "{}"))) } }
});`)
	projectText := `globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = base => ({ ...base, managedConfigs: { "root.json": {
 ...base.managedConfigs["root.json"], content: ctx => ctx.existingContent __PIN__
}}});`
	project := p.WriteFile("project.config.js", strings.ReplaceAll(projectText, "__PIN__", ""))
	p.WriteFile("root.json", `{"value":"custom"}`)
	args := []string{"--no-auto-config", "--before-config", base, "--config", project}
	result := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, append(args, "config", "chain-hash", "root.json")...)
	pin := regexp.MustCompile(`xxh3:[0-9a-f]{32}`).FindString(result.Stdout)
	if result.ExitCode != 0 || pin == "" {
		t.Fatalf("pin: %d, %s, %s", result.ExitCode, result.Stdout, result.Stderr)
	}
	p.WriteFile("project.config.js", strings.ReplaceAll(projectText, "__PIN__", `, expectChainHash: "`+pin+`"`))
	result = clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, append(args, "config", "reconcile", "--force", "--skip-fix")...)
	if result.ExitCode != 0 {
		t.Fatalf("valid pin refused: %s", result.Stderr)
	}
	data, err := os.ReadFile(filepath.Join(p.Dir, "root.json"))
	if err != nil || string(data) != `{"value":"default"}` {
		t.Fatalf("forced result: %q, %v", data, err)
	}
}
