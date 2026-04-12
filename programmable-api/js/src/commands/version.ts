import type { SpawnRaw } from "../types.js";

import { spawn } from "../spawn.js";

export type VersionResult =
  | { error: string; exitCode: number; raw: SpawnRaw; success: false }
  | { raw: SpawnRaw; success: true; version: string };

export async function version(): Promise<VersionResult> {
  const raw = await spawn(["version"]);

  if (raw.failed) {
    return {
      error: raw.stderr || raw.stdout,
      exitCode: raw.exitCode,
      raw,
      success: false,
    };
  }

  const output = raw.stdout.trim().split("\n")[0] ?? "";
  const versionStr = output.replace(/^.*version\s+/, "");
  return {
    raw,
    success: true,
    version: versionStr,
  };
}
