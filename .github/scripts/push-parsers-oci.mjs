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
    this.origin = `https://${registry}`;
    this.base = `${this.origin}/v2/${repo}`;
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
      throw new Error("unsupported registry authentication challenge");
    }
    const url = this.registryURL(realm);
    if (url.pathname !== "/token") throw new Error("unexpected registry token endpoint");
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
    const response = await fetch(url, { headers, redirect: "error" });
    if (!response.ok) {
      throw new Error(`token endpoint returned ${response.status}`);
    }
    const payload = await response.json();
    this.token = payload.token ?? payload.access_token;
    if (!this.token) {
      throw new Error("token endpoint returned no token");
    }
  }

  async getManifest(reference, { accept = MANIFEST_MEDIA_TYPE, allowMissing = false } = {}) {
    const response = await this.request(`/manifests/${reference}`, { method: "GET" }, accept);
    if (allowMissing && response.status === 404) {
      const body = await response
        .clone()
        .json()
        .catch(() => null);
      if (body?.errors?.length && body.errors.every(({ code }) => code === "MANIFEST_UNKNOWN")) {
        return null;
      }
    }
    if (!response.ok) {
      throw new Error(`GET manifest ${reference}: ${response.status} ${await response.text()}`);
    }
    return Buffer.from(await response.arrayBuffer());
  }

  /**
   * Uploads a blob unless the registry already has it.
   */
  async putBlob(bytes) {
    const digest = `sha256:${sha256Hex(bytes)}`;
    const head = await this.request(`/blobs/${digest}`, { method: "HEAD" });
    if (head.status !== 200 && head.status !== 404) {
      throw new Error(`HEAD blob ${digest}: ${head.status}`);
    }
    if (head.status !== 200) {
      const start = await this.request("/blobs/uploads/", { method: "POST" });
      if (start.status !== 202) {
        throw new Error(`POST blob upload: ${start.status} ${await start.text()}`);
      }
      const location = start.headers.get("location");
      if (!location) {
        throw new Error("blob upload returned no Location header");
      }
      const target = this.registryURL(location, this.base + "/");
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
  }

  async putManifest(reference, bytes) {
    const response = await this.request(`/manifests/${reference}`, {
      body: bytes,
      headers: { "Content-Type": MANIFEST_MEDIA_TYPE },
      method: "PUT",
    });
    if (response.status !== 201) {
      throw new Error(`PUT manifest ${reference}: ${response.status} ${await response.text()}`);
    }
    return response.headers.get("docker-content-digest");
  }

  registryURL(value, base) {
    const url = new URL(value, base);
    if (url.origin !== this.origin || url.username || url.password) {
      throw new Error("unexpected registry URL origin or credentials");
    }
    return url;
  }

  async request(path, init, accept) {
    const url = this.registryURL(path.startsWith("/") ? this.base + path : path);
    const send = async () => {
      const headers = new Headers(init.headers);
      if (accept) {
        headers.set("Accept", accept);
      }
      if (this.token) {
        headers.set("Authorization", `Bearer ${this.token}`);
      }
      return fetch(url, { ...init, headers, redirect: "error" });
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
  if (process.argv[2] === "lookup-image") {
    const tag = process.argv[3];
    if (!/^unstable-[1-9]\d*-[1-9]\d*(?:-alpine)?$/.test(tag ?? "")) {
      throw new Error("invalid unstable image tag");
    }
    const token = process.env.GITHUB_TOKEN;
    if (!token) throw new Error("GITHUB_TOKEN is required for image lookup");
    const registry = new Registry("ghcr.io", "datamitsu/datamitsu-unstable", token);
    const raw = await registry.getManifest(tag, {
      accept: [
        "application/vnd.oci.image.index.v1+json",
        "application/vnd.docker.distribution.manifest.list.v2+json",
        MANIFEST_MEDIA_TYPE,
        "application/vnd.docker.distribution.manifest.v2+json",
      ].join(", "),
      allowMissing: true,
    });
    console.log(JSON.stringify(raw === null ? null : `sha256:${sha256Hex(raw)}`));
    return;
  }
  const { hash, isStable, manifestBytes, module, record, repo } = prepare();
  const recordText = `${JSON.stringify(record, null, 2)}\n`;
  if (process.argv[2] === "prepare") {
    writeFileSync("dist/parsers-oci-manifest.json", manifestBytes);
    writeFileSync("dist/parsers-oci.json", recordText);
    return;
  }
  const promote = process.argv[2] === "promote-stable";
  const check = process.argv[2] === "check-prepared";
  if (process.argv[2] !== "publish" && !promote && !check) {
    throw new Error("expected prepare, check-prepared, publish, or promote-stable");
  }
  if (promote && (!isStable || record.version.includes("-rc."))) {
    throw new Error("only stable releases may promote the latest parser channel");
  }
  const prepared = readFileSync("dist/parsers-oci-manifest.json");
  if (
    !prepared.equals(manifestBytes) ||
    readFileSync("dist/parsers-oci.json", "utf8") !== recordText
  ) {
    throw new Error("prepared parser manifest or pin does not match the checked inputs");
  }
  if (check) return;
  const token = process.env.GITHUB_TOKEN;
  if (!token) throw new Error("GITHUB_TOKEN is required to push to the registry");
  const { digest, ref: reference, sha256: pin, tag } = record;
  const registry = new Registry(REGISTRY, repo, token);
  if (promote) {
    if (!(await registry.getManifest(tag)).equals(prepared)) {
      throw new Error("versioned parser manifest does not match the prepared bytes");
    }
    const current = await registry.getManifest("latest", { allowMissing: true });
    if (current?.equals(prepared)) {
      console.log(`Parser latest already points to ${digest}`);
      return;
    }
    if (current) {
      const annotations = JSON.parse(current.toString("utf8")).annotations;
      const currentVersion = annotations?.["com.datamitsu.parsers-version"];
      if (
        !/^v\d+\.\d+\.\d+$/.test(currentVersion ?? "") ||
        !(await registry.getManifest(currentVersion.slice(1))).equals(current)
      ) {
        throw new Error("cannot verify the current parser latest pointer");
      }
      const [run, attempt] = preparationOrder(annotations);
      const [candidateRun, candidateAttempt] = preparationOrder(JSON.parse(prepared).annotations);
      if (run > candidateRun || (run === candidateRun && attempt > candidateAttempt)) {
        console.log(`Parser latest stays at newer preparation sha256:${sha256Hex(current)}`);
        return;
      }
      if (run === candidateRun && attempt === candidateAttempt) {
        throw new Error("same parser preparation identity has different manifest bytes");
      }
    }
    if ((await registry.putManifest("latest", prepared)) !== digest) {
      throw new Error(`registry did not promote the prepared digest ${digest}`);
    }
    if (!(await registry.getManifest("latest")).equals(prepared)) {
      throw new Error("latest does not resolve to the prepared manifest after promotion");
    }
    console.log(`Promoted parser latest to ${digest}`);
    return;
  }
  const existing = await registry.getManifest(tag, { allowMissing: true });
  if (existing === null) {
    await registry.putBlob(EMPTY_CONFIG_BODY);
    await registry.putBlob(module);
    if ((await registry.putManifest(tag, prepared)) !== digest) {
      throw new Error(`registry did not report the prepared digest ${digest}`);
    }
  } else if (!existing.equals(prepared)) {
    throw new Error(`parser tag ${tag} already exists with different manifest bytes`);
  }

  // Load-bearing, not belt-and-braces: the entire design rests on the layer
  // digest equalling the module's SHA-256. If a future producer change ever
  // gzips or tars the layer, this fails the release instead of failing every
  // consumer's pull — and it is also what binds this publication to the
  // cosign-signed checksums.txt the hash came from.
  const publishedBytes = await registry.getManifest(digest);
  if (!publishedBytes.equals(prepared))
    throw new Error("registry returned different manifest bytes");
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

  emitOutput("digest", digest);
  emitOutput("ref", reference);
  emitOutput("sha256", pin);
  emitOutput("tag", tag);

  if (process.env.GITHUB_STEP_SUMMARY) {
    appendFileSync(
      process.env.GITHUB_STEP_SUMMARY,
      `### WASM parser module (OCI)\n\n` +
        `- artifact: \`${reference}@${digest}\`\n` +
        `- tag: \`${tag}\`\n` +
        `- module sha256: \`sha256:${hash}\` (also the layer digest)\n\n` +
        "Pin it in a config with:\n\n" +
        "```js\n" +
        `parsers: { core: { hash: "sha256:${hash}", oci: { ref: "${reference}", digest: "${digest}" } } }\n` +
        "```\n",
    );
  }
}

function preparationOrder(annotations) {
  return ["com.datamitsu.preparation-run", "com.datamitsu.preparation-attempt"].map((key) => {
    const value = annotations?.[key];
    if (typeof value !== "string" || !/^[1-9]\d*$/.test(value)) {
      throw new Error(`missing or invalid parser preparation identity: ${key}`);
    }
    return BigInt(value);
  });
}

function prepare() {
  const isStable = process.env.IS_STABLE === "true";
  const repo = isStable ? STABLE_REPO : UNSTABLE_REPO;
  const version = isStable ? process.env.VERSION || "" : process.env.PARSER_VERSION || "";
  const tag = isStable ? version.replace(/^v/, "") : process.env.UNSTABLE_TAG || "";
  if (!version || !tag) throw new Error("parser version and OCI tag are required");

  const { hash, name } = resolveModule("dist/checksums.txt");
  const expectedName = `datamitsu_parsers_${isStable ? tag : version}.wasm`;
  if (name !== expectedName) throw new Error(`expected parser release asset ${expectedName}`);
  const module = readFileSync(`dist/${name}`);
  const actual = sha256Hex(module);
  // Both sides are published checksums of a public artifact, not secrets: there is
  // nothing for a timing difference to leak, and an integrity check must stay exact.
  // eslint-disable-next-line security/detect-possible-timing-attacks
  if (actual !== hash) {
    throw new Error(`dist/${name} hashes ${actual}, but checksums.txt records ${hash}`);
  }

  const descriptor = (bytes, mediaType) => ({
    digest: `sha256:${sha256Hex(bytes)}`,
    mediaType,
    size: bytes.length,
  });
  const configDescriptor = descriptor(EMPTY_CONFIG_BODY, EMPTY_CONFIG_MEDIA_TYPE);
  const layerDescriptor = {
    ...descriptor(module, WASM_MEDIA_TYPE),
    annotations: {
      "com.datamitsu.kind": "parser",
      "com.datamitsu.parsers-version": version,
      "org.opencontainers.image.title": name,
    },
  };
  const sourceSHA = process.env.PREPARATION_SHA ?? process.env.GITHUB_SHA;
  if (!/^[0-9a-f]{40}$/.test(sourceSHA ?? "")) {
    throw new Error("missing or invalid parser preparation SHA");
  }
  const order = preparationOrder({
    "com.datamitsu.preparation-attempt":
      process.env.PREPARATION_ATTEMPT ?? process.env.GITHUB_RUN_ATTEMPT,
    "com.datamitsu.preparation-run": process.env.PREPARATION_RUN_ID ?? process.env.GITHUB_RUN_ID,
  });
  const manifest = {
    // Preparation order travels with the registry pointer, including across retries.
    annotations: {
      "com.datamitsu.parsers-version": version,
      "com.datamitsu.preparation-attempt": String(order[1]),
      "com.datamitsu.preparation-run": String(order[0]),
      "org.opencontainers.image.description": "datamitsu WASM output-parser module",
      "org.opencontainers.image.revision": sourceSHA,
      "org.opencontainers.image.source": "https://github.com/datamitsu/datamitsu",
    },
    artifactType: ARTIFACT_TYPE,
    config: configDescriptor,
    layers: [layerDescriptor],
    mediaType: MANIFEST_MEDIA_TYPE,
    schemaVersion: 2,
  };
  const manifestBytes = Buffer.from(JSON.stringify(manifest), "utf8");

  const record = {
    digest: `sha256:${sha256Hex(manifestBytes)}`,
    module: "core",
    ref: `${REGISTRY}/${repo}`,
    sha256: `sha256:${hash}`,
    tag,
    version,
  };
  return { hash, isStable, manifestBytes, module, record, repo };
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
