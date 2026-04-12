import {
  createToolCommand,
  type ToolCommandOptions,
  type ToolCommandResult,
} from "../tool-command.js";

export type LintOptions = ToolCommandOptions;
export type LintResult = ToolCommandResult;

export const lint = createToolCommand("lint");
