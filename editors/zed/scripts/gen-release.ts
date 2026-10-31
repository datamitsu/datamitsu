import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import process from "node:process";

import { parseChecksums, platformAssets } from "../../../packaging/action/src/checksums.ts";

// Writes release.json: the datamitsu release this build of the Zed extension
// downloads when no datamitsu is on PATH, with each archive's SHA-256 read from
// GoReleaser's checksums.txt. Every target must be present — the extension
// refuses to download anything it has no hash for.
const EXTENSION_DIR = join(import.meta.dirname, "..");
const REPO_ROOT = join(EXTENSION_DIR, "..", "..");

const version = (process.env.VERSION ?? "").replace(/^v/, "");
if (version === "") {
  throw new Error("VERSION env var is required (e.g. v0.4.0)");
}

const checksumsPath = process.env.CHECKSUMS_FILE ?? join(REPO_ROOT, "dist", "checksums.txt");
const checksums = parseChecksums(readFileSync(checksumsPath, "utf8"));

const assets: Record<string, { file: string; hash: string }> = {};
for (const { file, key } of platformAssets(version)) {
  const sha256 = checksums.get(file);
  if (sha256 === undefined) {
    throw new Error(`no checksum for ${file} in ${checksumsPath}`);
  }
  assets[key] = { file, hash: `sha256:${sha256}` };
}

writeFileSync(
  join(EXTENSION_DIR, "release.json"),
  `${JSON.stringify({ assets, version }, undefined, 2)}\n`,
);
console.log(`✓ wrote release.json for ${version} (${Object.keys(assets).length} platforms)`);
