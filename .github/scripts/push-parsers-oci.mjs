#!/usr/bin/env node
/**
 * Publish the built WASM parser module as a standalone OCI artifact.
 *
 * The module used to ship exactly one way — an asset on a GitHub Release — which made it the single
 * artifact that always came from github.com over HTTPS, and therefore the hole in the OCI bundle's
 * whole promise of "mirror one registry and everything works". Unstable builds had it worse:
 * `goreleaser --snapshot` skips the release pipe entirely, so an unreleased module reached nothing
 * downstream at all.
 *
 * The artifact is deliberately tiny and boring: an OCI 1.1 image manifest with an artifactType, an
 * empty config blob, and exactly ONE uncompressed `application/wasm` layer. That last part is the
 * whole design — the layer digest IS the module's SHA-256, the same value the config already pins
 * as its mandatory `hash`, so a consumer rejects a substituted payload from the manifest alone,
 * before requesting a single byte of it.
 *
 * Written by hand against fetch rather than shelling out to oras/crane, because neither exists in
 * this repository's CI, and because this needs exact control over artifactType, the layer media
 * type and the annotations. The registry handshake mirrors the wrapper's proven
 * scripts/oci-bundle-postprocess.ts.
 *
 * Env: GITHUB_TOKEN (required), IS_STABLE, VERSION, UNSTABLE_TAG, GITHUB_SHA, GITHUB_OUTPUT,
 * GITHUB_STEP_SUMMARY, PARSERS_REGISTRY.
 */

import { createHash } from "node:crypto";
import { appendFileSync, readFileSync, writeFileSync } from "node:fs";

const ARTIFACT_TYPE = "application/vnd.datamitsu.parsers.v1+wasm";
const WASM_MEDIA_TYPE = "application/wasm";
const EMPTY_CONFIG_MEDIA_TYPE = "application/vnd.oci.empty.v1+json";
const MANIFEST_MEDIA_TYPE = "application/vnd.oci.image.manifest.v1+json";
const EMPTY_CONFIG_BODY = Buffer.from("{}", "utf8");

const REGISTRY = process.env.PARSERS_REGISTRY || "ghcr.io";
const STABLE_REPO = "datamitsu/datamitsu-parsers";
const UNSTABLE_REPO = "datamitsu/datamitsu-parsers-unstable";

/**
 * Minimal OCI registry v2 push client: bearer handshake, blob upload, manifest PUT.
 */
class Registry {
  constructor(registry, repo, token) {
    this.base = `https://${registry}/v2/${repo}`;
    this.repo = repo;
    this.token = undefined;
    this.password = token;
  }

  async authenticate(challenge) {
    const parameters = new Map();
    for (const part of challenge.replace(/^Bearer\s+/i, "").split(",")) {
      const [key, value] = part.split("=", 2);
      if (key && value) {
        parameters.set(key.trim(), value.trim().replaceAll('"', ""));
      }
    }
    const realm = parameters.get("realm");
    if (!realm) {
      throw new Error(`unsupported auth challenge: ${challenge}`);
    }
    const url = new URL(realm);
    const service = parameters.get("service");
    if (service) {
      url.searchParams.set("service", service);
    }
    // pull,push: the same token is reused for the blob uploads and the PUT.
    url.searchParams.set("scope", `repository:${this.repo}:pull,push`);

    const headers = {};
    if (this.password) {
      headers.Authorization = `Basic ${Buffer.from(`x-access-token:${this.password}`).toString("base64")}`;
    }
    const response = await fetch(url, { headers });
    if (!response.ok) {
      throw new Error(`token endpoint returned ${response.status}`);
    }
    const payload = await response.json();
    this.token = payload.token ?? payload.access_token;
    if (!this.token) {
      throw new Error("token endpoint returned no token");
    }
  }

  async getManifest(reference) {
    const response = await this.request(
      `/manifests/${reference}`,
      { method: "GET" },
      MANIFEST_MEDIA_TYPE,
    );
    if (!response.ok) {
      throw new Error(`GET manifest ${reference}: ${response.status} ${await response.text()}`);
    }
    return Buffer.from(await response.arrayBuffer());
  }

  /**
   * Uploads a blob unless the registry already has it. Returns its descriptor.
   */
  async putBlob(bytes, mediaType, annotations) {
    const digest = `sha256:${sha256Hex(bytes)}`;
    const head = await this.request(`/blobs/${digest}`, { method: "HEAD" });
    if (head.status !== 200) {
      const start = await this.request("/blobs/uploads/", { method: "POST" });
      if (start.status !== 202) {
        throw new Error(`POST blob upload: ${start.status} ${await start.text()}`);
      }
      const location = start.headers.get("location");
      if (!location) {
        throw new Error("blob upload returned no Location header");
      }
      const target = new URL(location, this.base + "/");
      target.searchParams.set("digest", digest);
      const put = await this.request(target.href, {
        body: bytes,
        headers: { "Content-Type": "application/octet-stream" },
        method: "PUT",
      });
      if (put.status !== 201) {
        throw new Error(`PUT blob ${digest}: ${put.status} ${await put.text()}`);
      }
    }
    const descriptor = { digest, mediaType, size: bytes.length };
    if (annotations) {
      descriptor.annotations = annotations;
    }
    return descriptor;
  }

  async putManifest(reference, bytes) {
    const response = await this.request(`/manifests/${reference}`, {
      body: bytes,
      headers: { "Content-Type": MANIFEST_MEDIA_TYPE },
      method: "PUT",
    });
    if (!response.ok) {
      throw new Error(`PUT manifest ${reference}: ${response.status} ${await response.text()}`);
    }
    return response.headers.get("docker-content-digest") ?? `sha256:${sha256Hex(bytes)}`;
  }

  async request(path, init, accept) {
    const url = path.startsWith("http") ? path : this.base + path;
    const send = async () => {
      const headers = new Headers(init.headers);
      if (accept) {
        headers.set("Accept", accept);
      }
      if (this.token) {
        headers.set("Authorization", `Bearer ${this.token}`);
      }
      return fetch(url, { ...init, headers });
    };
    let response = await send();
    if (response.status === 401) {
      const challenge = response.headers.get("www-authenticate");
      if (!challenge) {
        throw new Error("registry returned 401 without a challenge");
      }
      await this.authenticate(challenge);
      response = await send();
    }
    return response;
  }
}

function emitOutput(key, value) {
  if (process.env.GITHUB_OUTPUT) {
    appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${value}\n`);
  }
  console.log(`${key}=${value}`);
}

async function main() {
  const token = process.env.GITHUB_TOKEN;
  if (!token) {
    throw new Error("GITHUB_TOKEN is required to push to the registry");
  }

  const isStable = process.env.IS_STABLE === "true";
  const repo = isStable ? STABLE_REPO : UNSTABLE_REPO;
  const version = isStable
    ? (process.env.VERSION || "").replace(/^v/, "")
    : process.env.UNSTABLE_TAG || "";
  if (!version) {
    throw new Error(
      isStable
        ? "VERSION is required for a stable release"
        : "UNSTABLE_TAG is required for an unstable build",
    );
  }

  const { hash, name } = resolveModule("dist/checksums.txt");
  const module = readFileSync(`dist/${name}`);
  const actual = sha256Hex(module);
  if (actual !== hash) {
    throw new Error(`dist/${name} hashes ${actual}, but checksums.txt records ${hash}`);
  }

  const registry = new Registry(REGISTRY, repo, token);
  const configDescriptor = await registry.putBlob(EMPTY_CONFIG_BODY, EMPTY_CONFIG_MEDIA_TYPE);
  const layerDescriptor = await registry.putBlob(module, WASM_MEDIA_TYPE, {
    "com.datamitsu.kind": "parser",
    "com.datamitsu.parsers-version": version,
    "org.opencontainers.image.title": name,
  });

  // No created timestamp: two runs of the same commit then produce the same
  // manifest digest, which makes a re-run idempotent instead of publishing a
  // second manifest for identical content.
  const manifest = {
    annotations: {
      "com.datamitsu.parsers-version": version,
      "org.opencontainers.image.description": "datamitsu WASM output-parser module",
      "org.opencontainers.image.revision": process.env.GITHUB_SHA || "",
      "org.opencontainers.image.source": "https://github.com/datamitsu/datamitsu",
    },
    artifactType: ARTIFACT_TYPE,
    config: configDescriptor,
    layers: [layerDescriptor],
    mediaType: MANIFEST_MEDIA_TYPE,
    schemaVersion: 2,
  };
  const manifestBytes = Buffer.from(JSON.stringify(manifest), "utf8");

  const tag = version;
  const digest = await registry.putManifest(tag, manifestBytes);

  // Load-bearing, not belt-and-braces: the entire design rests on the layer
  // digest equalling the module's SHA-256. If a future producer change ever
  // gzips or tars the layer, this fails the release instead of failing every
  // consumer's pull — and it is also what binds this publication to the
  // cosign-signed checksums.txt the hash came from.
  const publishedBytes = await registry.getManifest(digest);
  const published = JSON.parse(publishedBytes.toString("utf8"));
  if (published.layers?.length !== 1) {
    throw new Error(`published manifest has ${published.layers?.length} layers, want exactly 1`);
  }
  if (published.layers[0].digest !== `sha256:${hash}`) {
    throw new Error(
      `published layer digest ${published.layers[0].digest} != sha256:${hash} from checksums.txt — ` +
        "the layer is not the module itself (compressed? tarred?), and every consumer would reject it",
    );
  }
  if (published.artifactType !== ARTIFACT_TYPE) {
    throw new Error(`published artifactType ${published.artifactType} != ${ARTIFACT_TYPE}`);
  }
  if (published.config?.mediaType !== EMPTY_CONFIG_MEDIA_TYPE) {
    throw new Error(
      `published config mediaType ${published.config?.mediaType} != ${EMPTY_CONFIG_MEDIA_TYPE}`,
    );
  }

  // `latest` only for a real stable release: an rc must never become the tag a
  // human resolves when they ask for the current module.
  if (isStable && !version.includes("-rc.")) {
    await registry.putManifest("latest", manifestBytes);
  }

  const reference = `${REGISTRY}/${repo}`;
  const record = { digest, module: "core", ref: reference, sha256: hash, tag, version };
  writeFileSync("dist/parsers-oci.json", `${JSON.stringify(record, null, 2)}\n`);

  emitOutput("digest", digest);
  emitOutput("ref", reference);
  emitOutput("sha256", hash);
  emitOutput("tag", tag);

  if (process.env.GITHUB_STEP_SUMMARY) {
    appendFileSync(
      process.env.GITHUB_STEP_SUMMARY,
      `### WASM parser module (OCI)\n\n` +
        `- artifact: \`${reference}@${digest}\`\n` +
        `- tag: \`${tag}\`\n` +
        `- module sha256: \`${hash}\` (also the layer digest)\n\n` +
        "Pin it in a config with:\n\n" +
        "```js\n" +
        `parsers: { core: { hash: "${hash}", oci: { ref: "${reference}", digest: "${digest}" } } }\n` +
        "```\n",
    );
  }
}

/**
 * Resolves the module's asset name from checksums.txt rather than re-templating it. `--snapshot`
 * mangles the version into `...-SNAPSHOT-<sha>`, and checksums.txt is the file cosign signs — so
 * reading the name from there is what keeps the published bytes and their signed entry describing
 * the same thing. Exactly one match is required: two would mean the release carries two modules and
 * picking either silently would publish an artifact whose name does not describe its content.
 */
function resolveModule(checksumsPath) {
  const lines = readFileSync(checksumsPath, "utf8").split("\n");
  const matches = [];
  for (const line of lines) {
    const [hash, name] = line.trim().split(/\s+/, 2);
    if (name && /^datamitsu_parsers.*\.wasm$/.test(name)) {
      matches.push({ hash, name });
    }
  }
  if (matches.length !== 1) {
    throw new Error(
      `expected exactly one WASM parser module in ${checksumsPath}, found ${matches.length}` +
        (matches.length > 0 ? `: ${matches.map((m) => m.name).join(", ")}` : ""),
    );
  }
  return matches[0];
}

function sha256Hex(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

try {
  await main();
} catch (error) {
  console.error(`error: ${error.message}`);
  process.exit(1);
}
