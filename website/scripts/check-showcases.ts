/* eslint-disable new-cap -- TypeBox names Value.Errors like a constructor. */

/**
 * Checks the shape of src/data/showcases.json and everything it points at. Usage: node
 * scripts/check-showcases.ts [--offline] [--write-schema] Automation checks shape, not intent: a
 * listing is still a maintainer's decision.
 */

import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { Value } from "typebox/value";

import type { ShowcaseFile } from "../src/data/showcase-schema.ts";

import { showcaseFileSchema } from "../src/data/showcase-schema.ts";

const curatedPath = resolve(import.meta.dirname, "..", "src", "data", "showcases.json");
const schemaPath = resolve(import.meta.dirname, "..", "static", "schemas", "showcase.schema.json");
const offline = process.argv.includes("--offline");
const failures: string[] = [];

// Keys are sorted recursively because the repository's JSON formatter sorts them:
// written in TypeBox's construction order, the artifact would be rewritten by the
// first `dm fix` and never match this check again.
const publishedSchema = `${JSON.stringify(sortKeys(showcaseFileSchema), null, 2)}\n`;
if (process.argv.includes("--write-schema")) {
  writeFileSync(schemaPath, publishedSchema);
  console.log(`Wrote ${schemaPath}`);
  process.exit(0);
}
if (readFileSync(schemaPath, "utf8") !== publishedSchema) {
  failures.push(
    "the published schema is stale; regenerate with: node scripts/check-showcases.ts --write-schema",
  );
}

const curated = JSON.parse(readFileSync(curatedPath, "utf8")) as ShowcaseFile;
// A value that matches no branch of a union reports once per branch, which would
// bury the one thing a contributor has to fix under eight identical lines.
const reported = new Set<string>();
for (const error of Value.Errors(showcaseFileSchema, curated)) {
  const where = (error as { instancePath?: string }).instancePath || "/";
  if (!reported.has(where)) {
    reported.add(where);
    failures.push(`${where}: ${error.message}${describe(where, curated)}`);
  }
}

const identifiers = new Set<string>();
for (const entry of curated.configs) {
  if (identifiers.has(entry.id)) {
    failures.push(`${entry.id}: duplicate id`);
  }
  identifiers.add(entry.id);
}
if (curated.configs.filter((entry) => entry.reference).length > 1) {
  failures.push("more than one entry is marked as the reference configuration");
}

if (!offline) {
  for (const entry of curated.configs) {
    for (const url of [...Object.values(entry.links), ...(entry.dataset ? [entry.dataset] : [])]) {
      await checkReachable(entry.id, url);
    }
    for (const option of entry.consume) {
      if (option.kind === "remote") {
        await checkHash(entry.id, option.url, option.hash);
      }
    }
    if (entry.dataset) {
      await checkDataset(entry.id, entry.dataset);
    }
  }
}

if (failures.length > 0) {
  console.error(`showcases.json is not ready to merge:\n  ${failures.join("\n  ")}`);
  process.exit(1);
}
console.log(`showcases.json: ${curated.configs.length} entries checked`);

async function checkDataset(id: string, url: string) {
  try {
    const response = await fetch(url, { redirect: "follow" });
    if (!response.ok) {
      failures.push(`${id}: dataset answered ${response.status}`);
      return;
    }
    const manifest = (await response.json()) as { apps?: unknown; schemaVersion?: unknown };
    if (!Array.isArray(manifest.apps) || typeof manifest.schemaVersion !== "number") {
      failures.push(`${id}: dataset is not a datamitsu inspector manifest`);
    }
  } catch (error) {
    failures.push(`${id}: dataset could not be read (${(error as Error).message})`);
  }
}

async function checkHash(id: string, url: string, expected: string) {
  try {
    const response = await fetch(url, { redirect: "follow" });
    if (!response.ok) {
      failures.push(`${id}: ${url} answered ${response.status}`);
      return;
    }
    const digest = createHash("sha256")
      .update(new Uint8Array(await response.arrayBuffer()))
      .digest("hex");
    if (digest !== expected) {
      failures.push(`${id}: ${url} hashes to ${digest}, the entry says ${expected}`);
    }
  } catch (error) {
    failures.push(`${id}: ${url} could not be verified (${(error as Error).message})`);
  }
}

async function checkReachable(id: string, url: string) {
  try {
    // Some hosts answer HEAD with 405 while serving GET perfectly well, so a
    // failed HEAD is retried rather than reported.
    let response = await fetch(url, { method: "HEAD", redirect: "follow" });
    if (!response.ok) {
      response = await fetch(url, { method: "GET", redirect: "follow" });
    }
    if (!response.ok) {
      failures.push(`${id}: ${url} answered ${response.status}`);
    }
  } catch (error) {
    failures.push(`${id}: ${url} is unreachable (${(error as Error).message})`);
  }
}

/**
 * Quotes the offending value, because "must be equal to constant" alone says nothing.
 */
function describe(path: string, data: unknown): string {
  const segments = path.split("/").filter(Boolean);
  let value: unknown = data;
  for (const segment of segments) {
    value = (value as Record<string, unknown>)?.[segment];
  }
  return value === undefined ? "" : ` (${JSON.stringify(value)})`;
}

/**
 * The same value with every object's keys in alphabetical order, so the published schema is written
 * the way the formatter would leave it.
 */
function sortKeys(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => sortKeys(item));
  }
  if (value === null || typeof value !== "object") {
    return value;
  }
  const source = value as Record<string, unknown>;
  return Object.fromEntries(
    Object.keys(source)
      // Compared by code point, not by locale: the formatter puts "$id" before
      // "additionalProperties", which a locale that ignores punctuation would not.
      .sort((first, second) => (first < second ? -1 : Number(first > second)))
      .map((key) => [key, sortKeys(source[key])]),
  );
}
