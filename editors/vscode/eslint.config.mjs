import { join } from "node:path";

import { defineConfig } from "../../.datamitsu/eslint.config.mjs";
import packageJSON from "./package.json" with { type: "json" };

const config = await defineConfig(
  /**
   * @type {import("@shibanet0/datamitsu-config/type-fest").PackageJson}
   */ (packageJSON),
  [
    {
      rules: {
        "unicorn/consistent-class-member-order": "off",
        "unicorn/no-top-level-assignment-in-function": "off",
        "unicorn/prefer-await": "off",
      },
    },
  ],
  {
    plugins: {
      oxlint: {
        configFilePath: join(import.meta.dirname, ".oxlintrc.json"),
      },
    },
  },
);

export default config;
