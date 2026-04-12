import type { PlanJSON, SpawnRaw } from "../types.js";

import { extractAllJSON } from "../json.js";
import { spawn } from "../spawn.js";
import { buildArgs, createToolCommand, type ToolCommandOptions } from "../tool-command.js";

export type CheckOptions = ToolCommandOptions;

export type CheckResult =
  | { error: string; exitCode: number; raw: SpawnRaw; success: false }
  | { exitCode: number; raw: SpawnRaw; success: true }
  | { output: string; raw: SpawnRaw; success: true }
  | { plan: PlanJSON; plans: PlanJSON[]; raw: SpawnRaw; success: true };

const baseCheck = createToolCommand("check");

export async function check(options: CheckOptions = {}): Promise<CheckResult> {
  if (options.explain !== "json") {
    return baseCheck(options) as Promise<CheckResult>;
  }

  const args = buildArgs("check", options);
  const spawnOptions: { cwd?: string; stdio?: "inherit" | "pipe" } = {};
  if (options.cwd !== undefined) {
    spawnOptions.cwd = options.cwd;
  }
  if (options.stdio !== undefined) {
    spawnOptions.stdio = options.stdio;
  }
  const raw = await spawn(args, spawnOptions);

  if (raw.failed) {
    return {
      error: raw.stderr || raw.stdout,
      exitCode: raw.exitCode,
      raw,
      success: false,
    };
  }

  try {
    const jsonStrings = extractAllJSON(raw.stdout);
    if (jsonStrings.length === 0) {
      throw new Error("No JSON found");
    }
    const plans: PlanJSON[] = jsonStrings.map((s) => JSON.parse(s));
    return { plan: plans[0]!, plans, raw, success: true };
  } catch {
    return {
      error: `Failed to parse JSON output: ${raw.stdout}`,
      exitCode: raw.exitCode,
      raw,
      success: false,
    };
  }
}
