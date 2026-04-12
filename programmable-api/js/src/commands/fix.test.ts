import assert from "node:assert/strict";
import { beforeEach, describe, it, mock } from "node:test";

const mockSpawn = mock.fn();

mock.module("../spawn.js", {
  namedExports: {
    spawn: mockSpawn,
  },
});

const { fix } = await import("./fix.ts");

describe("fix", () => {
  beforeEach(() => {
    mockSpawn.mock.resetCalls();
  });

  it("fix with explain='json' parses and returns PlanJSON", async () => {
    const planJSON = {
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
        stdout: JSON.stringify(planJSON),
      }),
    );

    const result = await fix({ explain: "json" });
    assert.equal(result.success, true);
    assert.deepEqual(result.plan, planJSON);
    assert.equal(result.raw.exitCode, 0);
  });

  it("fix with explain='summary' returns text output", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "Fix plan summary text",
      }),
    );

    const result = await fix({ explain: "summary" });
    assert.equal(result.success, true);
    assert.equal(result.output, "Fix plan summary text");
    assert.equal(result.plan, undefined);
  });

  it("fix with explain=false executes and returns success/exitCode", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "some output",
      }),
    );

    const result = await fix();
    assert.equal(result.success, true);
    assert.equal(result.exitCode, 0);
    assert.equal(result.plan, undefined);
  });

  it("fix with files array passes files as args", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await fix({ files: ["src/a.ts", "src/b.ts"] });

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    assert.ok(args.includes("src/a.ts"));
    assert.ok(args.includes("src/b.ts"));
  });

  it("fix with fileScoped adds --file-scoped flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await fix({ fileScoped: true });

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    assert.ok(args.includes("--file-scoped"));
  });

  it("fix with tools array adds --tools flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await fix({ tools: ["prettier", "eslint"] });

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    const toolsIdx = args.indexOf("--tools");
    assert.ok(toolsIdx !== -1);
    assert.equal(args[toolsIdx + 1], "prettier,eslint");
  });

  it("fix with config adds --config flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await fix({ config: ["./custom.config.js"] });

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    const idx = args.indexOf("--config");
    assert.ok(idx !== -1);
    assert.equal(args[idx + 1], "./custom.config.js");
  });

  it("fix with beforeConfig adds --before-config flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await fix({ beforeConfig: ["./base.config.js"] });

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    const idx = args.indexOf("--before-config");
    assert.ok(idx !== -1);
    assert.equal(args[idx + 1], "./base.config.js");
  });

  it("fix handles tool errors (exit code != 0) gracefully", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 1,
        failed: true,
        stderr: "tool failed",
        stdout: "",
      }),
    );

    const result = await fix();
    assert.equal(result.success, false);
    assert.equal(result.exitCode, 1);
    assert.equal(result.error, "tool failed");
  });

  it("fix with noAutoConfig adds --no-auto-config flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await fix({ noAutoConfig: true });

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    assert.ok(args.includes("--no-auto-config"));
  });

  it("fix passes cwd and stdio to spawn", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await fix({ cwd: "/my/project", stdio: "inherit" });

    const call = mockSpawn.mock.calls[0];
    const options = call.arguments[1];
    assert.equal(options.cwd, "/my/project");
    assert.equal(options.stdio, "inherit");
  });

  it("fix with explain='json' and invalid JSON returns error", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "not valid json",
      }),
    );

    const result = await fix({ explain: "json" });
    assert.equal(result.success, false);
    assert.ok(result.error.includes("Failed to parse JSON"));
  });

  it("fix with multiple config files adds each with --config flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await fix({ config: ["./a.config.js", "./b.config.js"] });

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    const firstIdx = args.indexOf("--config");
    assert.ok(firstIdx !== -1);
    assert.equal(args[firstIdx + 1], "./a.config.js");
    const secondIdx = args.indexOf("--config", firstIdx + 1);
    assert.ok(secondIdx !== -1);
    assert.equal(args[secondIdx + 1], "./b.config.js");
  });
});
