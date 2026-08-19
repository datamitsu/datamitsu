export interface GroupJSON {
  parallelGroups: ParallelGroupJSON[];
  priority: number;
}

export interface ParallelGroupJSON {
  canRunInParallel: boolean;
  tasks: TaskJSON[];
}

export interface PlanJSON {
  cwdPath: string;
  groups: GroupJSON[];
  operation: string;
  rootPath: string;
  /**
   * Tools that will not run, and why. Always present; empty when none.
   */
  skipped: SkippedToolJSON[];
}

export interface SkippedToolJSON {
  detail?: string;
  operation: string;
  reason: "config" | "not-narrowable" | "unsupported-platform";
  toolName: string;
}

export interface SpawnRaw {
  exitCode: number;
  failed: boolean;
  stderr: string;
  stdout: string;
}

export interface TaskJSON {
  app: string;
  args: string[];
  arity: string;
  coverage: string;
  excludeGlobs?: string[];
  fileCount: number;
  files: string[];
  globs?: string[];
  granularity: string;
  scope: string;
  toolName: string;
  workingDir: string;
}
