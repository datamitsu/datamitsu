// Generate the "Parser Catalog" docs page from `datamitsu devtools parsers list
// --json` output.
//
// The datamitsu core only emits JSON (its job is to introspect the WASM module);
// turning that JSON into a Markdown page is this script's job, run from
// `task build:parsers`. The website build just renders the committed Markdown — it
// never needs Rust, Go or the WASM module. No timestamp is emitted, so the page
// only changes when the parsers actually change.
//
//   datamitsu devtools parsers list --wasm <module>.wasm --json \
//     | tsx packaging/parsers-catalog.ts > website/docs/reference/parser-catalog.md

import process from "node:process";
import { pathToFileURL } from "node:url";

export interface CatalogTool {
  category?: string;
  columnUnit?: string;
  description: string;
  kind?: string;
  module: string;
  name: string;
  operations: Record<string, { args: string[]; stdin: boolean }>;
  // null for a module that predates the field (descriptor schema 1).
  severities?: null | string[];
  url: string;
  version: string;
}

// A module that predates the field says nothing about levels ("—"); an empty
// vocabulary is a tool that prints none.
const levels = (t: CatalogTool): string => {
  if (t.severities == null) {
    return "—";
  }
  return t.severities.length === 0 ? "none" : t.severities.map((s) => `\`${s}\``).join(", ");
};

export interface ParserCatalog {
  conflicts?: string[];
  tools: CatalogTool[];
}

// Escape a value for a Markdown table cell: collapse newlines, escape the table
// delimiter `|`, and escape `< { }` so Docusaurus's MDX parser never treats a
// description as JSX (a future tool's description could contain them).
const cell = (s: string): string => s.replaceAll("\n", " ").replaceAll(/([|<{}])/g, "\\$1");

export function renderCatalogMarkdown(cat: ParserCatalog): string {
  // Drop the internal `echo` pipe-test parser; sort for a stable page. Format
  // parsers read a standard shape any tool may print and get a table of their own.
  const sorted = (cat.tools ?? [])
    .filter((t) => t.name !== "echo")
    .sort((a, b) => a.name.localeCompare(b.name));
  const tools = sorted.filter((t) => t.kind !== "format");
  const formats = sorted.filter((t) => t.kind === "format");

  // The module version is deliberately absent from the page. It is injected at
  // build time (`DATAMITSU_PARSERS_VERSION`, falling back to the crate version),
  // so a contributor regenerating the page locally would rewrite it to `0.1.0`
  // and a release would rewrite it back — churn in a committed file that says
  // nothing a reader of the catalogue needs. `devtools parsers list` still
  // reports it, from the module itself.
  const module = cat.tools?.[0]?.module ?? "datamitsu-parsers";

  const lines: string[] = [
    "---",
    // A YAML frontmatter comment, not an HTML/MDX comment: Docusaurus treats `.md`
    // as MDX, where `<!-- -->` is a syntax error and `{/* */}` gets mangled by
    // prettier. Frontmatter is stripped before MDX parsing, and prettier's YAML
    // formatter preserves `#` comments — so this marker survives `dm fix`.
    "# AUTO-GENERATED — do not edit by hand. Regenerate with `task build:parsers`.",
    "title: Parser Catalog",
    "description: Tools whose output the bundled datamitsu WASM parser module turns into diagnostics",
    "---",
    "",
    ":::info Auto-generated",
    "This page is generated from the WASM parser module's `describe` output by",
    "`packaging/parsers-catalog.ts`, run from `task build:parsers`. Do not edit by hand.",
    ":::",
    "",
    `datamitsu ships a single signed Rust → WASM module (\`${module}\`)` +
      ` that turns these **${tools.length} tools**' raw output into structured ` +
      "diagnostics. Wire one to a tool with " +
      "[`outputParser`](./configuration-api.md#output-parser-outputparser) — the " +
      "**Parser** name below is its `parser` field.",
    "",
    "**Levels** are the level words the tool prints, which the parser maps onto error, " +
      "warning, info and hint; `none` means the tool prints no level " +
      "([how the core resolves a finding without one](../guides/architecture/parsers.md#levels)). " +
      "**Columns** is the unit the tool counts columns in, where it was measured. " +
      "**Category** marks a security scanner.",
    "",
    "| Parser | Modes | Levels | Columns | Category | Description | Upstream |",
    "| ------ | ----- | ------ | ------- | -------- | ----------- | -------- |",
  ];

  for (const t of tools) {
    const modes =
      Object.keys(t.operations ?? {})
        .sort((a, b) => a.localeCompare(b))
        .join(", ") || "—";
    const upstream = t.url ? `[link](${t.url})` : "—";
    lines.push(
      `| \`${t.name}\` | ${modes} | ${cell(levels(t))} | ${t.columnUnit || "—"} | ${t.category || "—"} | ` +
        `${cell(t.description)} | ${upstream} |`,
    );
  }

  if (formats.length > 0) {
    lines.push(
      "",
      "## Format parsers",
      "",
      "Many tools print a standard format on request. These parsers read one such shape " +
        "whatever tool printed it, under the same `outputParser.parser` field; " +
        "`fallback` tries them all in turn and answers with the first that recognizes the " +
        "output ([format parsers](../guides/architecture/parsers.md#format-parsers)). A format counts " +
        "columns in whatever unit the tool that printed it does, so none declares one.",
      "",
      "| Parser | Levels | Description | Specification |",
      "| ------ | ------ | ----------- | ------------- |",
    );
    for (const t of formats) {
      const spec = t.url ? `[link](${t.url})` : "—";
      lines.push(`| \`${t.name}\` | ${cell(levels(t))} | ${cell(t.description)} | ${spec} |`);
    }
  }

  return lines.join("\n") + "\n";
}

async function main(): Promise<void> {
  const chunks: Buffer[] = [];
  for await (const chunk of process.stdin) {
    chunks.push(chunk as Buffer);
  }
  const catalog = JSON.parse(Buffer.concat(chunks).toString("utf8")) as ParserCatalog;
  process.stdout.write(renderCatalogMarkdown(catalog));
}

// Run main only when executed directly (not when imported by the test).
if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  await main();
}
