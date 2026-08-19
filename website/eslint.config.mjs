import { join } from "node:path";

import { defineConfig } from "../.datamitsu/eslint.config.mjs";
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

// Lowered to warnings where the rule fights a React convention rather than
// finding a defect:
//
//   name-replacements — wants CodeCardProps renamed to CodeCardProperties, and
//     `e` for an event. `Props` is the React naming convention and renaming it
//     across the component tree would make the code read worse, not better.
//   prefer-await / set-state-in-effect — the Asciinema player loads its cast
//     inside an effect and settles state from the promise; restructuring that is
//     a component rewrite, not a cleanup.
export default downgrade(config, [
  "react-hooks/set-state-in-effect",
  "unicorn/name-replacements",
  "unicorn/prefer-await",
]);
