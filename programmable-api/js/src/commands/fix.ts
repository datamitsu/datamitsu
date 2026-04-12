import {
  createToolCommand,
  type ToolCommandOptions,
  type ToolCommandResult,
} from "../tool-command.js";

export type FixOptions = ToolCommandOptions;
export type FixResult = ToolCommandResult;

export const fix = createToolCommand("fix");
