import { getExePath } from "@datamitsu/datamitsu/get-exe.js";
import { x } from "tinyexec";

import type { SpawnRaw } from "./types.js";

export interface SpawnOptions {
  cwd?: string;
  stdio?: "inherit" | "pipe";
}

export async function spawn(args: string[], options: SpawnOptions = {}): Promise<SpawnRaw> {
  const { cwd, stdio = "pipe" } = options;

  const binaryPath = getExePath();
  const fullArgs = ["--binary-command", "datamitsu", ...args];

  let result;
  try {
    result = await x(binaryPath, fullArgs, {
      nodeOptions: { cwd, stdio },
      throwOnError: false,
    });
  } catch (error) {
    throw new Error(
      `Failed to execute datamitsu binary at ${binaryPath}: ${(error as Error).message}`,
      {
        cause: error,
      },
    );
  }

  return {
    exitCode: result.exitCode ?? 1,
    failed: result.exitCode !== 0,
    stderr: result.stderr ?? "",
    stdout: result.stdout ?? "",
  };
}
