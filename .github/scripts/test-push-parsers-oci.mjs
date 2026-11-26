import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

import { promoteNonstableChannel, Registry } from "./push-parsers-oci.mjs";

const SCRIPT = resolve(".github/scripts/push-parsers-oci.mjs");
const REGISTRY = process.env.PARSERS_REGISTRY || "ghcr.io";
const STABLE_REPO = "datamitsu/datamitsu-parsers";
const UNSTABLE_REPO = "datamitsu/datamitsu-parsers-unstable";
const PRE_CHANGE_MODULE = Buffer.from([0, 97, 115, 109, 1, 0, 0, 0]);
const PRE_CHANGE_MANIFEST = Buffer.from(
  '{"annotations":{"com.datamitsu.parsers-version":"0.0.0-unstable.45589539579.1","com.datamitsu.preparation-attempt":"1","com.datamitsu.preparation-run":"45589539579","org.opencontainers.image.description":"datamitsu WASM output-parser module","org.opencontainers.image.revision":"8f7f850fbed7d9beda1b15051d96a16561a20d79","org.opencontainers.image.source":"https://github.com/datamitsu/datamitsu"},"artifactType":"application/vnd.datamitsu.parsers.v1+wasm","config":{"digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","mediaType":"application/vnd.oci.empty.v1+json","size":2},"layers":[{"digest":"sha256:93a44bbb96c751218e4c00d479e4c14358122a389acca16205b1e4d0dc5f9476","mediaType":"application/wasm","size":8,"annotations":{"com.datamitsu.kind":"parser","com.datamitsu.parsers-version":"0.0.0-unstable.45589539579.1","org.opencontainers.image.title":"datamitsu_parsers_0.0.0-unstable.45589539579.1.wasm"}}],"mediaType":"application/vnd.oci.image.manifest.v1+json","schemaVersion":2}',
);

test("check-prepared accepts frozen pre-change preparation bytes", () => {
  const directory = mkdtempSync(join(tmpdir(), "parser-prepared-"));
  try {
    mkdirSync(join(directory, "dist"));
    const moduleName = "datamitsu_parsers_0.0.0-unstable.45589539579.1.wasm";
    writeFileSync(join(directory, "dist", moduleName), PRE_CHANGE_MODULE);
    const moduleHash = createHash("sha256").update(PRE_CHANGE_MODULE).digest("hex");
    writeFileSync(join(directory, "dist", "checksums.txt"), `${moduleHash}  ${moduleName}\n`);
    writeFileSync(join(directory, "dist", "parsers-oci-manifest.json"), PRE_CHANGE_MANIFEST);
    writeFileSync(
      join(directory, "dist", "parsers-oci.json"),
      `${JSON.stringify(
        {
          digest: `sha256:${createHash("sha256").update(PRE_CHANGE_MANIFEST).digest("hex")}`,
          module: "core",
          ref: `${REGISTRY}/${UNSTABLE_REPO}`,
          sha256: `sha256:${moduleHash}`,
          tag: "unstable-45589539579-1",
          version: "0.0.0-unstable.45589539579.1",
        },
        null,
        2,
      )}\n`,
    );
    const result = spawnSync(process.execPath, [SCRIPT, "check-prepared"], {
      cwd: directory,
      encoding: "utf8",
      env: {
        ...process.env,
        IS_STABLE: "false",
        PARSER_VERSION: "0.0.0-unstable.45589539579.1",
        PREPARATION_ATTEMPT: "1",
        PREPARATION_RUN_ID: "45589539579",
        PREPARATION_SHA: "8f7f850fbed7d9beda1b15051d96a16561a20d79",
        UNSTABLE_TAG: "unstable-45589539579-1",
      },
    });
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(
      readFileSync(join(directory, "dist", "parsers-oci-manifest.json")),
      PRE_CHANGE_MANIFEST,
    );
  } finally {
    rmSync(directory, { force: true, recursive: true });
  }
});

function artifact(version, run, attempt = 1) {
  return Buffer.from(
    JSON.stringify({
      annotations: {
        "com.datamitsu.parsers-version": version,
        "com.datamitsu.preparation-attempt": String(attempt),
        "com.datamitsu.preparation-run": String(run),
      },
    }),
  );
}

function record(version, tag, run, { attempt = 1, unstable = false } = {}) {
  const bytes = artifact(version, run, attempt);
  return {
    bytes,
    digest: `sha256:${createHash("sha256").update(bytes).digest("hex")}`,
    ref: `${REGISTRY}/${unstable ? UNSTABLE_REPO : STABLE_REPO}`,
    tag,
    version,
  };
}

function setup({ candidate, current, denied = false, missing = false } = {}) {
  const writes = [];
  const reads = [];
  const manifests = new Map();
  if (candidate) manifests.set(candidate.tag, candidate.bytes);
  if (current) {
    manifests.set(current.tag, current.bytes);
    manifests.set(current.channel, current.bytes);
  }
  const registry = {
    async getManifest(reference, options = {}) {
      reads.push(reference);
      if (denied && reference === (current?.channel ?? "preview")) {
        throw new Error("GET manifest denied: 403");
      }
      const bytes = manifests.get(reference);
      if (bytes) return bytes;
      if (options.allowMissing) return null;
      throw new Error(`GET manifest ${reference}: 404`);
    },
    async putManifest(reference, bytes) {
      writes.push(reference);
      manifests.set(reference, bytes);
      return `sha256:${createHash("sha256").update(bytes).digest("hex")}`;
    },
  };
  if (missing && candidate) manifests.delete(candidate.tag);
  return { reads, registry, writes };
}

test("preview tags promote in the ordinary parser repository", async () => {
  const candidate = record("0.0.0-preview.202611251830.12.1", "preview-202611251830-12-1", 12);
  const { registry, writes } = setup({ candidate });
  await promoteNonstableChannel(registry, candidate.bytes, candidate);
  assert.deepEqual(writes, ["preview"]);
});

test("unstable promotion is confined to its sibling parser package", async () => {
  const candidate = record("0.0.0-unstable.202611251830.12.1", "unstable-202611251830-12-1", 12, {
    unstable: true,
  });
  const { registry, writes } = setup({ candidate });
  await promoteNonstableChannel(registry, candidate.bytes, candidate);
  assert.deepEqual(writes, ["unstable"]);
});

test("older preparation publishes no alias and equal verified identity is a no-op", async () => {
  const candidate = record("0.0.0-preview.202611251830.10.1", "preview-202611251830-10-1", 10);
  const currentVersion = "0.0.0-preview.202611251830.11.1";
  const currentTag = "preview-202611251830-11-1";
  const current = {
    bytes: artifact(currentVersion, 11),
    channel: "preview",
    tag: currentTag,
  };
  const older = setup({ candidate, current });
  await promoteNonstableChannel(older.registry, candidate.bytes, candidate);
  assert.deepEqual(older.writes, []);

  const same = record(currentVersion, currentTag, 11);
  const equal = setup({ candidate: same, current });
  await promoteNonstableChannel(equal.registry, same.bytes, same);
  assert.deepEqual(equal.writes, []);
});

test("preparation attempt breaks ties within one run", async () => {
  const candidate = record("0.0.0-preview.202611251830.12.2", "preview-202611251830-12-2", 12, {
    attempt: 2,
  });
  const currentVersion = "0.0.0-preview.202611251830.12.1";
  const currentTag = "preview-202611251830-12-1";
  const current = {
    bytes: artifact(currentVersion, 12, 1),
    channel: "preview",
    tag: currentTag,
  };
  const { registry, writes } = setup({ candidate, current });
  await promoteNonstableChannel(registry, candidate.bytes, candidate);
  assert.deepEqual(writes, ["preview"]);
});

test("equal preparation identity with different bytes fails closed", async () => {
  const version = "0.0.0-preview.202611251830.11.1";
  const tag = "preview-202611251830-11-1";
  const candidate = record(version, tag, 11);
  const current = {
    bytes: Buffer.from(
      JSON.stringify({
        annotations: {
          "com.datamitsu.parsers-version": "0.0.0-preview.202611251829.11.1",
          "com.datamitsu.preparation-attempt": "1",
          "com.datamitsu.preparation-run": "11",
        },
        changed: true,
      }),
    ),
    channel: "preview",
    tag: "preview-202611251829-11-1",
  };
  const { registry, writes } = setup({ candidate, current });

  await assert.rejects(
    promoteNonstableChannel(registry, candidate.bytes, candidate),
    /same parser preparation identity/,
  );
  assert.deepEqual(writes, []);
});

test("missing immutable candidate and denied current alias are not treated as absence", async () => {
  const candidate = record("0.0.0-preview.202611251830.12.1", "preview-202611251830-12-1", 12);
  const missing = setup({ candidate, missing: true });
  await assert.rejects(
    promoteNonstableChannel(missing.registry, candidate.bytes, candidate),
    /immutable parser tag .* is absent/,
  );
  assert.deepEqual(missing.writes, []);

  const prior = {
    bytes: artifact("0.0.0-preview.202611251830.11.1", 11),
    channel: "preview",
    tag: "preview-202611251830-11-1",
  };
  const denied = setup({ candidate, current: prior, denied: true });
  await assert.rejects(promoteNonstableChannel(denied.registry, candidate.bytes, candidate), /403/);
  assert.deepEqual(denied.writes, []);
});

test("mismatched immutable content and unverified pointer provenance block promotion", async () => {
  const candidate = record("0.0.0-preview.202611251830.12.1", "preview-202611251830-12-1", 12);
  const mismatched = setup({ candidate: { ...candidate, bytes: Buffer.from("different") } });
  await assert.rejects(
    promoteNonstableChannel(mismatched.registry, candidate.bytes, candidate),
    /does not match the prepared bytes/,
  );
  assert.deepEqual(mismatched.writes, []);

  const current = {
    bytes: Buffer.from("not-json"),
    channel: "preview",
    tag: "preview-202611251830-11-1",
  };
  const unknown = setup({ candidate, current });
  await assert.rejects(promoteNonstableChannel(unknown.registry, candidate.bytes, candidate));
  assert.deepEqual(unknown.writes, []);
});
test("only an explicit MANIFEST_UNKNOWN 404 confirms registry absence", async () => {
  const originalFetch = globalThis.fetch;
  try {
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ errors: [{ code: "MANIFEST_UNKNOWN" }] }), { status: 404 });
    const registry = new Registry("ghcr.io", "datamitsu/datamitsu-parsers", "token");
    assert.equal(await registry.getManifest("preview", { allowMissing: true }), null);

    globalThis.fetch = async () =>
      new Response(JSON.stringify({ errors: [{ code: "DENIED" }] }), { status: 404 });
    await assert.rejects(
      registry.getManifest("preview", { allowMissing: true }),
      /GET manifest preview: 404/,
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
});
