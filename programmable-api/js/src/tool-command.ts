import type { PlanJSON, SpawnRaw } from "./types.js";

import { extractJSON } from "./json.js";
import { spawn } from "./spawn.js";

export interface BaseOptions {
  beforeConfig?: string[];
  config?: string[];
  explain?: "detailed" | "json" | "summary" | "text" | boolean;
  files?: string[];
  fileScoped?: boolean;
  noAutoConfig?: boolean;
  tools?: string[];
}

export interface ToolCommandOptions extends BaseOptions {
  cwd?: string;
  stdio?: "inherit" | "pipe";
}

export type ToolCommandResult =
  | { error: string; exitCode: number; raw: SpawnRaw; success: false }
  | { exitCode: number; raw: SpawnRaw; success: true }
  | { output: string; raw: SpawnRaw; success: true }
  | { plan: PlanJSON; raw: SpawnRaw; success: true };

export function buildArguments(commandName: string, options: BaseOptions = {}): string[] {
  const {
    beforeConfig = [],
    config = [],
    explain = false,
    files = [],
    fileScoped = false,
    noAutoConfig = false,
    tools = [],
  } = options;

  const arguments_: string[] = [commandName];

  if (explain === true) {
    arguments_.push("--explain");
  } else if (explain) {
    arguments_.push(`--explain=${explain}`);
  }

  if (fileScoped) {
    arguments_.push("--file-scoped");
  }

  if (tools.length > 0) {
    arguments_.push("--tools", tools.join(","));
  }

  for (const c of config) {
    arguments_.push("--config", c);
  }

  for (const bc of beforeConfig) {
    arguments_.push("--before-config", bc);
  }

  if (noAutoConfig) {
    arguments_.push("--no-auto-config");
  }

  arguments_.push(...files);

  return arguments_;
}

export function createToolCommand(
  commandName: string,
): (options?: ToolCommandOptions) => Promise<ToolCommandResult> {
  return async function (options: ToolCommandOptions = {}): Promise<ToolCommandResult> {
    const { explain = false } = options;
    const arguments_ = buildArguments(commandName, options);
    const spawnOptions: { cwd?: string; stdio?: "inherit" | "pipe" } = {};
    if (options.cwd !== undefined) {
      spawnOptions.cwd = options.cwd;
    }
    if (options.stdio !== undefined) {
      spawnOptions.stdio = options.stdio;
    }
    const raw = await spawn(arguments_, spawnOptions);

    if (raw.failed) {
      return {
        error: raw.stderr || raw.stdout,
        exitCode: raw.exitCode,
        raw,
        success: false,
      };
    }

    if (explain === "json") {
      try {
        const jsonString = extractJSON(raw.stdout) || raw.stdout;
        const plan: PlanJSON = JSON.parse(jsonString);
        return { plan, raw, success: true };
      } catch {
        return {
          error: `Failed to parse JSON output: ${raw.stdout}`,
          exitCode: raw.exitCode,
          raw,
          success: false,
        };
      }
    }

    if (explain) {
      return { output: raw.stdout, raw, success: true };
    }

    return { exitCode: raw.exitCode, raw, success: true };
  };
}
