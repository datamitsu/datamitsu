package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestConfigReconcileInheritedContent(t *testing.T) {
	cases := []struct {
		name  string
		layer string
	}{
		{name: "deep merge", layer: `return merge(input, { version: "project" });`},
		{name: "entry spread", layer: `return { ...input, managedConfigs: { ...input.managedConfigs, "child.json": { ...input.managedConfigs["child.json"] } } };`},
		{name: "outer spread control", layer: `return { ...input };`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := clitest.NewProject(t)
			base := p.WriteFile("base.config.js", `
globalThis.getMinVersion = () => "0.0.0";
const baseValue = "base";
globalThis.getConfig = () => ({
  version: "1.0.0", apps: {}, runtimes: {}, tools: {},
  projectTypes: { sample: { markers: ["**/fixture.marker"] } },
  managedConfigs: {
    "child.json": { scope: "project", projectTypes: ["sample"], content: ctx => JSON.stringify({ value: baseValue, cwd: ctx.cwdPath, keep: JSON.parse(ctx.originalContent || "{}").keep }) },
    "root.json": { scope: "git-root", content: ctx => JSON.stringify({ value: baseValue, cwd: ctx.cwdPath }) }
  }
});`)
			project := p.WriteFile("project.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = input => { `+tc.layer+` };`)
			for _, dir := range []string{"", "nested", "other"} {
				p.WriteFile(filepath.Join(dir, "fixture.marker"), "")
				keep := strings.ReplaceAll(dir, "/", "-")
				if keep == "" {
					keep = "root"
				}
				p.WriteFile(filepath.Join(dir, "child.json"), `{"keep":"`+keep+`"}`)
			}
			p.WriteFile("root.json", `{"keep":"root"}`)
			args := []string{"--no-auto-config", "--before-config", base, "--config", project}
			reconcileArgs := append(append([]string{}, args...), "config", "reconcile", "--skip-fix")
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, reconcileArgs...)
			if res.ExitCode != 0 {
				t.Fatalf("reconcile: %d\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
			}
			show := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, append(append([]string{}, args...), "config", "show")...)
			if show.ExitCode != 0 {
				t.Fatalf("config show: %d\n%s", show.ExitCode, show.Stderr)
			}
			var shown map[string]any
			if err := json.Unmarshal([]byte(show.Stdout), &shown); err != nil {
				t.Fatalf("config show JSON: %v", err)
			}
			managed, ok := shown["managedConfigs"].(map[string]any)
			if !ok {
				t.Fatalf("managedConfigs missing from config show: %#v", shown)
			}
			for name, raw := range managed {
				entry, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("managed config %s: %#v", name, raw)
				}
				if _, ok := entry["content"]; ok {
					t.Errorf("config show exposed %s content", name)
				}
				if _, ok := entry["render"]; ok {
					t.Errorf("config show exposed %s render", name)
				}
			}
			for _, name := range []string{"nested/root.json", "other/root.json"} {
				if _, err := os.Stat(filepath.Join(p.Dir, name)); !os.IsNotExist(err) {
					t.Errorf("unexpected root-scoped output %s (stat error %v)", name, err)
				}
			}
			for _, dir := range []string{"", "nested", "other"} {
				name := filepath.Join(dir, "child.json")
				b, err := os.ReadFile(filepath.Join(p.Dir, name))
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]string
				if err := json.Unmarshal(b, &got); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				wantKeep := strings.ReplaceAll(dir, "/", "-")
				if wantKeep == "" {
					wantKeep = "root"
				}
				if got["value"] != "base" || got["keep"] != wantKeep || got["cwd"] != filepath.Join(p.Dir, dir) {
					t.Fatalf("%s: %#v", name, got)
				}
			}
			rootData, err := os.ReadFile(filepath.Join(p.Dir, "root.json"))
			if err != nil {
				t.Fatalf("root output missing: %v", err)
			}
			var rootGot map[string]string
			if err := json.Unmarshal(rootData, &rootGot); err != nil {
				t.Fatalf("root output JSON: %v", err)
			}
			if rootGot["value"] != "base" || rootGot["cwd"] != p.Dir {
				t.Fatalf("root output: %#v", rootGot)
			}
		})
	}
}

func TestConfigReconcileInheritedContentDeadline(t *testing.T) {
	p := clitest.NewProject(t)
	base := p.WriteFile("base.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({
  version: "1.0.0", apps: {}, runtimes: {}, tools: {},
  managedConfigs: {
    "child.json": { scope: "project", content: () => { while (true) {} } }
  }
});`)
	first := p.WriteFile("first.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = input => merge(input, { version: "first" });`)
	second := p.WriteFile("second.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = input => merge(input, { version: "second" });`)
	consumer := p.WriteFile("consumer.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = input => { input.managedConfigs["child.json"].content({}); return input; };`)
	args := []string{"--no-auto-config", "--before-config", base, "--before-config", first, "--before-config", second, "--config", consumer, "config", "show"}
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Timeout: 25 * time.Second}, args...)
	if res.ExitCode == 0 {
		t.Fatalf("config show unexpectedly succeeded with non-terminating inherited callback")
	}
	if !strings.Contains(res.Stderr+res.Stdout, "execution timeout") {
		t.Fatalf("config show did not report callback deadline: exit %d\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
}

func TestConfigReconcileConsumingLayerContentException(t *testing.T) {
	p := clitest.NewProject(t)
	base := p.WriteFile("base.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({
  version: "1.0.0", apps: {}, runtimes: {}, tools: {},
  projectTypes: { sample: { markers: ["**/fixture.marker"] } },
  managedConfigs: {
    "child.json": { scope: "project", projectTypes: ["sample"], content: () => { throw new Error("inherited-content-failure"); } }
  }
});`)
	project := p.WriteFile("project.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = input => merge(input, { version: "project" });`)
	p.WriteFile("fixture.marker", "")
	p.WriteFile("child.json", `{"keep":"original"}`)
	args := []string{"--no-auto-config", "--before-config", base, "--config", project, "config", "reconcile", "--skip-fix"}
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Timeout: 20 * time.Second}, args...)
	if res.ExitCode == 0 || !strings.Contains(res.Stderr+res.Stdout, "inherited-content-failure") {
		t.Fatalf("reconcile did not surface callback exception: exit %d\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
}

func TestConfigReconcileInheritedContentOverrides(t *testing.T) {
	p := clitest.NewProject(t)
	base := p.WriteFile("base.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({
  version: "1.0.0", apps: {}, runtimes: {}, tools: {},
  projectTypes: { sample: { markers: ["**/fixture.marker"] } },
  managedConfigs: {
    "override.json": { scope: "project", projectTypes: ["sample"], content: ctx => JSON.stringify({ value: "base", cwd: ctx.cwdPath }) },
    "undefined-opt-out.json": { scope: "project", projectTypes: ["sample"], content: () => undefined },
    "null-opt-out.json": { scope: "project", projectTypes: ["sample"], content: () => null }
  }
});`)
	project := p.WriteFile("project.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = input => merge(input, { managedConfigs: {
  "override.json": { content: ctx => JSON.stringify({ value: "project", cwd: ctx.cwdPath }) }
} });`)
	for _, dir := range []string{"", "nested"} {
		p.WriteFile(filepath.Join(dir, "fixture.marker"), "")
		p.WriteFile(filepath.Join(dir, "override.json"), `{"value":"custom"}`)
		p.WriteFile(filepath.Join(dir, "undefined-opt-out.json"), `{"keep":"undefined-original"}`)
		p.WriteFile(filepath.Join(dir, "null-opt-out.json"), `{"keep":"null-original"}`)
	}
	args := []string{"--no-auto-config", "--before-config", base, "--config", project, "config", "reconcile", "--skip-fix"}
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, args...)
	if res.ExitCode != 0 {
		t.Fatalf("reconcile: %d\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	for _, dir := range []string{"", "nested"} {
		override, err := os.ReadFile(filepath.Join(p.Dir, dir, "override.json"))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]string
		if err := json.Unmarshal(override, &got); err != nil {
			t.Fatalf("override JSON: %v", err)
		}
		if got["value"] != "project" || got["cwd"] != filepath.Join(p.Dir, dir) {
			t.Errorf("override in %q: %#v", dir, got)
		}
		for name, original := range map[string]string{
			"undefined-opt-out.json": `{"keep":"undefined-original"}`,
			"null-opt-out.json":      `{"keep":"null-original"}`,
		} {
			optOut, err := os.ReadFile(filepath.Join(p.Dir, dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if string(optOut) != original {
				t.Errorf("opt-out %s in %q changed to %s", name, dir, optOut)
			}
		}
	}
}

func TestConfigReconcileForce(t *testing.T) {
	p := clitest.NewProject(t)
	base := p.WriteFile("base.config.js", `
globalThis.getMinVersion = () => "0.0.0";
globalThis.getConfig = () => ({
  version: "1.0.0",
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
globalThis.getConfig = () => ({ version: "1.0.0", apps: { shell: { shell: { name: "sh" } } }, runtimes: {},
  bundles: { base: { files: { "base.txt": "fixture" }, links: { "base.txt": "base.txt" } } },
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
globalThis.getConfig = () => ({ version: "1.0.0", apps: {}, runtimes: {}, tools: {},
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
