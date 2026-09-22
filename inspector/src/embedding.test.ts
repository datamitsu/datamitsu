import assert from "node:assert/strict";
import { test } from "node:test";

import type { Manifest } from "./model.ts";

import {
  embedCode,
  embedMode,
  embedPreset,
  readLocation,
  themePreference,
  viewURL,
} from "./embedding.ts";
import { matchingApps, matchingTools } from "./selection.ts";

const data: Manifest = {
  apps: [{ dependsOn: [], description: "", name: "eslint", runtime: "node", version: "1" }],
  managedConfigs: [],
  name: "datamitsu.config",
  projectTypes: [{ description: "", id: "npm", markers: ["package.json"] }],
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
          scope: "repository",
        },
      ],
      projectTypes: ["npm"],
      skipped: false,
      skipReason: "",
    },
  ],
  version: "test",
};

test("published query contract opens a selected tool and filters with a system theme", () => {
  const route = readLocation(
    "?view=operations&tool=eslint&runtime=node&projectType=npm&embed=1",
    "",
    data,
  );
  assert.equal(route.tool, "eslint");
  assert.equal(route.project, "npm");
  assert.equal(route.runtime, "node");
  assert.equal(themePreference("?theme=auto"), "auto");
  assert.equal(themePreference("?theme=invalid"), "auto");
  assert.equal(matchingTools(data, route).length, 1);
  assert.equal(matchingTools(data, { ...route, runtime: "python" }).length, 0);
  assert.equal(matchingApps(data, { ...route, runtime: "python" }).length, 0);
});
test("old hash bookmarks take precedence and sharing preserves every selection on file URLs", () => {
  const route = readLocation(
    "?view=blueprints",
    "#/universe?app=eslint&layout=helix&runtime=node&q=a%26b",
    data,
  );
  assert.equal(route.view, "universe");
  const url = new URL(viewURL("file:///snapshots/config.html", route, "dark"));
  assert.equal(url.hash, "");
  assert.equal(url.searchParams.get("theme"), "dark");
  assert.deepEqual(readLocation(url.search, url.hash, data), route);
});
test("an embed snippet is the frame alone, with an escaped URL and no sandbox privileges", () => {
  const route = readLocation("?view=operations&tool=eslint&q=a%26b", "", data);
  const snippet = embedCode("https://example.test/sub/atlas.html", route, "auto");
  assert.ok(snippet.includes('sandbox="allow-scripts"'));
  assert.ok(snippet.includes('loading="lazy"'));
  assert.ok(snippet.includes("embed=operations"));
  assert.ok(snippet.includes("&amp;"));
  assert.ok(snippet.trimEnd().endsWith("</iframe>"));
  assert.equal(snippet.includes("<p>"), false);
});

test("a tool selected in Operations does not narrow app views after changing tabs", () => {
  const snapshot = {
    ...data,
    apps: [
      ...data.apps,
      { dependsOn: [], description: "", name: "ruff", runtime: "python", version: "1" },
    ],
  };
  const route = readLocation("?view=universe&tool=eslint", "", snapshot);
  assert.equal(matchingApps(snapshot, route).length, 2);
  const filtered = readLocation("?view=blueprints&runtime=python", "", snapshot);
  assert.equal(filtered.diagram, "runtimes");
  assert.deepEqual(
    matchingApps(snapshot, filtered).map((app) => app.name),
    ["ruff"],
  );
});

test("the minimal orbit preset stays an embed option and may open a tab from the frame", () => {
  assert.equal(embedPreset("?embed=universe&preset=minimal"), "minimal");
  assert.equal(embedPreset("?embed=universe&preset=compact"), "");
  assert.equal(embedPreset("?preset=minimal"), "minimal");
  const route = readLocation("?embed=universe&preset=minimal", "", data);
  const url = new URL(viewURL("https://example.test/atlas.html", route, "auto", true, "minimal"));
  assert.equal(url.searchParams.get("preset"), "minimal");
  assert.equal(new URL(viewURL(url.href, route, "auto")).searchParams.has("preset"), false);
  const snippet = embedCode("https://example.test/atlas.html", route, "auto", "minimal");
  assert.ok(snippet.includes("preset=minimal"));
  assert.ok(
    snippet.includes('sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"'),
  );
  assert.ok(
    embedCode("https://example.test/atlas.html", route, "auto").includes('sandbox="allow-scripts"'),
  );
});

test("Universe is the default and explicit embeds isolate a section even with an old hash", () => {
  assert.equal(readLocation("", "", data).view, "universe");
  for (const [mode, view] of [
    ["universe", "universe"],
    ["runtimes", "blueprints"],
    ["operations", "operations"],
  ]) {
    assert.equal(embedMode(`?embed=${mode}`), mode);
    assert.equal(readLocation(`?embed=${mode}`, "#/operations", data).view, view);
  }
  assert.equal(embedMode("?embed=bogus"), "");
  assert.equal(readLocation("?embed=1&view=blueprints", "", data).view, "blueprints");
});
