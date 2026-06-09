#!/usr/bin/env node

import { spawnSync } from "node:child_process";

import { getExePath } from "../get-exe.js";

const args = process.argv.slice(2);
if (!args.includes("--binary-command")) {
  args.unshift("--binary-command", "datamitsu");
}

const result = spawnSync(getExePath(), args, { stdio: "inherit" });

if (result.error) {
  throw result.error;
}

if (result.signal) {
  process.kill(process.pid, result.signal);
} else {
  process.exitCode = result.status ?? 1;
}
