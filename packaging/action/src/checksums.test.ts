import assert from "node:assert/strict";
import { test } from "node:test";

import { parseChecksums, platformAssets } from "./checksums";

const A = "a".repeat(64);
const B = "b".repeat(64);

test("parseChecksums maps filenames to lowercase sha256, skipping blanks", () => {
  const content = `${A}  datamitsu_0.1.5_linux_amd64.tar.gz\n\n${B.toUpperCase()} *datamitsu_0.1.5_windows_amd64.zip\n`;
  const map = parseChecksums(content);

  assert.equal(map.get("datamitsu_0.1.5_linux_amd64.tar.gz"), A);
  assert.equal(map.get("datamitsu_0.1.5_windows_amd64.zip"), B);
  assert.equal(map.size, 2);
});

test("platformAssets uses zip for windows and tar.gz otherwise", () => {
  const assets = platformAssets("0.1.5");

  assert.equal(
    assets.find((a) => a.key === "windows_amd64")?.file,
    "datamitsu_0.1.5_windows_amd64.zip",
  );
  assert.equal(
    assets.find((a) => a.key === "linux_arm64")?.file,
    "datamitsu_0.1.5_linux_arm64.tar.gz",
  );
});
