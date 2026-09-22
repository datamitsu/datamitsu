import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import { test } from "node:test";

import { readLocation, viewURL } from "./embedding.ts";
import { type Manifest, readRoute, type Route } from "./model.ts";
import { select } from "./selection.ts";

const data: Manifest = {
  apps: [
    { dependsOn: [], description: "js linter", name: "eslint", runtime: "node", version: "9" },
    { dependsOn: [], description: "js formatter", name: "prettier", runtime: "node", version: "3" },
    { dependsOn: [], description: "py linter", name: "ruff", runtime: "binary", version: "0.6" },
    { dependsOn: [], description: "py types", name: "mypy", runtime: "python", version: "1" },
  ],
  managedConfigs: [
    {
      deleteOnly: false,
      name: "eslint.config.mjs",
      projectTypes: ["npm"],
      scope: "project",
      tools: ["eslint"],
    },
    {
      deleteOnly: false,
      name: "ruff.toml",
      projectTypes: ["python-package"],
      scope: "project",
      tools: ["ruff"],
    },
    { deleteOnly: false, name: ".editorconfig", projectTypes: [], scope: "repository", tools: [] },
  ],
  name: "datamitsu.config",
  projectTypes: [
    { description: "", id: "npm", markers: ["package.json"] },
    { description: "", id: "python-package", markers: ["pyproject.toml"] },
  ],
  schemaVersion: 2,
  tools: [
    {
      id: "eslint",
      name: "ESLint",
      operations: [
        {
          app: "eslint",
          excludeGlobs: [],
          globs: ["**/*.js"],
          kind: "lint",
          priority: 1,
          scope: "project",
        },
      ],
      projectTypes: ["npm"],
      skipped: false,
      skipReason: "",
    },
    {
      id: "prettier",
      name: "Prettier",
      operations: [
        {
          app: "prettier",
          excludeGlobs: [],
          globs: ["**/*"],
          kind: "fix",
          priority: 2,
          scope: "project",
        },
      ],
      projectTypes: ["npm"],
      skipped: false,
      skipReason: "",
    },
    {
      id: "ruff",
      name: "Ruff",
      operations: [
        {
          app: "ruff",
          excludeGlobs: [],
          globs: ["**/*.py"],
          kind: "lint",
          priority: 3,
          scope: "project",
        },
        {
          app: "ruff",
          excludeGlobs: [],
          globs: ["**/*.py"],
          kind: "fix",
          priority: 4,
          scope: "project",
        },
      ],
      projectTypes: ["python-package"],
      skipped: false,
      skipReason: "",
    },
    {
      id: "mypy",
      name: "mypy",
      operations: [
        {
          app: "mypy",
          excludeGlobs: [],
          globs: ["**/*.py"],
          kind: "lint",
          priority: 5,
          scope: "repository",
        },
      ],
      projectTypes: ["python-package"],
      skipped: true,
      skipReason: "opt-in",
    },
  ],
  version: "test",
};

const route = (query: string): Route => readRoute(`#/universe?${query}`, data);

test("one selection answers for every view and every counter", () => {
  // What each view renders, named the way the components read it: if a view were
  // to filter for itself again, these would stop agreeing.
  const rendered = (search: string) => {
    const selection = select(data, route(search));
    return {
      // Universe: lit points, the four counters, the strip, the legend, the directory.
      blueprintGroups: [
        selection.projectTypes.length,
        selection.tools.length,
        selection.apps.length,
        selection.managedConfigs.length,
      ],
      counters: [
        selection.apps.length,
        selection.tools.length,
        selection.projectTypes.length,
        selection.managedConfigs.length,
      ],
      directory: `${selection.apps.length} of ${selection.totals.apps}`,
      legendTotal: selection.families.reduce((total, family) => total + family.count, 0),
      lit: selection.apps.map((app) => app.name),
      // Operations: the table, its metrics and its footnote.
      operations: selection.tools.map((tool) => tool.id),
      runtimeBars: selection.families.map((family) => family.count),
    };
  };

  const everything = rendered("");
  assert.deepEqual(everything.lit, ["eslint", "prettier", "ruff", "mypy"]);
  assert.deepEqual(everything.counters, [4, 4, 2, 3]);
  assert.deepEqual(everything.blueprintGroups, [2, 4, 4, 3]);
  assert.equal(everything.legendTotal, 4);
  assert.equal(everything.directory, "4 of 4");

  const python = rendered("project=python-package");
  assert.deepEqual(python.lit, ["ruff", "mypy"]);
  assert.deepEqual(python.operations, ["ruff", "mypy"]);
  // Every counter follows: two apps, two tools, the one project type, and the
  // files that project keeps.
  assert.deepEqual(python.counters, [2, 2, 1, 2]);
  assert.deepEqual(python.blueprintGroups, [1, 2, 2, 2]);
  assert.equal(python.legendTotal, 2);
  assert.equal(python.directory, "2 of 4");
  // The strip keeps every family so the bar does not change shape, counting what
  // the project filter leaves: binary 1, node 0, python 1.
  assert.deepEqual(python.runtimeBars, [0, 1, 1]);

  const search = rendered("q=lint");
  assert.deepEqual(search.lit, ["eslint", "ruff"]);
  assert.equal(search.counters[0], 2);
  assert.equal(search.legendTotal, 2);
  // A search that lights an app keeps the tool that drives it: "lint" names
  // eslint by its id and ruff by the app it runs.
  assert.deepEqual(search.operations, ["eslint", "ruff"]);

  const node = rendered("runtime=node");
  assert.deepEqual(node.lit, ["eslint", "prettier"]);
  assert.deepEqual(node.operations, ["eslint", "prettier"]);
  assert.equal(node.counters[0], 2);
  // The legend keeps counting the other families under the remaining filters, so
  // following a different runtime is still an informed choice.
  assert.deepEqual(node.runtimeBars, [2, 1, 1]);

  const combined = rendered("project=python-package&runtime=binary&q=lint");
  assert.deepEqual(combined.lit, ["ruff"]);
  assert.deepEqual(combined.operations, ["ruff"]);
  assert.equal(combined.counters[0], 1);

  // Reset clears all three inputs, and every view returns to the whole snapshot.
  const reset = select(data, {
    ...route("project=python-package&runtime=node&q=lint"),
    project: "",
    q: "",
    runtime: "",
  });
  assert.deepEqual(
    reset.apps.map((app) => app.name),
    everything.lit,
  );
  assert.equal(reset.tools.length, everything.counters[1]);
});

test("a shared link and an embed carry all three filters into the same selection", () => {
  const selected = route("project=python-package&runtime=binary&q=lint");
  for (const isEmbedded of [false, true]) {
    const url = new URL(viewURL("https://example.test/atlas.html", selected, "auto", isEmbedded));
    // The published contract spells the project type `projectType`, in a shared
    // link and in an embed alike; both come back as the same selection.
    assert.equal(url.searchParams.get("projectType"), "python-package");
    assert.equal(url.searchParams.get("runtime"), "binary");
    assert.equal(url.searchParams.get("q"), "lint");
    const reopened = readLocation(url.search, "", data);
    assert.deepEqual(
      select(data, reopened).apps.map((app) => app.name),
      select(data, selected).apps.map((app) => app.name),
    );
  }
});

test("no view filters the snapshot for itself", async () => {
  // The guard that keeps a fourth widget from reintroducing the bug: a view may
  // look an app up by name, but every list, count and total comes from select().
  const forbidden = [
    /\bdata\.(apps|tools|projectTypes|managedConfigs)\.length\b/,
    /\bdata\.(apps|tools|managedConfigs)\.filter\(/,
    /\bmatchingApps\(/,
    /\bmatchingTools\(/,
  ];
  const directory = new URL(".", import.meta.url);
  const entries = await readdir(directory);
  const views = entries.filter((name) => name.endsWith(".svelte"));
  assert.ok(views.length >= 4, "expected the view components to be found");
  for (const view of views) {
    const source = await readFile(new URL(view, directory), "utf8");
    for (const pattern of forbidden) {
      assert.equal(
        pattern.test(source),
        false,
        `${view} derives its own selection (${pattern.source}); read select() instead`,
      );
    }
  }
});
