/**
 * Capture the homepage terminal recordings as asciinema v2 .cast files. Usage (inside Docker only):
 * node scripts/capture-demo.ts <repo-path> --output-dir <dir> [--metadata <file>] Writes:
 * <dir>/cold.cast — a first run against an empty store <dir>/warm.cast — the same command again,
 * reusing that store <dir>/scope.cast — a narrowed run from a subdirectory, with its plan explained
 * <file> — what each recording ran, when, at what terminal size, and against which revision Nothing
 * is written unless all three runs succeed at the pinned terminal size.
 */

import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve as resolvePath } from "node:path";

const __dirname = import.meta.dirname;

interface Recording {
  command: string;
  cwd: string;
  name: string;
  title: string;
}

// The recorded terminal size. Wide enough that no line in any of the three runs
// wraps — the longest is the 81-column "skipped (opt-in: …)" row — with a few
// columns to spare, and narrow enough that the player still sets readable type
// inside the homepage card.
const terminalSize = { cols: 88, rows: 24 };

function argumentValue(rawArguments: string[], flag: string): string | undefined {
  const index = rawArguments.indexOf(flag);
  return index === -1 ? undefined : rawArguments[index + 1];
}

function castDuration(path: string): number {
  const lines = readFileSync(path, "utf8").trim().split("\n");
  const last = JSON.parse(lines.at(-1)!) as [number, string, string];
  return last[0];
}

function castHeader(path: string): { height: number; timestamp: number; width: number } {
  const [header] = readFileSync(path, "utf8").split("\n", 1);
  return JSON.parse(header!);
}

async function main() {
  const rawArguments = process.argv.slice(2);
  const outputDir = resolvePath(
    argumentValue(rawArguments, "--output-dir") ??
      resolvePath(__dirname, "..", "website", "static"),
  );
  // Defaults next to the casts because the capture runs in a container where
  // that directory is the one mounted from the repository.
  const metadataPath = resolvePath(
    argumentValue(rawArguments, "--metadata") ?? resolvePath(outputDir, "recordings.json"),
  );
  const repoPath = rawArguments.find(
    (value, index) => !value.startsWith("--") && rawArguments[index - 1]?.startsWith("--") !== true,
  );
  if (!repoPath) {
    console.error(
      "Usage: node scripts/capture-demo.ts <repo-path> [--output-dir <dir>] [--metadata <file>]",
    );
    process.exit(1);
  }
  const repo = resolvePath(repoPath);
  if (!existsSync(repo)) {
    console.error(`Repo path does not exist: ${repo}`);
    process.exit(1);
  }

  const store = mkdtempSync(join(tmpdir(), "datamitsu-demo-store-"));
  const environment = {
    ...process.env,
    DATAMITSU_CACHE_DIR: store,
    FORCE_COLOR: "1",
    npm_config_loglevel: "silent",
  };
  const target = narrowedTarget(repo);
  const narrowed = `pnpm datamitsu lint ${target.file} --tools prettier,syncpack --widen-to=target --explain`;
  const recordings: Recording[] = [
    {
      command: "pnpm datamitsu check",
      cwd: "",
      name: "cold",
      title: "datamitsu check — cold start",
    },
    { command: "pnpm datamitsu check", cwd: "", name: "warm", title: "datamitsu check — cached" },
    {
      command: narrowed,
      cwd: target.cwd,
      name: "scope",
      title: "datamitsu lint — narrowed plan",
    },
  ];

  console.log(`Repo:       ${repo}`);
  console.log(`Store:      ${store}`);
  console.log(`Output dir: ${outputDir}`);

  const revision = execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: repo,
    encoding: "utf8",
  }).trim();
  for (const recording of recordings) {
    console.log(`\n→ Recording ${recording.name}...`);
    const result = spawnSync(
      "asciinema",
      [
        "rec",
        "--command",
        recording.cwd ? `cd ${recording.cwd} && ${recording.command}` : recording.command,
        "--cols",
        String(terminalSize.cols),
        "--rows",
        String(terminalSize.rows),
        "--overwrite",
        "--title",
        recording.title,
        `${resolvePath(outputDir, `${recording.name}.cast`)}.tmp`,
      ],
      { cwd: repo, env: environment, stdio: "inherit" },
    );
    if (result.status !== 0) {
      console.error(`\n${recording.name} run failed (exit ${result.status}) — no files written.`);
      process.exit(result.status ?? 1);
    }
  }

  const headers = recordings.map((recording) =>
    castHeader(`${resolvePath(outputDir, `${recording.name}.cast`)}.tmp`),
  );
  if (
    headers.some(
      (header) => header.width !== terminalSize.cols || header.height !== terminalSize.rows,
    )
  ) {
    console.error(
      `\nRecordings are not ${terminalSize.cols}×${terminalSize.rows} — no files written.`,
    );
    process.exit(1);
  }

  const metadata: Record<string, unknown> = {};
  for (const [index, recording] of recordings.entries()) {
    const path = resolvePath(outputDir, `${recording.name}.cast`);
    const duration = castDuration(`${path}.tmp`);
    renameSync(`${path}.tmp`, path);
    metadata[recording.name] = {
      cols: terminalSize.cols,
      command: recording.command,
      cwd: recording.cwd,
      // The completed final screen is the poster: the run summary, which carries
      // the whole result and names no ecosystem, rather than an empty prompt or
      // the project types a particular repository happens to have. Half a second
      // past the last event, because seeking to its timestamp stops just short
      // of applying it.
      poster: `npt:${(duration + 0.5).toFixed(2)}`,
      recordedAt: new Date(headers[index]!.timestamp * 1000).toISOString().slice(0, 10),
      revision,
      rows: terminalSize.rows,
    };
    console.log(`  ${path}`);
  }
  writeFileSync(metadataPath, `${JSON.stringify(metadata, null, 2)}\n`);
  console.log(`  ${metadataPath}`);
}

/**
 * The narrowed run needs a real package directory: the one it names must exist in the repository
 * being recorded, not in the repository this script lives in.
 */
function narrowedTarget(repo: string): { cwd: string; file: string } {
  const candidates = execFileSync("sh", ["-c", "ls -d packages/*/ apps/*/ 2>/dev/null || true"], {
    cwd: repo,
    encoding: "utf8",
  })
    .split("\n")
    .filter(Boolean)
    .map((directory) => directory.replace(/\/$/, ""));
  for (const candidate of candidates) {
    for (const file of ["src/index.ts", "src/main.ts", "package.json"]) {
      if (existsSync(resolvePath(repo, candidate, file))) {
        return { cwd: candidate, file };
      }
    }
  }
  throw new Error("No workspace package with a file to lint was found in the recorded repository");
}

try {
  await main();
} catch (error) {
  console.error(error);
  process.exit(1);
}
