import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execPath, platform } from "node:process";
import { test } from "node:test";

import { findInPath, resolveBinary } from "./binary";

const baseOptions = {
  explicitPath: "",
  log: () => {},
  storageDir: join(tmpdir(), "dm-vscode-test"),
};

test("explicit path: returns it when it exists, errors when missing", async () => {
  const ok = await resolveBinary({ ...baseOptions, explicitPath: execPath, mode: "auto" });
  assert.equal(ok, execPath);

  await assert.rejects(
    resolveBinary({ ...baseOptions, explicitPath: "/no/such/datamitsu", mode: "auto" }),
    /missing file/,
  );
});

test("system mode: returns the PATH binary, errors when absent", async () => {
  const found = await resolveBinary({
    ...baseOptions,
    download: async () => assert.fail("must not download in system mode"),
    lookPath: () => "/usr/bin/datamitsu",
    mode: "system",
  });
  assert.equal(found, "/usr/bin/datamitsu");

  await assert.rejects(
    resolveBinary({ ...baseOptions, lookPath: () => {}, mode: "system" }),
    /not found on PATH/,
  );
});

test("auto mode: prefers PATH, falls back to download", async () => {
  const sys = await resolveBinary({
    ...baseOptions,
    download: async () => assert.fail("must not download when PATH has datamitsu"),
    lookPath: () => "/usr/bin/datamitsu",
    mode: "auto",
  });
  assert.equal(sys, "/usr/bin/datamitsu");

  const dl = await resolveBinary({
    ...baseOptions,
    download: async () => "/cache/datamitsu",
    lookPath: () => {},
    mode: "auto",
  });
  assert.equal(dl, "/cache/datamitsu");
});

test("bundled mode: always downloads, ignoring PATH", async () => {
  const dl = await resolveBinary({
    ...baseOptions,
    download: async () => "/cache/datamitsu",
    lookPath: () => "/usr/bin/datamitsu",
    mode: "bundled",
  });
  assert.equal(dl, "/cache/datamitsu");
});

test(
  "findInPath returns an executable regular file on PATH",
  { skip: platform === "win32" },
  () => {
    const directory = mkdtempSync(join(tmpdir(), "dm-path-"));
    const exe = join(directory, "datamitsu");
    writeFileSync(exe, "#!/bin/sh\necho hi\n");
    chmodSync(exe, 0o755);

    const saved = process.env.PATH;
    process.env.PATH = directory;
    try {
      assert.equal(findInPath("datamitsu"), exe);
    } finally {
      process.env.PATH = saved;
    }
  },
);

test(
  "findInPath skips a directory and a non-executable file (auto can fall through)",
  { skip: platform === "win32" },
  () => {
    const directoryOnly = mkdtempSync(join(tmpdir(), "dm-directory-"));
    mkdirSync(join(directoryOnly, "datamitsu")); // a directory named like the binary

    const nonExecDirectory = mkdtempSync(join(tmpdir(), "dm-file-"));
    const nonExec = join(nonExecDirectory, "datamitsu");
    writeFileSync(nonExec, "not executable");
    chmodSync(nonExec, 0o644);

    const saved = process.env.PATH;
    process.env.PATH = [directoryOnly, nonExecDirectory].join(":");
    try {
      assert.equal(findInPath("datamitsu"), undefined);
    } finally {
      process.env.PATH = saved;
    }
  },
);
