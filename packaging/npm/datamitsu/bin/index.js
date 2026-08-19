#!/usr/bin/env node

import { spawnSync } from "node:child_process";

import { getExePath } from "../get-exe.js";

const arguments_ = process.argv.slice(2);
if (!arguments_.includes("--binary-command")) {
  arguments_.unshift("--binary-command", "datamitsu");
}

const result = spawnSync(getExePath(), arguments_, { stdio: "inherit" });

if (result.error) {
  throw result.error;
}

if (result.signal) {
  process.kill(process.pid, result.signal);
} else {
  process.exitCode = result.status ?? 1;
}
