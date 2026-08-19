import { getExePath } from "@datamitsu/datamitsu/get-exe.js";
import { x } from "tinyexec";

import type { SpawnRaw } from "./types.js";

export interface SpawnOptions {
  cwd?: string;
  stdio?: "inherit" | "pipe";
}

export async function spawn(arguments_: string[], options: SpawnOptions = {}): Promise<SpawnRaw> {
  const { cwd, stdio = "pipe" } = options;

  const binaryPath = getExePath();
  const fullArguments = ["--binary-command", "datamitsu", ...arguments_];

  let result;
  try {
    result = await x(binaryPath, fullArguments, {
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
