import type { SpawnRaw } from "../types.js";

import { spawn } from "../spawn.js";

export interface ExecOptions {
  args?: string[];
  beforeConfig?: string[];
  config?: string[];
  cwd?: string;
  noAutoConfig?: boolean;
  stdio?: "inherit" | "pipe";
}

export type ExecResult =
  | { error: string; exitCode: number; raw: SpawnRaw; success: false }
  | { exitCode: number; raw: SpawnRaw; stderr: string; stdout: string; success: true }
  | { raw: SpawnRaw; success: true; tools: ToolInfo[] };

export interface ToolInfo {
  details: string;
  name: string;
  type: string;
}

export async function exec(appName?: string, options: ExecOptions = {}): Promise<ExecResult> {
  const spawnArgs = buildArgs(appName, options);
  const spawnOpts: { cwd?: string; stdio?: "inherit" | "pipe" } = {};
  if (options.cwd !== undefined) {
    spawnOpts.cwd = options.cwd;
  }
  if (options.stdio !== undefined) {
    spawnOpts.stdio = options.stdio;
  }
  const raw = await spawn(spawnArgs, spawnOpts);

  if (!appName) {
    if (raw.failed) {
      return {
        error: raw.stderr || raw.stdout,
        exitCode: raw.exitCode,
        raw,
        success: false,
      };
    }
    const tools = parseToolList(raw.stdout);
    return { raw, success: true, tools };
  }

  if (raw.failed) {
    return {
      error: raw.stderr || raw.stdout,
      exitCode: raw.exitCode,
      raw,
      success: false,
    };
  }

  return {
    exitCode: raw.exitCode,
    raw,
    stderr: raw.stderr,
    stdout: raw.stdout,
    success: true,
  };
}

const ESC = String.fromCodePoint(0x1b);
const ANSI_PATTERN = new RegExp(`${ESC}\\[[0-9;]*m`, "g");

export function parseToolList(output: string): ToolInfo[] {
  if (!output || !output.trim()) {
    return [];
  }

  const tools: ToolInfo[] = [];
  let currentType: null | string = null;
  const typePattern = /^\[(binary|uv|fnm|jvm|shell)\]$/;
  const toolPattern = /^ {2}(\S+)(?:\s{2,}(.+))?$/;

  for (const line of output.split("\n").map((l) => l.replace(ANSI_PATTERN, ""))) {
    const typeMatch = line.match(typePattern);
    if (typeMatch?.[1]) {
      currentType = typeMatch[1];
    } else if (currentType) {
      const toolMatch = line.match(toolPattern);
      if (toolMatch?.[1]) {
        tools.push({
          details: toolMatch[2] || "",
          name: toolMatch[1],
          type: currentType,
        });
      }
    }
  }

  return tools;
}

function buildArgs(appName: string | undefined, options: ExecOptions = {}): string[] {
  const { args = [], beforeConfig = [], config = [], noAutoConfig = false } = options;

  const spawnArgs = ["exec"];

  for (const c of config) {
    spawnArgs.push("--config", c);
  }

  for (const bc of beforeConfig) {
    spawnArgs.push("--before-config", bc);
  }

  if (noAutoConfig) {
    spawnArgs.push("--no-auto-config");
  }

  if (appName) {
    spawnArgs.push(appName, ...args);
  }

  return spawnArgs;
}
