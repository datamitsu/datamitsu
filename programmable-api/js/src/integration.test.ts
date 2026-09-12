import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { after, describe, it, mock } from "node:test";

const BINARY_PATH = resolve(import.meta.dirname, "../../../datamitsu");

mock.module("@datamitsu/datamitsu/get-exe.js", {
  namedExports: {
    getExePath: () => BINARY_PATH,
  },
});

const { fix } = await import("./commands/fix.ts");
const { exec } = await import("./commands/exec.ts");
const { cache } = await import("./commands/cache.ts");
const { version } = await import("./commands/version.ts");

// The command tests run against a throwaway project instead of this checkout.
// This repository's config chain pins a wrapper config version, so every core
// schema change would fail them until the wrapper is republished — the same
// reason the loadConfig contract test moved to an isolated repository. Written
// as plain strings: a template literal would read "{file}" as a broken
// interpolation.
const MINIMAL_CONFIG = [
  'globalThis.getMinVersion = () => "0.0.0";',
  "globalThis.getBeforeConfigs = () => [];",
  "globalThis.getConfig = () => ({",
  '  apps: { "hello-shell": { description: "say hi", shell: { name: "echo" } } },',
  "  managedConfigs: {},",
  "  runtimes: {},",
  "  tools: {",
  "    hello: {",
  '      name: "hello",',
  "      operations: {",
  '        fix: { app: "hello-shell", args: ["{file}"], globs: ["**/*.txt"], scope: "per-file" },',
  "      },",
  "    },",
  "  },",
  "});",
].join("\n");

const projectDirectory = mkdtempSync(join(tmpdir(), "datamitsu-api-"));
// Planning resolves the git root first, so the throwaway project has to be one.
execFileSync("git", ["init", "-q"], { cwd: projectDirectory });
writeFileSync(join(projectDirectory, "datamitsu.config.js"), MINIMAL_CONFIG);
writeFileSync(join(projectDirectory, "a.txt"), "hi\n");

after(() => {
  rmSync(projectDirectory, { force: true, recursive: true });
});

describe("integration: fix --explain=json", () => {
  it("returns valid PlanJSON structure", async () => {
    const result = await fix({
      cwd: projectDirectory,
      explain: "json",
    });

    assert.equal(result.success, true, `fix failed: ${result.error}`);
    assert.ok(result.plan, "plan should be defined");
    assert.equal(result.plan.operation, "fix");
    assert.ok(typeof result.plan.rootPath === "string");
    assert.ok(typeof result.plan.cwdPath === "string");
    assert.ok(Array.isArray(result.plan.groups));

    for (const group of result.plan.groups) {
      assert.ok(typeof group.priority === "number");
      assert.ok(Array.isArray(group.parallelGroups));

      for (const pg of group.parallelGroups) {
        assert.ok(typeof pg.canRunInParallel === "boolean");
        assert.ok(Array.isArray(pg.tasks));

        for (const task of pg.tasks) {
          assert.ok(typeof task.toolName === "string");
          assert.ok(typeof task.app === "string");
          assert.ok(Array.isArray(task.args));
        }
      }
    }
  });
});

describe("integration: exec without args", () => {
  it("returns tool list with at least one tool", async () => {
    const result = await exec(undefined, {
      cwd: projectDirectory,
    });

    assert.equal(result.success, true, `exec failed: ${result.error}`);
    assert.ok(Array.isArray(result.tools));
    assert.ok(result.tools.length > 0, "should have at least one tool");

    const tool = result.tools[0];
    assert.ok(typeof tool.name === "string");
    assert.ok(typeof tool.type === "string");
    assert.ok(
      ["binary", "bun", "go", "jvm", "node", "shell", "uv"].includes(tool.type),
      `unexpected tool type: ${tool.type}`,
    );
  });
});

describe("integration: cache.path", () => {
  it("returns a valid directory path", async () => {
    const result = await cache.path();

    assert.equal(result.success, true, `cache.path failed: ${result.error}`);
    assert.ok(typeof result.path === "string");
    assert.ok(result.path.length > 0, "path should not be empty");
    assert.ok(
      result.path.startsWith("/") || /^[A-Z]:\\/i.test(result.path),
      `path should be absolute: ${result.path}`,
    );
  });
});

describe("integration: version", () => {
  it("returns a version string", async () => {
    const result = await version();

    assert.equal(result.success, true, `version failed: ${result.error}`);
    assert.ok(typeof result.version === "string");
    assert.ok(result.version.length > 0, "version should not be empty");
    assert.ok(
      /^(datamitsu version )?\S+/.test(result.version),
      `unexpected version format: ${result.version}`,
    );
  });
});
