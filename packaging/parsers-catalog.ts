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
  description: string;
  module: string;
  name: string;
  operations: Record<string, { args: string[]; stdin: boolean }>;
  url: string;
  version: string;
}

export interface ParserCatalog {
  conflicts?: string[];
  tools: CatalogTool[];
}

// Escape a value for a Markdown table cell: collapse newlines, escape the table
// delimiter `|`, and escape `< { }` so Docusaurus's MDX parser never treats a
// description as JSX (a future tool's description could contain them).
const cell = (s: string): string => s.replaceAll("\n", " ").replaceAll(/([|<{}])/g, "\\$1");

export function renderCatalogMarkdown(cat: ParserCatalog): string {
  // Drop the internal `echo` pipe-test parser; sort for a stable page.
  const tools = (cat.tools ?? [])
    .filter((t) => t.name !== "echo")
    .sort((a, b) => a.name.localeCompare(b.name));

  const module = cat.tools?.[0]?.module ?? "datamitsu-parsers";
  const version = cat.tools?.[0]?.version ?? "";

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
    `datamitsu ships a single signed Rust → WASM module (\`${module}\`${
      version ? `, version \`${version}\`` : ""
    }) that turns these **${tools.length} tools**' raw output into structured ` +
      "diagnostics. Wire one to a tool with " +
      "[`outputParser`](./configuration-api.md#output-parser-outputparser) — the " +
      "**Parser** name below is its `parser` field.",
    "",
    "| Parser | Modes | Description | Upstream |",
    "| ------ | ----- | ----------- | -------- |",
  ];

  for (const t of tools) {
    const modes =
      Object.keys(t.operations ?? {})
        .sort()
        .join(", ") || "—";
    const upstream = t.url ? `[link](${t.url})` : "—";
    lines.push(`| \`${t.name}\` | ${modes} | ${cell(t.description)} | ${upstream} |`);
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
