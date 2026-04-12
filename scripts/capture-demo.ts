/**
 * Capture datamitsu check output as asciinema v2 .cast files.
 *
 * Usage (inside Docker only):
 *   node scripts/capture-demo.ts <repo-path> --output-dir <dir>
 *
 * Writes:
 *   <dir>/cold.cast  — cold start (cache cleared before run)
 *   <dir>/warm.cast  — cached run
 *
 * If either run exits non-zero, prints error and exits without writing any files.
 */

import { execSync, spawnSync } from "node:child_process";
import { existsSync, renameSync } from "node:fs";
import { resolve as resolvePath } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = fileURLToPath(new URL(".", import.meta.url));

async function main() {
  // Arg parsing
  const rawArgs = process.argv.slice(2);
  const outputDirIdx = rawArgs.indexOf("--output-dir");
  const outputDir =
    outputDirIdx === -1
      ? resolvePath(__dirname, "..", "website", "static")
      : resolvePath(rawArgs[outputDirIdx + 1]);
  const repoPath = rawArgs.find((a, i) => !a.startsWith("--") && i !== outputDirIdx + 1);

  if (!repoPath) {
    console.error("Usage: node scripts/capture-demo.ts <repo-path> [--output-dir <dir>]");
    process.exit(1);
  }

  const absRepo = resolvePath(repoPath);
  if (!existsSync(absRepo)) {
    console.error(`Repo path does not exist: ${absRepo}`);
    process.exit(1);
  }

  console.log(`Repo:       ${absRepo}`);
  console.log(`Output dir: ${outputDir}`);

  const coldPath = resolvePath(outputDir, "cold.cast");
  const warmPath = resolvePath(outputDir, "warm.cast");

  const env = {
    ...process.env,
    FORCE_COLOR: "1",
    npm_config_loglevel: "silent",
  };

  // ── Cold run ──────────────────────────────────────────────────────────────
  console.log("\n→ Clearing cache...");
  try {
    execSync("pnpm datamitsu cache clear", { cwd: absRepo, env, stdio: "inherit" });
  } catch {
    // OK if cache doesn't exist yet
  }

  console.log("→ Cold run...");
  const coldResult = spawnSync(
    "asciinema",
    [
      "rec",
      "--command",
      "pnpm datamitsu check",
      "--overwrite",
      "--title",
      "datamitsu check — cold start",
      `${coldPath}.tmp`,
    ],
    { cwd: absRepo, env, stdio: "inherit" },
  );

  if (coldResult.status !== 0) {
    console.error(`\nCold run failed (exit ${coldResult.status}) — no files written.`);
    process.exit(coldResult.status ?? 1);
  }

  // ── Warm run ──────────────────────────────────────────────────────────────
  console.log("→ Warm run...");
  const warmResult = spawnSync(
    "asciinema",
    [
      "rec",
      "--command",
      "pnpm datamitsu check",
      "--overwrite",
      "--title",
      "datamitsu check — cached",
      `${warmPath}.tmp`,
    ],
    { cwd: absRepo, env, stdio: "inherit" },
  );

  if (warmResult.status !== 0) {
    console.error(`\nWarm run failed (exit ${warmResult.status}) — no files written.`);
    process.exit(warmResult.status ?? 1);
  }

  // ── Rename atomically ─────────────────────────────────────────────────────
  renameSync(`${coldPath}.tmp`, coldPath);
  renameSync(`${warmPath}.tmp`, warmPath);

  console.log(`\nWritten:`);
  console.log(`  ${coldPath}`);
  console.log(`  ${warmPath}`);
}

try {
  await main();
} catch (error) {
  console.error(error);
  process.exit(1);
}
