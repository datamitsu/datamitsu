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
  batch: boolean;
  fileCount: number;
  files: string[];
  globs: string[];
  scope: string;
  toolName: string;
  workingDir: string;
}
