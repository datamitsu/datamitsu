import { cache } from "./commands/cache.js";
import { check } from "./commands/check.js";
import { exec } from "./commands/exec.js";
import { fix } from "./commands/fix.js";
import { lint } from "./commands/lint.js";
import { version } from "./commands/version.js";

export default { cache, check, exec, fix, lint, version };
export { cache } from "./commands/cache.js";
export type {
  CacheClearOptions,
  CacheClearResult,
  CachePathProjectOptions,
  CachePathResult,
} from "./commands/cache.js";
export { check } from "./commands/check.js";
export type { CheckOptions, CheckResult } from "./commands/check.js";
export { exec, parseToolList } from "./commands/exec.js";
export type { ExecOptions, ExecResult, ToolInfo } from "./commands/exec.js";
export { fix } from "./commands/fix.js";
export type { FixOptions, FixResult } from "./commands/fix.js";
export { lint } from "./commands/lint.js";
export type { LintOptions, LintResult } from "./commands/lint.js";
export { version } from "./commands/version.js";
export type { VersionResult } from "./commands/version.js";
export type { GroupJSON, ParallelGroupJSON, PlanJSON, SpawnRaw, TaskJSON } from "./types.js";
