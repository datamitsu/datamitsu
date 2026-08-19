import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync } from "node:fs";
import { chmod, mkdir, mkdtemp, rename, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { RELEASES, VERSION } from "./generated";
import { binaryName, resolveTarget, targetKey } from "./platform";

const REPO = "datamitsu/datamitsu";

export interface DownloadDependencies {
  log: (message: string) => void;
  // Directory the verified binary is cached in (the extension's global storage).
  storageDir: string;
}

// One in-flight download per process: concurrent resolveBinary() calls share it.
// This is the plan's "one datamitsu process at a time" mutex, scoped to where it
// actually matters — the network/extract — so two activations never race on the
// same store path.
let inflight: Promise<string> | undefined;

// downloadBundled returns the path to the datamitsu binary pinned to this
// extension build, downloading and SHA-256-verifying it on first use and caching
// it under storageDir. Hash-or-refuse: a build with no baked checksums throws.
export async function downloadBundled(dependencies: DownloadDependencies): Promise<string> {
  inflight ??= run(dependencies).finally(() => {
    inflight = undefined;
  });
  return inflight;
}

// extract uses the system `tar` (BSD tar on macOS/Windows 10+, GNU tar on Linux),
// which unpacks both .tar.gz and .zip, so the extension needs no archive library.
async function extract(archive: string, destination: string): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const child = spawn("tar", ["-xf", archive, "-C", destination], { stdio: "ignore" });
    child.on("error", reject);
    child.on("exit", (code) => {
      if (code === 0) {
        resolve();
      } else {
        reject(new Error(`tar exited with code ${String(code)}`));
      }
    });
  });
}

async function fetchBytes(url: string): Promise<Buffer> {
  const response = await fetch(url, { redirect: "follow" });
  if (!response.ok) {
    throw new Error(`Download failed (${response.status} ${response.statusText}) for ${url}`);
  }
  return Buffer.from(await response.arrayBuffer());
}

async function run(dependencies: DownloadDependencies): Promise<string> {
  const { goarch, goos } = resolveTarget(process.platform, process.arch);
  const key = targetKey({ goarch, goos });
  const asset = RELEASES[key];

  if (VERSION === "0.0.0" || asset === undefined) {
    throw new Error(
      `This extension build has no pinned datamitsu binary for ${key} (version "${VERSION}"). ` +
        `Install datamitsu and set datamitsu.binaryMode to "system", or set datamitsu.path.`,
    );
  }

  const exeName = binaryName(goos);
  const versionDirectory = join(dependencies.storageDir, VERSION);
  const cached = join(versionDirectory, exeName);
  if (existsSync(cached)) {
    return cached;
  }

  await mkdir(dependencies.storageDir, { recursive: true });
  await mkdir(versionDirectory, { recursive: true });

  const url = `https://github.com/${REPO}/releases/download/v${VERSION}/${asset.file}`;
  dependencies.log(`Downloading ${asset.file}`);
  const data = await fetchBytes(url);

  const digest = createHash("sha256").update(data).digest("hex");
  if (digest !== asset.sha256) {
    throw new Error(`SHA-256 mismatch for ${asset.file}: expected ${asset.sha256}, got ${digest}`);
  }
  dependencies.log(`Verified SHA-256 ${digest}`);

  // Unpack in a private, freshly-created (0700) directory on the SAME filesystem
  // as the cache, then atomically rename the binary into place. This avoids a
  // predictable temp path a local attacker could pre-create as a symlink, writes
  // the verified bytes with O_EXCL ("wx"), and lets a concurrent process see only
  // the absent-or-complete cache entry, never a half-extracted one.
  const work = await mkdtemp(join(dependencies.storageDir, ".dl-"));
  try {
    const archive = join(work, asset.file);
    await writeFile(archive, data, { flag: "wx" });
    await extract(archive, work);

    const extracted = join(work, exeName);
    if (!existsSync(extracted)) {
      throw new Error(`Archive ${asset.file} did not contain ${exeName}`);
    }
    await chmod(extracted, 0o755);
    await rename(extracted, cached);
  } finally {
    await rm(work, { force: true, recursive: true });
  }

  return cached;
}
