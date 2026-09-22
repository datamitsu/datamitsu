import { join } from "node:path";

import { defineConfig } from "../.datamitsu/eslint.config.mjs";
import packageJSON from "./package.json" with { type: "json" };
export default await defineConfig(packageJSON, [], {
  plugins: { oxlint: { configFilePath: join(import.meta.dirname, ".oxlintrc.json") } },
});
