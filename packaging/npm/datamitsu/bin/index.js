#!/usr/bin/env node

import { spawn } from "node:child_process";

import { getExePath } from "../get-exe.js";

// Add --binary-command datamitsu to args if not already present
const args = process.argv.slice(2);
if (!args.includes("--binary-command")) {
  args.unshift("--binary-command", "datamitsu");
}

const child = spawn(getExePath(), args, { stdio: "inherit" });

child.on("exit", (code) => {
  if (code !== 0) {
    process.exit(code);
  }
});
