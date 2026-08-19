import { join } from "node:path";

import { defineConfig } from "../../.datamitsu/eslint.config.mjs";
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

// Brought in by the wrapper upgrade against pre-existing code. Silencing the
// break would mean extracting the JSON scanner's inner loops into functions,
// which is a rewrite of a parser rather than a cleanup.
export default downgrade(config, ["unicorn/no-break-in-nested-loop"]);
