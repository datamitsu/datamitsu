import assert from "node:assert/strict";
import { beforeEach, describe, it, mock } from "node:test";

const mockSpawn = mock.fn();

mock.module("../spawn.js", {
  namedExports: {
    spawn: mockSpawn,
  },
});

const { cache } = await import("./cache.ts");

describe("cache.clear", () => {
  beforeEach(() => {
    mockSpawn.mock.resetCalls();
  });

  it("cache.clear() returns success and message", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "Cache cleared successfully",
      }),
    );

    const result = await cache.clear();
    assert.equal(result.success, true);
    assert.equal(result.message, "Cache cleared successfully");

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    assert.deepEqual(args, ["cache", "clear"]);
  });

  it("cache.clear with all=true adds --all flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "All caches cleared",
      }),
    );

    const result = await cache.clear({ all: true });
    assert.equal(result.success, true);

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    assert.ok(args.includes("--all"));
  });

  it("cache.clear with dryRun=true adds --dry-run flag", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "Would clear cache",
      }),
    );

    const result = await cache.clear({ dryRun: true });
    assert.equal(result.success, true);

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    assert.ok(args.includes("--dry-run"));
  });

  it("cache.clear passes cwd to spawn", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "",
      }),
    );

    await cache.clear({ cwd: "/my/project" });

    const call = mockSpawn.mock.calls[0];
    const options = call.arguments[1];
    assert.equal(options.cwd, "/my/project");
  });

  it("cache.clear handles errors gracefully", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 1,
        failed: true,
        stderr: "cache clear failed",
        stdout: "",
      }),
    );

    const result = await cache.clear();
    assert.equal(result.success, false);
    assert.equal(result.exitCode, 1);
    assert.equal(result.error, "cache clear failed");
  });
});

describe("cache.path", () => {
  beforeEach(() => {
    mockSpawn.mock.resetCalls();
  });

  it("cache.path() returns cache directory path as string", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "/home/user/.cache/datamitsu\n",
      }),
    );

    const result = await cache.path();
    assert.equal(result.success, true);
    assert.equal(result.path, "/home/user/.cache/datamitsu");

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    assert.deepEqual(args, ["cache", "path"]);
  });

  it("cache.path handles errors gracefully", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 1,
        failed: true,
        stderr: "error getting path",
        stdout: "",
      }),
    );

    const result = await cache.path();
    assert.equal(result.success, false);
    assert.equal(result.error, "error getting path");
  });
});

describe("cache.pathProject", () => {
  beforeEach(() => {
    mockSpawn.mock.resetCalls();
  });

  it("cache.pathProject() returns project cache path as string", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "/home/user/.cache/datamitsu/projects/abc123\n",
      }),
    );

    const result = await cache.pathProject();
    assert.equal(result.success, true);
    assert.equal(result.path, "/home/user/.cache/datamitsu/projects/abc123");

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    assert.deepEqual(args, ["cache", "path", "project"]);
  });

  it("cache.pathProject respects cwd option", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "/home/user/.cache/datamitsu/projects/def456\n",
      }),
    );

    await cache.pathProject({ cwd: "/other/project" });

    const call = mockSpawn.mock.calls[0];
    const options = call.arguments[1];
    assert.equal(options.cwd, "/other/project");
  });

  it("cache.pathProject handles errors gracefully", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 1,
        failed: true,
        stderr: "not in a git repo",
        stdout: "",
      }),
    );

    const result = await cache.pathProject();
    assert.equal(result.success, false);
    assert.equal(result.error, "not in a git repo");
  });
});
