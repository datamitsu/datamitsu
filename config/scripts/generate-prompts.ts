#!/usr/bin/env node

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const __dirname = import.meta.dirname;

const CONFIG_ROOT = join(__dirname, "..");
const PROMPTS_DIR = join(CONFIG_ROOT, "src", "prompts");
const OUTPUT_FILE = join(PROMPTS_DIR, "generated.ts");

const PROMPTS = [
  {
    doc: "Agent guide for repositories that use a datamitsu configuration",
    exportName: "DATAMITSU_AGENT_GUIDE",
    file: "datamitsu-agent-guide.md",
  },
  {
    doc: "Agent guide for repositories that write a datamitsu configuration; opt-in only",
    exportName: "DATAMITSU_CONFIG_AUTHOR_GUIDE",
    file: "datamitsu-config-author-guide.md",
  },
];

function generatePromptsFile() {
  try {
    const exports = PROMPTS.map(({ doc, exportName, file }) => {
      const markdown = readFileSync(join(PROMPTS_DIR, file), "utf8");
      return `/**
 * ${doc}
 * Generated from: ${file}
 */
export const ${exportName} = \`${toTemplateLiteral(markdown)}\`;
`;
    });

    const tsContent = `// AUTO-GENERATED - DO NOT EDIT
// Run: task generate:prompts

${exports.join("\n")}`;

    mkdirSync(PROMPTS_DIR, { recursive: true });

    writeFileSync(OUTPUT_FILE, tsContent, "utf8");

    console.log("✓ Generated config/src/prompts/generated.ts");
  } catch (error) {
    console.error("✗ Failed to generate prompts file:", error);
    process.exit(1);
  }
}

function toTemplateLiteral(markdown: string): string {
  return markdown.replaceAll("\\", "\\\\").replaceAll("`", "\\`").replaceAll("$", "\\$");
}

generatePromptsFile();
