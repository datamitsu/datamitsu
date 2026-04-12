import assert from "node:assert/strict";
import { beforeEach, describe, it, mock } from "node:test";

const mockSpawn = mock.fn();

mock.module("../spawn.js", {
  namedExports: {
    spawn: mockSpawn,
  },
});

const { lint } = await import("./lint.ts");

describe("lint", () => {
  beforeEach(() => {
    mockSpawn.mock.resetCalls();
  });

  it("lint with explain='json' parses and returns PlanJSON", async () => {
    const planJSON = {
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
        stdout: JSON.stringify(planJSON),
      }),
    );

    const result = await lint({ explain: "json" });
    assert.equal(result.success, true);
    assert.deepEqual(result.plan, planJSON);
    assert.equal(result.raw.exitCode, 0);

    const args = mockSpawn.mock.calls[0].arguments[0];
    assert.equal(args[0], "lint");
    assert.ok(args.includes("--explain=json"));
  });

  it("lint with explain='summary' returns text output", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "Lint plan summary text",
      }),
    );

    const result = await lint({ explain: "summary" });
    assert.equal(result.success, true);
    assert.equal(result.output, "Lint plan summary text");
    assert.equal(result.plan, undefined);
  });

  it("lint with explain=false executes and returns success/exitCode", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "some output",
      }),
    );

    const result = await lint();
    assert.equal(result.success, true);
    assert.equal(result.exitCode, 0);
    assert.equal(result.plan, undefined);
  });

  it("lint with files array passes files as args", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await lint({ files: ["src/a.ts", "src/b.ts"] });

    const args = mockSpawn.mock.calls[0].arguments[0];
    assert.ok(args.includes("src/a.ts"));
    assert.ok(args.includes("src/b.ts"));
  });

  it("lint handles tool errors gracefully", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 1,
        failed: true,
        stderr: "lint failed",
        stdout: "",
      }),
    );

    const result = await lint();
    assert.equal(result.success, false);
    assert.equal(result.exitCode, 1);
    assert.equal(result.error, "lint failed");
  });

  it("lint with fileScoped adds --file-scoped flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await lint({ fileScoped: true });

    const args = mockSpawn.mock.calls[0].arguments[0];
    assert.ok(args.includes("--file-scoped"));
  });

  it("lint with tools array adds --tools flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await lint({ tools: ["eslint", "tsc"] });

    const args = mockSpawn.mock.calls[0].arguments[0];
    const toolsIdx = args.indexOf("--tools");
    assert.ok(toolsIdx !== -1);
    assert.equal(args[toolsIdx + 1], "eslint,tsc");
  });

  it("lint with config and beforeConfig adds respective flags", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await lint({
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

  it("lint passes cwd and stdio to spawn", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await lint({ cwd: "/my/project", stdio: "inherit" });

    const options = mockSpawn.mock.calls[0].arguments[1];
    assert.equal(options.cwd, "/my/project");
    assert.equal(options.stdio, "inherit");
  });

  it("lint with explain='json' and invalid JSON returns error", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "not valid json",
      }),
    );

    const result = await lint({ explain: "json" });
    assert.equal(result.success, false);
    assert.ok(result.error.includes("Failed to parse JSON"));
  });

  it("lint with noAutoConfig adds --no-auto-config flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await lint({ noAutoConfig: true });

    const args = mockSpawn.mock.calls[0].arguments[0];
    assert.ok(args.includes("--no-auto-config"));
  });
});
