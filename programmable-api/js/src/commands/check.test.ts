import assert from "node:assert/strict";
import { beforeEach, describe, it, mock } from "node:test";

const mockSpawn = mock.fn();

mock.module("../spawn.js", {
  namedExports: {
    spawn: mockSpawn,
  },
});

const { check } = await import("./check.ts");

describe("check", () => {
  beforeEach(() => {
    mockSpawn.mock.resetCalls();
  });

  it("check with explain='json' parses and returns PlanJSON", async () => {
    const fixPlan = {
      cwdPath: "/root",
      groups: [],
      operation: "fix",
      rootPath: "/root",
    };
    const lintPlan = {
      cwdPath: "/root",
      groups: [],
      operation: "lint",
      rootPath: "/root",
    };
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "prefix\n" + JSON.stringify(fixPlan) + "\ntext\n" + JSON.stringify(lintPlan),
      }),
    );

    const result = await check({ explain: "json" });
    assert.equal(result.success, true);
    assert.deepEqual(result.plan, fixPlan);
    assert.deepEqual(result.plans, [fixPlan, lintPlan]);
    assert.equal(result.raw.exitCode, 0);

    const args = mockSpawn.mock.calls[0].arguments[0];
    assert.equal(args[0], "check");
    assert.ok(args.includes("--explain=json"));
  });

  it("check with explain='json' handles single JSON object", async () => {
    const plan = {
      cwdPath: "/root",
      groups: [],
      operation: "fix",
      rootPath: "/root",
    };
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: JSON.stringify(plan),
      }),
    );

    const result = await check({ explain: "json" });
    assert.equal(result.success, true);
    assert.deepEqual(result.plan, plan);
    assert.deepEqual(result.plans, [plan]);
  });

  it("check executes and returns success/exitCode", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "some output",
      }),
    );

    const result = await check();
    assert.equal(result.success, true);
    assert.equal(result.exitCode, 0);
    assert.equal(result.plan, undefined);
  });

  it("check handles errors (exit code != 0) gracefully", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 1,
        failed: true,
        stderr: "check failed",
        stdout: "",
      }),
    );

    const result = await check();
    assert.equal(result.success, false);
    assert.equal(result.exitCode, 1);
    assert.equal(result.error, "check failed");
  });

  it("check with explain='summary' returns text output", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "Check plan summary text",
      }),
    );

    const result = await check({ explain: "summary" });
    assert.equal(result.success, true);
    assert.equal(result.output, "Check plan summary text");
    assert.equal(result.plan, undefined);
  });

  it("check with files array passes files as args", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await check({ files: ["src/a.ts", "src/b.ts"] });

    const args = mockSpawn.mock.calls[0].arguments[0];
    assert.ok(args.includes("src/a.ts"));
    assert.ok(args.includes("src/b.ts"));
  });

  it("check with fileScoped adds --file-scoped flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await check({ fileScoped: true });

    const args = mockSpawn.mock.calls[0].arguments[0];
    assert.ok(args.includes("--file-scoped"));
  });

  it("check with tools array adds --tools flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await check({ tools: ["prettier", "eslint"] });

    const args = mockSpawn.mock.calls[0].arguments[0];
    const toolsIdx = args.indexOf("--tools");
    assert.ok(toolsIdx !== -1);
    assert.equal(args[toolsIdx + 1], "prettier,eslint");
  });

  it("check with config and beforeConfig adds respective flags", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await check({
      beforeConfig: ["./base.config.js"],
      config: ["./custom.config.js"],
    });

    const args = mockSpawn.mock.calls[0].arguments[0];
    const configIdx = args.indexOf("--config");
    assert.ok(configIdx !== -1);
    assert.equal(args[configIdx + 1], "./custom.config.js");
    const beforeIdx = args.indexOf("--before-config");
    assert.ok(beforeIdx !== -1);
    assert.equal(args[beforeIdx + 1], "./base.config.js");
  });

  it("check passes cwd and stdio to spawn", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await check({ cwd: "/my/project", stdio: "inherit" });

    const options = mockSpawn.mock.calls[0].arguments[1];
    assert.equal(options.cwd, "/my/project");
    assert.equal(options.stdio, "inherit");
  });

  it("check with explain='json' and invalid JSON returns error", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "not valid json",
      }),
    );

    const result = await check({ explain: "json" });
    assert.equal(result.success, false);
    assert.ok(result.error.includes("Failed to parse JSON"));
  });

  it("check with noAutoConfig adds --no-auto-config flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await check({ noAutoConfig: true });

    const args = mockSpawn.mock.calls[0].arguments[0];
    assert.ok(args.includes("--no-auto-config"));
  });
});
