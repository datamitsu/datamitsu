/**
 * Rebuilds src/data/showcases.generated.json from the listed configurations. Usage: node
 * scripts/refresh-showcases.ts [--force] It never evaluates a third-party config and never runs
 * `init`: composition comes only from a published dataset. An entry that fails keeps the values of
 * its last good refresh and records what went wrong, so one unreachable repository cannot empty the
 * page.
 */

import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

import type { ShowcaseDerived, ShowcaseFile } from "../src/data/showcase-schema.ts";

// A manual rerun must not refetch what a scheduled run has just fetched; the
// weekly schedule sets the pace, this only keeps a rerun cheap.
const minimumIntervalDays = 6;
const maximumDatasetBytes = 4 * 1024 * 1024;

const dataDirectory = resolve(import.meta.dirname, "..", "src", "data");
const curatedPath = resolve(dataDirectory, "showcases.json");
const generatedPath = resolve(dataDirectory, "showcases.generated.json");

const force = process.argv.includes("--force");
const curated = JSON.parse(readFileSync(curatedPath, "utf8")) as ShowcaseFile;
const previous = readGenerated();
const refreshed: Record<string, ShowcaseDerived> = {};

for (const entry of curated.configs) {
  const last = previous[entry.id];
  if (!force && last && !isDue(last.fetchedAt)) {
    console.log(`${entry.id}: fetched ${last.fetchedAt}, still fresh`);
    refreshed[entry.id] = last;
    continue;
  }
  const errors: string[] = [];
  const repository = await readRepository(entry.links.repository, last?.repository, errors);
  const dataset = entry.dataset
    ? await readDataset(entry.dataset, last?.dataset, errors)
    : undefined;
  refreshed[entry.id] = {
    composition: dataset?.composition ?? (entry.dataset ? last?.composition : undefined),
    dataset: dataset?.observed ?? last?.dataset,
    errors,
    fetchedAt: new Date().toISOString(),
    repository: repository ?? last?.repository,
  };
  console.log(`${entry.id}: ${errors.length > 0 ? errors.join("; ") : "refreshed"}`);
}

writeFileSync(generatedPath, `${JSON.stringify(sortKeys(refreshed), null, 2)}\n`);
console.log(`Wrote ${generatedPath}`);

function githubHeaders(etag?: string): Record<string, string> {
  const headers: Record<string, string> = {
    accept: "application/vnd.github+json",
    "user-agent": "datamitsu-showcase-refresh",
  };
  // A conditional request that answers 304 costs no rate limit at all, which is
  // what keeps a weekly job on an unauthenticated runner viable.
  if (etag) {
    headers["if-none-match"] = etag;
  }
  const token = process.env.GITHUB_TOKEN;
  if (token) {
    headers.authorization = `Bearer ${token}`;
  }
  return headers;
}

function isDue(fetchedAt: string): boolean {
  const age = Date.now() - new Date(fetchedAt).getTime();
  return !Number.isFinite(age) || age >= minimumIntervalDays * 24 * 60 * 60 * 1000;
}

async function readDataset(
  url: string,
  last: ShowcaseDerived["dataset"],
  errors: string[],
): Promise<
  | undefined
  | { composition?: ShowcaseDerived["composition"]; observed?: ShowcaseDerived["dataset"] }
> {
  try {
    const headers: Record<string, string> = { "user-agent": "datamitsu-showcase-refresh" };
    if (last?.etag) {
      headers["if-none-match"] = last.etag;
    }
    const response = await fetch(url, { headers });
    if (response.status === 304) {
      return undefined;
    }
    if (!response.ok) {
      errors.push(`dataset: answered ${response.status}`);
      return undefined;
    }
    const body = await response.text();
    if (body.length > maximumDatasetBytes) {
      errors.push(`dataset: larger than ${maximumDatasetBytes} bytes`);
      return undefined;
    }
    const manifest = JSON.parse(body) as {
      apps?: { runtime?: string }[];
      capture?: { capturedAt?: string };
      schemaVersion?: number;
    };
    if (!Array.isArray(manifest.apps) || typeof manifest.schemaVersion !== "number") {
      errors.push("dataset: not a datamitsu inspector manifest");
      return undefined;
    }
    const runtimes: Record<string, number> = {};
    for (const app of manifest.apps) {
      const runtime = app.runtime ?? "unknown";
      runtimes[runtime] = (runtimes[runtime] ?? 0) + 1;
    }
    return {
      composition: { apps: manifest.apps.length, runtimes: sortKeys(runtimes) },
      observed: {
        capturedAt: manifest.capture?.capturedAt,
        etag: response.headers.get("etag") ?? undefined,
        // Recorded rather than pinned: the diff of this file is where a changed
        // dataset becomes visible to a human.
        sha256: createHash("sha256").update(body).digest("hex"),
      },
    };
  } catch (error) {
    errors.push(`dataset: ${(error as Error).message}`);
    return undefined;
  }
}

function readGenerated(): Record<string, ShowcaseDerived> {
  try {
    return JSON.parse(readFileSync(generatedPath, "utf8")) as Record<string, ShowcaseDerived>;
  } catch {
    return {};
  }
}

async function readLatestRelease(
  owner: string,
  name: string,
  errors: string[],
): Promise<string | undefined> {
  const response = await fetch(`https://api.github.com/repos/${owner}/${name}/releases/latest`, {
    headers: githubHeaders(),
  });
  if (response.status === 404) {
    return undefined;
  }
  if (!response.ok) {
    errors.push(`release: GitHub answered ${response.status}`);
    return undefined;
  }
  const release = (await response.json()) as { published_at?: string };
  return release.published_at;
}

async function readRepository(
  url: string,
  last: ShowcaseDerived["repository"],
  errors: string[],
): Promise<ShowcaseDerived["repository"]> {
  const match = /^https:\/\/github\.com\/([^/]+)\/([^/]+?)(?:\.git)?\/?$/.exec(url);
  if (!match) {
    return last;
  }
  const [, owner, name] = match;
  try {
    const response = await fetch(`https://api.github.com/repos/${owner}/${name}`, {
      headers: githubHeaders(last?.etag),
    });
    if (response.status === 304) {
      return last;
    }
    if (!response.ok) {
      errors.push(`repository: GitHub answered ${response.status}`);
      return last;
    }
    const repository = (await response.json()) as { pushed_at?: string; stargazers_count?: number };
    const release = await readLatestRelease(owner!, name!, errors);
    return {
      etag: response.headers.get("etag") ?? undefined,
      lastCommit: repository.pushed_at,
      lastRelease: release ?? last?.lastRelease,
      stars: repository.stargazers_count,
    };
  } catch (error) {
    errors.push(`repository: ${(error as Error).message}`);
    return last;
  }
}

function sortKeys<T>(values: Record<string, T>): Record<string, T> {
  return Object.fromEntries(Object.entries(values).sort(([a], [b]) => a.localeCompare(b)));
}
