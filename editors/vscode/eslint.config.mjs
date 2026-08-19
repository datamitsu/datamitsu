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

// The wrapper upgrade brought these rules in against pre-existing code. They ask
// for structural changes to the extension's lifecycle, not cleanups, so they are
// lowered to warnings here rather than silently ignored:
//
//   no-top-level-assignment-in-function — the output channel and the in-flight
//     download promise are module singletons by design; VS Code activates once
//     and both must outlive the activate() call.
//   prefer-await — the same activation path, whose chaining is deliberate.
//   consistent-class-member-order — directly contradicts perfectionist/sort-classes,
//     which is also enabled; one of the two has to yield.
export default downgrade(config, [
  "unicorn/consistent-class-member-order",
  "unicorn/no-top-level-assignment-in-function",
  "unicorn/prefer-await",
]);
