#!/usr/bin/env node

import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

const CONFIG_ROOT = join(__dirname, "..");
const PROMPTS_DIR = join(CONFIG_ROOT, "src", "prompts");
const INPUT_FILE = join(PROMPTS_DIR, "datamitsu-agent-guide.md");
const OUTPUT_FILE = join(PROMPTS_DIR, "generated.ts");

function generatePromptsFile() {
  try {
    const markdownContent = readFileSync(INPUT_FILE, "utf8");

    const escapedContent = markdownContent
      .replaceAll("\\", "\\\\")
      .replaceAll("`", "\\`")
      .replaceAll("$", "\\$");

    const contentHash = createHash("sha256").update(markdownContent).digest("hex");

    const tsContent = `// AUTO-GENERATED - DO NOT EDIT
// Generated from: datamitsu-agent-guide.md
// Run: pnpm generate:prompts

/**
 * Standard agent prompt content for AGENTS.md
 * This content is distributed via sharedStorage to wrapper packages
 */
export const DATAMITSU_AGENT_GUIDE = \`${escapedContent}\`;

/**
 * SHA-256 hash of the agent prompt content
 * Used as bundle version for automatic cache invalidation
 */
export const DATAMITSU_AGENT_GUIDE_HASH = "${contentHash}";
`;

    mkdirSync(PROMPTS_DIR, { recursive: true });

    writeFileSync(OUTPUT_FILE, tsContent, "utf8");

    console.log("✓ Generated config/src/prompts/generated.ts");
  } catch (error) {
    console.error("✗ Failed to generate prompts file:", error);
    process.exit(1);
  }
}

generatePromptsFile();
