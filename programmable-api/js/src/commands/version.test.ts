import assert from "node:assert/strict";
import { beforeEach, describe, it, mock } from "node:test";

const mockSpawn = mock.fn();

mock.module("../spawn.js", {
  namedExports: {
    spawn: mockSpawn,
  },
});

const { version } = await import("./version.ts");

describe("version", () => {
  beforeEach(() => {
    mockSpawn.mock.resetCalls();
  });

  it("version() returns version string", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 0,
        failed: false,
        stderr: "",
        stdout: "datamitsu version 1.2.3\n",
      }),
    );

    const result = await version();
    assert.equal(result.success, true);
    assert.equal(result.version, "1.2.3");

    const call = mockSpawn.mock.calls[0];
    const args = call.arguments[0];
    assert.deepEqual(args, ["version"]);
  });

  it("version handles errors gracefully", async () => {
    mockSpawn.mock.mockImplementation(() =>
      Promise.resolve({
        exitCode: 1,
        failed: true,
        stderr: "unknown command",
        stdout: "",
      }),
    );

    const result = await version();
    assert.equal(result.success, false);
    assert.equal(result.exitCode, 1);
    assert.equal(result.error, "unknown command");
  });
});
