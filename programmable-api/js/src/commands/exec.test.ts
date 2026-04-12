import assert from "node:assert/strict";
import { beforeEach, describe, it, mock } from "node:test";

const mockSpawn = mock.fn();

mock.module("../spawn.js", {
  namedExports: {
    spawn: mockSpawn,
  },
});

const { exec, parseToolList } = await import("./exec.ts");

describe("exec", () => {
  beforeEach(() => {
    mockSpawn.mock.resetCalls();
  });

  it("exec without appName returns parsed tool list", async () => {
    const output = [
      "Available tools:",
      "",
      "[binary]",
      "  lefthook    0.9.0  Git hooks manager",
      "  shellcheck  0.10.0",
      "",
      "[uv]",
      "  yamllint  1.35.0  YAML linter",
      "",
    ].join("\n");

    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: output,
      }),
    );

    const result = await exec();
    assert.equal(result.success, true);
    assert.ok("tools" in result && Array.isArray(result.tools), "expected tools property");
    assert.equal(result.tools.length, 3);
    assert.deepEqual(result.tools[0], {
      details: "0.9.0  Git hooks manager",
      name: "lefthook",
      type: "binary",
    });
    assert.deepEqual(result.tools[1], {
      details: "0.10.0",
      name: "shellcheck",
      type: "binary",
    });
    assert.deepEqual(result.tools[2], {
      details: "1.35.0  YAML linter",
      name: "yamllint",
      type: "uv",
    });
  });

  it("exec with appName executes app and returns stdout/stderr", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "tool output here",
      }),
    );

    const result = await exec("lefthook");
    assert.equal(result.success, true);
    assert.ok("stdout" in result, "expected stdout property");
    assert.equal(result.stdout, "tool output here");
    assert.equal(result.stderr, "");
    assert.equal(result.exitCode, 0);
  });

  it("exec with args array passes args to app", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await exec("lefthook", { args: ["run", "pre-commit"] });

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    assert.deepEqual(args, ["exec", "lefthook", "run", "pre-commit"]);
  });

  it("exec handles app errors gracefully", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 1,
        failed: true,
        stderr: "app crashed",
        stdout: "",
      }),
    );

    const result = await exec("broken-tool");
    assert.equal(result.success, false);
    assert.ok("error" in result, "expected error property");
    assert.equal(result.exitCode, 1);
    assert.equal(result.error, "app crashed");
  });

  it("exec passes cwd and stdio to spawn", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await exec("lefthook", { cwd: "/my/project", stdio: "inherit" });

    const call = mockSpawn.mock.calls[0];
    const options = call.arguments[1];
    assert.equal(options.cwd, "/my/project");
    assert.equal(options.stdio, "inherit");
  });

  it("exec passes config and beforeConfig flags", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await exec("lefthook", {
      beforeConfig: ["./base.config.js"],
      config: ["./custom.config.js"],
    });

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    const configIdx = args.indexOf("--config");
    assert.ok(configIdx !== -1);
    assert.equal(args[configIdx + 1], "./custom.config.js");
    const beforeIdx = args.indexOf("--before-config");
    assert.ok(beforeIdx !== -1);
    assert.equal(args[beforeIdx + 1], "./base.config.js");
  });

  it("exec passes noAutoConfig flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await exec("lefthook", { noAutoConfig: true });

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    assert.ok(args.includes("--no-auto-config"));
  });

  it("exec without appName and empty output returns empty tools array", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    const result = await exec();
    assert.equal(result.success, true);
    assert.ok("tools" in result, "expected tools property");
    assert.deepEqual(result.tools, []);
  });

  it("exec without appName returns error when command fails", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 1,
        failed: true,
        stderr: "config load error",
        stdout: "",
      }),
    );

    const result = await exec();
    assert.equal(result.success, false);
    assert.ok("error" in result, "expected error property");
    assert.equal(result.error, "config load error");
    assert.equal(result.exitCode, 1);
    assert.equal("tools" in result, false);
  });
});

describe("parseToolList", () => {
  it("correctly parses table format grouped by type", () => {
    const output = [
      "Available tools:",
      "",
      "[binary]",
      "  lefthook    0.9.0  Git hooks manager",
      "  shellcheck  0.10.0",
      "",
      "[uv]",
      "  yamllint  1.35.0  YAML linter",
      "",
      "[fnm]",
      "  slidev  0.50.0  Presentation slides",
      "",
    ].join("\n");

    const tools = parseToolList(output);
    assert.equal(tools.length, 4);
    assert.equal(tools[0].name, "lefthook");
    assert.equal(tools[0].type, "binary");
    assert.equal(tools[1].name, "shellcheck");
    assert.equal(tools[1].type, "binary");
    assert.equal(tools[2].name, "yamllint");
    assert.equal(tools[2].type, "uv");
    assert.equal(tools[3].name, "slidev");
    assert.equal(tools[3].type, "fnm");
  });

  it("handles empty output", () => {
    const tools = parseToolList("");
    assert.deepEqual(tools, []);
  });

  it("extracts name, type, details correctly", () => {
    const output = [
      "[jvm]",
      "  openapi-generator  7.0.0  (openapi-generator-cli)  OpenAPI code generator",
    ].join("\n");

    const tools = parseToolList(output);
    assert.equal(tools.length, 1);
    assert.equal(tools[0].name, "openapi-generator");
    assert.equal(tools[0].type, "jvm");
    assert.equal(tools[0].details, "7.0.0  (openapi-generator-cli)  OpenAPI code generator");
  });

  it("handles tools without details", () => {
    const output = ["[shell]", "  my-tool"].join("\n");

    const tools = parseToolList(output);
    assert.equal(tools.length, 1);
    assert.equal(tools[0].name, "my-tool");
    assert.equal(tools[0].type, "shell");
    assert.equal(tools[0].details, "");
  });

  it("handles ANSI color codes in output", () => {
    const output = [
      "\u001B[1m[binary]\u001B[0m",
      "  lefthook    \u001B[2m0.9.0  Git hooks manager\u001B[0m",
      "",
      "\u001B[1m[uv]\u001B[0m",
      "  yamllint  \u001B[2m1.35.0  YAML linter\u001B[0m",
    ].join("\n");

    const tools = parseToolList(output);
    assert.equal(tools.length, 2);
    assert.equal(tools[0].name, "lefthook");
    assert.equal(tools[0].type, "binary");
    assert.equal(tools[1].name, "yamllint");
    assert.equal(tools[1].type, "uv");
  });

  it("handles all five type groups", () => {
    const output = [
      "[binary]",
      "  b1  v1",
      "[uv]",
      "  u1  v2",
      "[fnm]",
      "  f1  v3",
      "[jvm]",
      "  j1  v4",
      "[shell]",
      "  s1  v5",
    ].join("\n");

    const tools = parseToolList(output);
    assert.equal(tools.length, 5);
    assert.equal(tools[0].type, "binary");
    assert.equal(tools[1].type, "uv");
    assert.equal(tools[2].type, "fnm");
    assert.equal(tools[3].type, "jvm");
    assert.equal(tools[4].type, "shell");
  });
});
