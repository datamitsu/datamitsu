import { accessSync, constants, existsSync, statSync } from "node:fs";
import { delimiter, join } from "node:path";

import { downloadBundled, type DownloadDeps } from "./download";

export type BinaryMode = "auto" | "bundled" | "system";

export interface ResolveOptions {
  download?: (deps: DownloadDeps) => Promise<string>;
  // datamitsu.path setting (empty string when unset).
  explicitPath: string;
  log: (message: string) => void;
  // Injection seams for tests.
  lookPath?: (cmd: string) => string | undefined;
  mode: BinaryMode;
  // Extension global-storage dir the bundled binary is cached in.
  storageDir: string;
}

// findInPath returns the first PATH directory containing an executable named cmd
// (honoring Windows executable extensions), or undefined.
export function findInPath(cmd: string): string | undefined {
  const exts = process.platform === "win32" ? [".exe", ".cmd", ".bat", ""] : [""];
  for (const dir of (process.env.PATH ?? "").split(delimiter)) {
    if (dir === "") {
      continue;
    }
    for (const ext of exts) {
      const candidate = join(dir, cmd + ext);
      if (isExecutableFile(candidate)) {
        return candidate;
      }
    }
  }
  return undefined;
}

// resolveBinary picks the datamitsu binary to run the language server:
//   datamitsu.path (if set)  >  binaryMode (auto | system | bundled).
// auto prefers a PATH binary and falls back to the pinned download; system
// requires a PATH binary; bundled always uses the pinned download.
export async function resolveBinary(opts: ResolveOptions): Promise<string> {
  const explicit = opts.explicitPath.trim();
  if (explicit !== "") {
    if (!existsSync(explicit)) {
      throw new Error(`datamitsu.path points to a missing file: ${explicit}`);
    }
    return explicit;
  }

  const lookPath = opts.lookPath ?? findInPath;
  const download = opts.download ?? downloadBundled;
  const system = lookPath("datamitsu");

  switch (opts.mode) {
    case "auto": {
      return system ?? download({ log: opts.log, storageDir: opts.storageDir });
    }
    case "bundled": {
      return download({ log: opts.log, storageDir: opts.storageDir });
    }
    case "system": {
      if (system === undefined) {
        throw new Error(
          'datamitsu not found on PATH (binaryMode "system"). Install it or set datamitsu.path.',
        );
      }
      return system;
    }
  }
}

// isExecutableFile reports whether p is a regular file with an executable bit, so
// a directory or non-executable file named like the binary is not mistaken for it
// (which would also wrongly suppress auto-mode's fall-through to the download).
function isExecutableFile(p: string): boolean {
  try {
    if (!statSync(p).isFile()) {
      return false;
    }
    accessSync(p, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}
