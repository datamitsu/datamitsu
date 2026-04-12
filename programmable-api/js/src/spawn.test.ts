import assert from "node:assert/strict";
import { beforeEach, describe, it, mock } from "node:test";

const mockX = mock.fn();
const mockGetExePath = mock.fn();

mock.module("tinyexec", {
  namedExports: {
    x: mockX,
  },
});

mock.module("@datamitsu/datamitsu/get-exe.js", {
  namedExports: {
    getExePath: mockGetExePath,
  },
});

const { spawn } = await import("./spawn.ts");

describe("spawn", () => {
  beforeEach(() => {
    mockX.mock.resetCalls();
    mockGetExePath.mock.resetCalls();
    mockGetExePath.mock.mockImplementation(() => "/usr/bin/datamitsu");
  });

  it("successful spawn returns stdout/stderr/exitCode", async () => {
    mockX.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        stderr: "",
        stdout: "output text",
      }),
    );

    const result = await spawn(["fix"]);
    assert.equal(result.stdout, "output text");
    assert.equal(result.stderr, "");
    assert.equal(result.exitCode, 0);
    assert.equal(result.failed, false);
  });

  it("failed spawn (non-zero exit) returns failed=true with output", async () => {
    mockX.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 1,
        stderr: "error output",
        stdout: "",
      }),
    );

    const result = await spawn(["lint"]);
    assert.equal(result.failed, true);
    assert.equal(result.exitCode, 1);
    assert.equal(result.stderr, "error output");
  });

  it("binary not found throws error with helpful message", async () => {
    mockGetExePath.mock.mockImplementation(() => {
      throw new Error("datamitsu binary not found");
    });

    await assert.rejects(() => spawn(["fix"]), {
      message: /datamitsu binary not found/,
    });
  });

  it("tinyexec spawn failure throws wrapped error with binary path", async () => {
    const enoentError = new Error("spawn /usr/bin/datamitsu ENOENT");
    (enoentError as NodeJS.ErrnoException).code = "ENOENT";
    mockX.mock.mockImplementation(() => Promise.reject(enoentError));

    await assert.rejects(() => spawn(["fix"]), {
      message: /Failed to execute datamitsu binary at \/usr\/bin\/datamitsu/,
    });
  });

  it("respects cwd option", async () => {
    mockX.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        stderr: "",
        stdout: "",
      }),
    );

    await spawn(["fix"], { cwd: "/home/user/project" });

    const call = mockX.mock.calls[0];
    const tinyexecOptions = call.arguments[2];
    assert.equal(tinyexecOptions.nodeOptions.cwd, "/home/user/project");
  });

  it("respects stdio option (pipe vs inherit)", async () => {
    mockX.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        stderr: "",
        stdout: "",
      }),
    );

    await spawn(["fix"], { stdio: "inherit" });

    const call = mockX.mock.calls[0];
    const tinyexecOptions = call.arguments[2];
    assert.equal(tinyexecOptions.nodeOptions.stdio, "inherit");
  });

  it("always adds --binary-command flag", async () => {
    mockX.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        stderr: "",
        stdout: "",
      }),
    );

    await spawn(["fix", "--explain"]);

    const call = mockX.mock.calls[0];
    const args = call.arguments[1];
    assert.ok(args.includes("--binary-command"));
    assert.ok(args.includes("datamitsu"));
    assert.ok(args.includes("fix"));
    assert.ok(args.includes("--explain"));
  });

  it("passes binary path to tinyexec", async () => {
    mockGetExePath.mock.mockImplementation(() => "/custom/path/datamitsu");
    mockX.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        stderr: "",
        stdout: "",
      }),
    );

    await spawn(["fix"]);

    const call = mockX.mock.calls[0];
    assert.equal(call.arguments[0], "/custom/path/datamitsu");
  });

  it("signal-killed process (exitCode undefined) returns exitCode 1 and failed true", async () => {
    mockX.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: undefined,
        stderr: "",
        stdout: "partial output",
      }),
    );

    const result = await spawn(["fix"]);
    assert.equal(result.exitCode, 1);
    assert.equal(result.failed, true);
    assert.equal(result.stdout, "partial output");
    assert.equal(result.stderr, "");
  });

  it("defaults stdio to pipe", async () => {
    mockX.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        stderr: "",
        stdout: "",
      }),
    );

    await spawn(["fix"]);

    const call = mockX.mock.calls[0];
    const tinyexecOptions = call.arguments[2];
    assert.equal(tinyexecOptions.nodeOptions.stdio, "pipe");
  });

  it("passes throwOnError false to prevent tinyexec from throwing on non-zero exit", async () => {
    mockX.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 1,
        stderr: "",
        stdout: "",
      }),
    );

    await spawn(["fix"]);

    const call = mockX.mock.calls[0];
    const tinyexecOptions = call.arguments[2];
    assert.equal(tinyexecOptions.throwOnError, false);
  });
});
