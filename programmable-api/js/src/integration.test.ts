import assert from "node:assert/strict";
import { resolve } from "node:path";
import { describe, it, mock } from "node:test";

const BINARY_PATH = resolve(import.meta.dirname, "../../../datamitsu");
const PROJECT_ROOT = resolve(import.meta.dirname, "../../..");

mock.module("@datamitsu/datamitsu/get-exe.js", {
  namedExports: {
    getExePath: () => BINARY_PATH,
  },
});

const { fix } = await import("./commands/fix.ts");
const { exec } = await import("./commands/exec.ts");
const { cache } = await import("./commands/cache.ts");
const { version } = await import("./commands/version.ts");

describe("integration: fix --explain=json", () => {
  it("returns valid PlanJSON structure", async () => {
    const result = await fix({
      cwd: PROJECT_ROOT,
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
      cwd: PROJECT_ROOT,
    });

    assert.equal(result.success, true, `exec failed: ${result.error}`);
    assert.ok(Array.isArray(result.tools));
    assert.ok(result.tools.length > 0, "should have at least one tool");

    const tool = result.tools[0];
    assert.ok(typeof tool.name === "string");
    assert.ok(typeof tool.type === "string");
    assert.ok(
      ["binary", "fnm", "jvm", "shell", "uv"].includes(tool.type),
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
