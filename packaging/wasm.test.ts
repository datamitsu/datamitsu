import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import {
  buildParserManifest,
  parseChecksums,
  prepareWasmPackage,
  WASM_FILENAME,
  writeParserManifest,
} from "./wasm";

const SHA = "51cfb55ec44c586188f9c842a9f4531bba32c4be171ada8d8e6b36b955cfacf4";
const OTHER = "a".repeat(64);

function checksumsBody(wasmHash = SHA): string {
  return `${OTHER}  datamitsu_0.1.7_linux_amd64.tar.gz\n\n${wasmHash}  ${WASM_FILENAME}\n`;
}

test("parseChecksums maps base names to lowercase sha256, skipping blanks and markers", () => {
  const map = parseChecksums(`${OTHER.toUpperCase()}  out/${WASM_FILENAME}\n\n${SHA} *file.zip\n`);

  assert.equal(map.get(WASM_FILENAME), OTHER);
  assert.equal(map.get("file.zip"), SHA);
  assert.equal(map.size, 2);
});

test("buildParserManifest derives url+hash+version per parser from checksums.txt", () => {
  const manifest = buildParserManifest(checksumsBody(), "0.1.7");

  assert.deepEqual(Object.keys(manifest), [
    "echo",
    "cue_fmt",
    "dotenv_linter",
    "hadolint",
    "yamllint",
  ]);
  assert.equal(manifest.echo.hash, SHA);
  assert.equal(manifest.echo.version, "0.1.7");
  // Every dispatched parser shares the single module's url+hash+version.
  assert.equal(manifest.yamllint.hash, SHA);
  assert.equal(manifest.cue_fmt.version, "0.1.7");
  assert.equal(
    manifest.echo.url,
    `https://github.com/datamitsu/datamitsu/releases/download/v0.1.7/${WASM_FILENAME}`,
  );
});

test("buildParserManifest preserves an existing v-prefixed version in the tag", () => {
  const manifest = buildParserManifest(checksumsBody(), "v2.0.0-rc.1");
  assert.equal(
    manifest.echo.url,
    `https://github.com/datamitsu/datamitsu/releases/download/v2.0.0-rc.1/${WASM_FILENAME}`,
  );
});

test("buildParserManifest honours a custom repo and parser-name list", () => {
  const manifest = buildParserManifest(checksumsBody(), "1.0.0", {
    parserNames: ["echo", "hadolint"],
    repo: "acme/fork",
  });

  assert.deepEqual(Object.keys(manifest).sort(), ["echo", "hadolint"]);
  // All parsers share one module → identical url+hash.
  assert.equal(manifest.echo.hash, manifest.hadolint.hash);
  assert.equal(manifest.echo.url, manifest.hadolint.url);
  assert.match(manifest.echo.url, /^https:\/\/github\.com\/acme\/fork\//);
});

test("buildParserManifest throws when the WASM is absent from checksums.txt", () => {
  assert.throws(
    () => buildParserManifest(`${OTHER}  some_binary.tar.gz\n`, "0.1.7"),
    /not found in checksums\.txt/,
  );
});

test("buildParserManifest rejects a malformed (non-sha256) checksum", () => {
  assert.throws(
    () => buildParserManifest(`deadbeef  ${WASM_FILENAME}\n`, "0.1.7"),
    /not a 64-char sha256/,
  );
});

test("prepareWasmPackage bundles the .wasm and stamps the version", () => {
  const dir = mkdtempSync(join(tmpdir(), "wasm-pkg-"));
  try {
    const src = join(dir, "src.wasm");
    writeFileSync(src, Buffer.from([0x00, 0x61, 0x73, 0x6d, 1, 0, 0, 0]));

    const pkgDir = join(dir, "pkg");
    // Minimal package.json scaffold (the committed package carries the full one).
    mkdirSync(pkgDir, { recursive: true });
    writeFileSync(
      join(pkgDir, "package.json"),
      JSON.stringify({ name: "@datamitsu/datamitsu-wasm", version: "0.0.0" }),
    );

    prepareWasmPackage({ packageDir: pkgDir, version: "9.9.9", wasmSource: src });

    const bundled = readFileSync(join(pkgDir, WASM_FILENAME));
    assert.deepEqual([...bundled], [0x00, 0x61, 0x73, 0x6d, 1, 0, 0, 0]);

    const pkg = JSON.parse(readFileSync(join(pkgDir, "package.json"), "utf8"));
    assert.equal(pkg.version, "9.9.9");
  } finally {
    rmSync(dir, { force: true, recursive: true });
  }
});

test("prepareWasmPackage throws when the artifact was not built", () => {
  assert.throws(
    () =>
      prepareWasmPackage({
        packageDir: tmpdir(),
        version: "1.0.0",
        wasmSource: join(tmpdir(), "does-not-exist.wasm"),
      }),
    /WASM artifact not found/,
  );
});

test("writeParserManifest emits well-formed JSON whose hashes equal checksums.txt", () => {
  const dir = mkdtempSync(join(tmpdir(), "wasm-manifest-"));
  try {
    const dest = join(dir, "parser-manifest.json");
    const content = checksumsBody();

    const returned = writeParserManifest({
      checksumsContent: content,
      destPath: dest,
      version: "0.1.7",
    });

    const onDisk = JSON.parse(readFileSync(dest, "utf8"));
    assert.deepEqual(onDisk, returned);

    // Cross-check: the manifest hash equals the checksums.txt entry.
    const checksumHash = parseChecksums(content).get(WASM_FILENAME);
    for (const entry of Object.values(onDisk) as { hash: string }[]) {
      assert.equal(entry.hash, checksumHash);
    }
  } finally {
    rmSync(dir, { force: true, recursive: true });
  }
});
