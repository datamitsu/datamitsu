import type { SpawnRaw } from "../types.js";

import { spawn } from "../spawn.js";

export interface CacheClearOptions {
  all?: boolean;
  cwd?: string;
  dryRun?: boolean;
}

export type CacheClearResult =
  | { error: string; exitCode: number; raw: SpawnRaw; success: false }
  | { message: string; raw: SpawnRaw; success: true };

export interface CachePathProjectOptions {
  cwd?: string;
}

export type CachePathResult =
  | { error: string; exitCode: number; raw: SpawnRaw; success: false }
  | { path: string; raw: SpawnRaw; success: true };

export const cache = {
  async clear(options: CacheClearOptions = {}): Promise<CacheClearResult> {
    const { all = false, cwd, dryRun = false } = options;
    const arguments_ = ["cache", "clear"];

    if (all) {
      arguments_.push("--all");
    }

    if (dryRun) {
      arguments_.push("--dry-run");
    }

    const raw = await spawn(arguments_, cwd ? { cwd } : {});

    if (raw.failed) {
      return {
        error: raw.stderr || raw.stdout,
        exitCode: raw.exitCode,
        raw,
        success: false,
      };
    }

    return {
      message: raw.stdout.trim(),
      raw,
      success: true,
    };
  },

  async path(): Promise<CachePathResult> {
    const raw = await spawn(["cache", "path"]);

    if (raw.failed) {
      return {
        error: raw.stderr || raw.stdout,
        exitCode: raw.exitCode,
        raw,
        success: false,
      };
    }

    return {
      path: raw.stdout.trim(),
      raw,
      success: true,
    };
  },

  async pathProject(options: CachePathProjectOptions = {}): Promise<CachePathResult> {
    const { cwd } = options;
    const raw = await spawn(["cache", "path", "project"], cwd ? { cwd } : {});

    if (raw.failed) {
      return {
        error: raw.stderr || raw.stdout,
        exitCode: raw.exitCode,
        raw,
        success: false,
      };
    }

    return {
      path: raw.stdout.trim(),
      raw,
      success: true,
    };
  },
} as const;
