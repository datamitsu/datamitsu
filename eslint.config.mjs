import { join } from "node:path";

import { defineConfig } from "./.datamitsu/eslint.config.mjs";
import packageJSON from "./package.json" with { type: "json" };

const config = await defineConfig(
  /**
   * @type {import("@shibanet0/datamitsu-config/type-fest").PackageJson}
   */ (packageJSON),
  undefined,
  {
    plugins: {
      oxlint: {
        configFilePath: join(import.meta.dirname, ".oxlintrc.json"),
      },
    },
  },
);

/**
 * Return the flat config with the given rules lowered to "warn". Rewrites the blocks that already
 * declare each rule, because a fresh block would have to re-register the plugin the rule comes
 * from.
 *
 * @param {readonly unknown[]} flatConfig
 * @param {readonly string[]} ruleIds
 * @returns {unknown[]}
 */
function downgrade(flatConfig, ruleIds) {
  return flatConfig.map((block) => {
    const rules =
      /**
       * @type {{ rules?: Record<string, unknown> }}
       */ (block).rules;
    if (rules === undefined) {
      return block;
    }
    const lowered = Object.fromEntries(
      Object.entries(rules).map(([id, level]) => [id, ruleIds.includes(id) ? "warn" : level]),
    );
    return { ...block, rules: lowered };
  });
}

// Lowered to warnings where the rule fights a published contract rather than
// finding a defect:
//
//   no-global-object-property-assignment — config/src/main.ts assigns
//     globalThis.getConfig and globalThis.getMinVersion because that is the
//     config file format; the loader looks for exactly those globals.
//   name-replacements — wants the injected rel() helper renamed. Its name is
//     part of the API user configs are written against.
export default downgrade(config, [
  "unicorn/name-replacements",
  "unicorn/no-global-object-property-assignment",
]);
