import assert from "node:assert/strict";
import { test } from "node:test";

import {
  buildInitializationOptions,
  describeEffectiveFormat,
  describeServedRoot,
  isExplicitlySet,
  type SettingInspection,
  type SettingsReader,
} from "./options";

// fakeConfig mimics WorkspaceConfiguration: inspect reports each level, get
// returns the merged value, with the contributed defaults underneath.
function fakeConfig(levels: Record<string, SettingInspection>): SettingsReader {
  const defaults: Record<string, unknown> = {
    "format.timeoutMs": 3000,
    "format.tools": {},
    "format.widenTo": "unit",
  };
  return {
    get: (section) => {
      const inspection = levels[section];
      return (
        inspection?.workspaceFolderValue ??
        inspection?.workspaceValue ??
        inspection?.globalValue ??
        defaults[section]
      );
    },
    inspect: (section) => ({ ...levels[section] }),
  };
}

// inspect() yields undefined for a section the configuration does not know.
const unknownSections: SettingsReader = { get: () => {}, inspect: () => {} };

test("isExplicitlySet: any of the user, workspace and folder levels counts", () => {
  assert.equal(isExplicitlySet(unknownSections.inspect("format.widenTo")), false);
  assert.equal(isExplicitlySet({}), false);
  assert.equal(isExplicitlySet({ globalValue: "target" }), true);
  assert.equal(isExplicitlySet({ workspaceValue: 0 }), true);
  assert.equal(isExplicitlySet({ workspaceFolderValue: {} }), true);
});

test("buildInitializationOptions: nothing set sends no options", () => {
  assert.equal(buildInitializationOptions(fakeConfig({})), undefined);
  assert.equal(buildInitializationOptions(unknownSections), undefined);
});

test("buildInitializationOptions: sends only the settings a user set", () => {
  assert.deepEqual(
    buildInitializationOptions(fakeConfig({ "format.widenTo": { globalValue: "target" } })),
    { format: { widenTo: "target" } },
  );
  assert.deepEqual(
    buildInitializationOptions(
      fakeConfig({
        "format.timeoutMs": { workspaceValue: 0 },
        "format.tools": { globalValue: { eslint: false, golangci: true } },
      }),
    ),
    { format: { timeoutMs: 0, tools: { eslint: false, golangci: true } } },
  );
});

test("buildInitializationOptions: a value equal to the default is still sent when set", () => {
  assert.deepEqual(
    buildInitializationOptions(fakeConfig({ "format.widenTo": { workspaceValue: "unit" } })),
    { format: { widenTo: "unit" } },
  );
});

test("buildInitializationOptions: forwards values as written for the server to validate", () => {
  assert.deepEqual(
    buildInitializationOptions(fakeConfig({ "format.widenTo": { globalValue: "repo" } })),
    { format: { widenTo: "repo" } },
  );
});

test("describeEffectiveFormat: renders the echoed policy, ignores anything else", () => {
  assert.equal(
    describeEffectiveFormat({
      datamitsu: { format: { timeoutMs: 3000, tools: {}, widenTo: "unit" } },
    }),
    `{"timeoutMs":3000,"tools":{},"widenTo":"unit"}`,
  );
  assert.equal(describeEffectiveFormat(null), undefined);
  assert.equal(describeEffectiveFormat({}), undefined);
  assert.equal(describeEffectiveFormat({ datamitsu: {} }), undefined);
  assert.equal(describeEffectiveFormat({ datamitsu: { format: "unit" } }), undefined);
});

test("describeServedRoot: returns the echoed root, ignores anything else", () => {
  assert.equal(
    describeServedRoot({ datamitsu: { format: {}, root: "/home/me/repo" } }),
    "/home/me/repo",
  );
  assert.equal(describeServedRoot(null), undefined);
  assert.equal(describeServedRoot({ datamitsu: { format: {} } }), undefined);
  assert.equal(describeServedRoot({ datamitsu: { root: "" } }), undefined);
  assert.equal(describeServedRoot({ datamitsu: { root: 42 } }), undefined);
});
