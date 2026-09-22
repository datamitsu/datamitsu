import assert from "node:assert/strict";
import { test } from "node:test";

import { isToolMatching, type Manifest, officialUrlLabel, readRoute, routeHash } from "./model.ts";
const data: Manifest = {
  apps: [{ dependsOn: [], description: "", name: "formatter", runtime: "node", version: "" }],
  managedConfigs: [],
  name: "datamitsu.config",
  projectTypes: [{ description: "", id: "npm", markers: ["package.json"] }],
  schemaVersion: 2,
  tools: [
    {
      id: "format",
      name: "Formatter",
      operations: [
        {
          app: "formatter",
          excludeGlobs: [],
          globs: ["**/*.ts"],
          kind: "fix",
          priority: 3,
          scope: "per-project",
        },
      ],
      projectTypes: ["npm"],
      skipped: false,
      skipReason: "",
    },
  ],
  version: "test",
};
test("hash routes survive export paths, Unicode searches and every view setting", () => {
  const route = readRoute(
    "#/universe?layout=helix&app=formatter&project=npm&runtime=node&q=a%26b%20%C3%A9&list=cards&operation=fix&status=enabled&section=metadata&diagram=runtimes",
    data,
  );
  assert.deepEqual(readRoute(routeHash(route), data), route);
  assert.equal(route.q, "a&b é");
  assert.equal(route.view, "universe");
  assert.equal(route.layout, "helix");
});
test("unknown hash sections reset and invalid selections remain unselected", () => {
  const route = readRoute(
    "#/bad?app=missing&tool=missing&project=missing&status=bad&section=bad",
    data,
  );
  assert.equal(route.view, "universe");
  assert.equal(route.app, "");
  assert.equal(route.tool, "");
  assert.equal(route.project, "");
  assert.equal(route.status, "all");
  assert.equal(route.section, "overview");
});
test("filters combine project applicability, status, kind and text", () => {
  const route = readRoute(
    "#/operations?project=npm&operation=fix&status=enabled&q=formatter",
    data,
  );
  assert.equal(isToolMatching(data.tools[0]!, route), true);
  assert.equal(isToolMatching({ ...data.tools[0]!, skipped: true }, route), false);
  assert.equal(isToolMatching(data.tools[0]!, { ...route, operation: "lint" }), false);
  assert.equal(isToolMatching(data.tools[0]!, { ...route, project: "python" }), false);
  assert.equal(
    isToolMatching({ ...data.tools[0]!, projectTypes: [] }, { ...route, project: "python" }),
    true,
  );
});

test("opening a view without a selection does not open a detail drawer", () => {
  const route = readRoute("#/operations", data);
  assert.equal(route.tool, "");
  assert.equal(route.app, "");
  assert.equal(routeHash(route).includes("tool="), false);
});

test("an app's link is labelled by what it actually is", () => {
  const app = { dependsOn: [], description: "", name: "eslint", runtime: "node", version: "9" };
  assert.equal(officialUrlLabel(app), "");
  // The configuration named a page: that is documentation, whatever its host.
  assert.equal(
    officialUrlLabel({ ...app, officialUrl: "https://eslint.org/docs/latest/" }),
    "Documentation",
  );
  // Derived links say what they are, so nothing promises more than it is.
  for (const [url, label] of [
    ["https://www.npmjs.com/package/eslint", "Package"],
    ["https://pypi.org/project/ruff/", "Package"],
    ["https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck", "Package"],
    ["https://central.sonatype.com/artifact/com.pinterest.ktlint/ktlint-cli", "Package"],
    ["https://github.com/koalaman/shellcheck", "Repository"],
    ["https://codeberg.org/dnkl/foot", "Repository"],
    ["https://example.com/tool", "Homepage"],
  ] as const) {
    assert.equal(
      officialUrlLabel({ ...app, officialUrl: url, officialUrlDerived: true }),
      label,
      url,
    );
  }
});
