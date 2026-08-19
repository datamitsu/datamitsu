import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

// gen-binaries bakes src/generated.ts at release time: it maps each datamitsu
// release target ("<goos>_<goarch>") to its archive asset filename and SHA-256,
// read from the GoReleaser checksums.txt. The extension's bundled-binary path
// downloads from these and verifies the hash (hash-or-refuse). VERSION comes from
// the release tag.
const EXT_DIR = join(import.meta.dirname, "..");
const REPO_ROOT = join(EXT_DIR, "..", "..");

const version = (process.env.VERSION ?? "").replace(/^v/, "");
if (version === "") {
  throw new Error("VERSION env var is required (e.g. v0.1.5)");
}

const checksumsPath = process.env.CHECKSUMS_FILE ?? join(REPO_ROOT, "dist", "checksums.txt");

// Parse "<sha256>  <filename>" lines (BSD "*" binary marker tolerated).
const checksums = new Map<string, string>();
for (const line of readFileSync(checksumsPath, "utf8").split("\n")) {
  const match = /^([0-9a-f]{64})[ \t]+\*?([^\s](?:.*[^\s])?)$/i.exec(line.trim());
  const sha = match?.[1];
  const file = match?.[2];
  if (sha !== undefined && file !== undefined) {
    checksums.set(file, sha.toLowerCase());
  }
}

// The host targets the extension can download a pinned binary for. Windows ships
// as .zip, the rest as .tar.gz (GoReleaser defaults).
const targets = [
  { goarch: "amd64", goos: "linux" },
  { goarch: "arm64", goos: "linux" },
  { goarch: "amd64", goos: "darwin" },
  { goarch: "arm64", goos: "darwin" },
  { goarch: "amd64", goos: "windows" },
  { goarch: "arm64", goos: "windows" },
];

const releases: Record<string, { file: string; sha256: string }> = {};
for (const { goarch, goos } of targets) {
  const file =
    goos === "windows"
      ? `datamitsu_${version}_${goos}_${goarch}.zip`
      : `datamitsu_${version}_${goos}_${goarch}.tar.gz`;
  const sha256 = checksums.get(file);
  if (sha256 === undefined) {
    console.warn(`⚠️  no checksum for ${file} — skipping ${goos}_${goarch}`);
    continue;
  }
  releases[`${goos}_${goarch}`] = { file, sha256 };
}

if (Object.keys(releases).length === 0) {
  throw new Error(`no datamitsu release assets for ${version} in ${checksumsPath}`);
}

const entries = Object.entries(releases)
  .map(
    ([key, { file, sha256 }]) =>
      `  ${JSON.stringify(key)}: { file: ${JSON.stringify(file)}, sha256: ${JSON.stringify(sha256)} },`,
  )
  .join("\n");

const body = `// AUTO-GENERATED at release time by scripts/gen-binaries.ts. Do not edit by hand.
// Maps "<goos>_<goarch>" to the datamitsu release asset filename and its SHA-256.
export interface ReleaseAsset {
  file: string;
  sha256: string;
}

export const VERSION = ${JSON.stringify(version)};

export const RELEASES: Record<string, ReleaseAsset> = {
${entries}
};
`;

writeFileSync(join(EXT_DIR, "src", "generated.ts"), body);
console.log(`✓ wrote src/generated.ts for ${version} (${Object.keys(releases).length} platforms)`);
