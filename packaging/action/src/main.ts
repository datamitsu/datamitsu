import * as core from "@actions/core";
import * as exec from "@actions/exec";
import * as tc from "@actions/tool-cache";
import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";

import { RELEASES, VERSION } from "./generated";
import { resolveTarget } from "./platform";

const TOOL = "datamitsu";
const REPO = "datamitsu/datamitsu";

async function install(): Promise<string> {
  const { goarch, goos } = resolveTarget(process.platform, process.arch);
  const key = `${goos}_${goarch}`;
  const asset = RELEASES[key];

  // Hash-or-refuse: every real release bakes the asset filename and its
  // SHA-256. A missing entry means an unverified build — never install it.
  if (VERSION === "0.0.0" || !asset) {
    throw new Error(
      `No verified release for ${key} (version "${VERSION}"). This action build ` +
        `is missing baked checksums and cannot install datamitsu safely.`,
    );
  }

  const cached = tc.find(TOOL, VERSION, goarch);
  if (cached) {
    core.addPath(cached);
    return cached;
  }

  const url = `https://github.com/${REPO}/releases/download/v${VERSION}/${asset.file}`;
  core.info(`Downloading ${asset.file}`);
  const archive = await tc.downloadTool(url);

  const digest = await sha256(archive);
  if (digest !== asset.sha256) {
    throw new Error(`SHA-256 mismatch for ${asset.file}: expected ${asset.sha256}, got ${digest}`);
  }
  core.info(`Verified SHA-256 ${digest}`);

  const extracted = asset.file.endsWith(".zip")
    ? await tc.extractZip(archive)
    : await tc.extractTar(archive);
  const installPath = await tc.cacheDir(extracted, TOOL, VERSION, goarch);
  core.addPath(installPath);
  return installPath;
}

async function run(): Promise<void> {
  const installPath = await install();
  core.setOutput("version", VERSION);
  core.info(`datamitsu ${VERSION} ready (${installPath})`);

  if (core.getBooleanInput("init")) {
    await exec.exec(TOOL, ["init"]);
  }

  const arguments_ = core.getInput("args");
  if (arguments_.trim() !== "") {
    await exec.exec(`${TOOL} ${arguments_}`);
  }
}

async function sha256(file: string): Promise<string> {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(file)) {
    hash.update(chunk as Buffer);
  }
  return hash.digest("hex");
}

try {
  await run();
} catch (error: unknown) {
  core.setFailed(error instanceof Error ? error.message : String(error));
}
