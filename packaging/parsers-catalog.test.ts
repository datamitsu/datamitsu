import assert from "node:assert/strict";
import { test } from "node:test";

import { type ParserCatalog, renderCatalogMarkdown } from "./parsers-catalog";

const sample: ParserCatalog = {
  tools: [
    // echo must be excluded from the public catalog.
    {
      description: "pipe test",
      module: "datamitsu-parsers",
      name: "echo",
      operations: {},
      url: "",
      version: "0.1.0",
    },
    {
      description: "JS linter",
      module: "datamitsu-parsers",
      name: "eslint",
      operations: { lint: { args: ["--format", "json"], stdin: false } },
      url: "https://eslint.org",
      version: "0.1.0",
    },
    {
      description: "Dockerfile | linter", // contains a pipe → must be escaped
      module: "datamitsu-parsers",
      name: "hadolint",
      operations: { lint: { args: [], stdin: false } },
      url: "https://github.com/hadolint/hadolint",
      version: "0.1.0",
    },
  ],
};

test("renderCatalogMarkdown: frontmatter, auto-gen note, module + count", () => {
  const md = renderCatalogMarkdown(sample);
  assert.ok(md.startsWith("---\ntitle: Parser Catalog\n"), "has frontmatter");
  assert.match(md, /AUTO-GENERATED/);
  assert.match(md, /`datamitsu-parsers`, version `0\.1\.0`/);
  // echo is excluded, so the count is 2, not 3.
  assert.match(md, /\*\*2 tools\*\*/);
});

test("renderCatalogMarkdown: table rows, echo excluded, pipes escaped", () => {
  const md = renderCatalogMarkdown(sample);
  assert.ok(!md.includes("`echo`"), "echo excluded from the table");
  assert.match(md, /\| `eslint` \| lint \| JS linter \| \[link\]\(https:\/\/eslint\.org\) \|/);
  // The pipe inside hadolint's description is escaped so it doesn't break the table.
  assert.ok(md.includes("Dockerfile \\| linter"), "pipe escaped");
});

test("renderCatalogMarkdown: deterministic (no timestamp)", () => {
  assert.equal(renderCatalogMarkdown(sample), renderCatalogMarkdown(sample));
});

test("renderCatalogMarkdown: escapes MDX-hazard chars < { }", () => {
  const md = renderCatalogMarkdown({
    tools: [
      {
        description: "checks a < b and {block}",
        module: "m",
        name: "x",
        operations: {},
        url: "",
        version: "1",
      },
    ],
  });
  assert.ok(md.includes("checks a \\< b and \\{block\\}"), md);
});

test("renderCatalogMarkdown: tool with no operations shows an em dash", () => {
  const md = renderCatalogMarkdown({
    tools: [{ description: "d", module: "m", name: "x", operations: {}, url: "", version: "1" }],
  });
  assert.match(md, /\| `x` \| — \| d \| — \|/);
});
