// WASM parser packaging
//
// The Rust→WASM parser modules are delivered two ways from a single release:
//
//   1. As a platform-independent npm package (@datamitsu/datamitsu-wasm) that
//      bundles the built `.wasm` and exposes its on-disk path via get-wasm.js.
//   2. As a release-time parser manifest ({ name -> { url, hash, version } })
//      that config maintainers import to obtain each parser's url+hash.
//
// Both reference the SAME single `.wasm` (one module dispatches every parser by
// name). Its integrity is covered by the signed `checksums.txt` — the SHA-256 in
// the manifest equals the `checksums.txt` entry, so no per-version hash is
// embedded in the core binary (revise.txt §5.2).

import { cpSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";

// Basename of the built WASM artifact. Single source for the filename — must
// match `task build:parsers` output and the `.goreleaser.yml` extra_files globs.
export const WASM_FILENAME = "datamitsu_parsers.wasm";

// Parser names dispatched inside the single WASM module. Each Rust tool module in
// parsers/datamitsu-parsers/src/tools adds its name here so the release manifest
// advertises it. All names share one url+hash because they live in one module.
// (The authoritative, richer list — with versions and invocation recipes — comes
// from the module's `describe` export; this is just the manifest's name set.)
export const PARSER_NAMES: readonly string[] = [
  "echo",
  "actionlint",
  "alex",
  "ansiblelint",
  "bean_check",
  "bslint",
  "buf",
  "buildifier",
  "cfn_lint",
  "checkmake",
  "checkstyle",
  "clazy",
  "clj_kondo",
  "cmake_lint",
  "codespell",
  "commitlint",
  "cppcheck",
  "credo",
  "cue_fmt",
  "deadnix",
  "djlint",
  "dotenv_linter",
  "editorconfig_checker",
  "erb_lint",
  "fish",
  "gccdiag",
  "gdlint",
  "gitleaks",
  "gitlint",
  "glslc",
  "golangci_lint",
  "hadolint",
  "haml_lint",
  "ktlint",
  "kube_linter",
  "ltrs",
  "markdownlint",
  "markdownlint_cli2",
  "markuplint",
  "mdl",
  "mlint",
  "mypy",
  "npm_groovy_lint",
  "opacheck",
  "opentofu_validate",
  "perlimports",
  "phpcs",
  "phpmd",
  "phpstan",
  "pmd",
  "proselint",
  "protolint",
  "puppet_lint",
  "pydoclint",
  "pylint",
  "qmllint",
  "reek",
  "regal",
  "revive",
  "rpmspec",
  "rstcheck",
  "rubocop",
  "saltlint",
  "selene",
  "semgrep",
  "solhint",
  "spectral",
  "sqlfluff",
  "sqruff",
  "staticcheck",
  "statix",
  "stylint",
  "swiftlint",
  "teal",
  "terraform_validate",
  "terragrunt_validate",
  "textidote",
  "textlint",
  "tfsec",
  "tidy",
  "trivy",
  "twigcs",
  "vacuum",
  "vale",
  "verilator",
  "vint",
  "write_good",
  "yamllint",
  "zsh",
];

// Default GitHub repo the release assets are published under.
export const DEFAULT_REPO = "datamitsu/datamitsu";

export type ParserManifest = Record<string, ParserManifestEntry>;

export interface ParserManifestEntry {
  hash: string;
  url: string;
  version: string;
}

// Build the parser manifest from a `checksums.txt` body. The WASM's hash MUST be
// present (a missing entry means the parsers build / goreleaser checksum step did
// not run) and a well-formed 64-char sha256.
export function buildParserManifest(
  checksumsContent: string,
  version: string,
  options?: { parserNames?: readonly string[]; repo?: string; wasmFilename?: string },
): ParserManifest {
  const repo = options?.repo ?? DEFAULT_REPO;
  const names = options?.parserNames ?? PARSER_NAMES;
  const wasmFilename = options?.wasmFilename ?? WASM_FILENAME;

  const checksums = parseChecksums(checksumsContent);
  const hash = checksums.get(wasmFilename);
  if (!hash) {
    throw new Error(
      `WASM artifact ${wasmFilename} not found in checksums.txt — ` +
        `did the parsers build and goreleaser checksum.extra_files run?`,
    );
  }
  if (!/^[0-9a-f]{64}$/.test(hash)) {
    throw new Error(`WASM checksum for ${wasmFilename} is not a 64-char sha256: ${hash}`);
  }

  const tag = version.startsWith("v") ? version : `v${version}`;
  const url = `https://github.com/${repo}/releases/download/${tag}/${wasmFilename}`;

  const manifest: ParserManifest = {};
  for (const name of names) {
    manifest[name] = { hash, url, version };
  }
  return manifest;
}

// Parse a `checksums.txt` body into basename -> lowercase sha256. Tolerates the
// GNU `*` binary marker, blank lines, and uppercase digests; keys by basename so
// path-prefixed entries still resolve.
export function parseChecksums(content: string): Map<string, string> {
  const map = new Map<string, string>();
  for (const line of content.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) {
      continue;
    }
    const parts = trimmed.split(/\s+/);
    const hash = parts[0];
    const rest = parts.slice(1);
    if (!hash || rest.length === 0) {
      continue;
    }
    const file = rest.join(" ").replace(/^\*/, "");
    map.set(basename(file), hash.toLowerCase());
  }
  return map;
}

// Copy the built `.wasm` into the npm wasm package directory and stamp its
// version. Throws if the artifact has not been built (run `task build:parsers`).
export function prepareWasmPackage(options: {
  packageDir: string;
  version: string;
  wasmSource: string;
}): void {
  if (!existsSync(options.wasmSource)) {
    throw new Error(
      `WASM artifact not found: ${options.wasmSource}\n` +
        `Did the parsers build run? (task build:parsers)`,
    );
  }

  mkdirSync(options.packageDir, { recursive: true });
  cpSync(options.wasmSource, join(options.packageDir, WASM_FILENAME));

  const pkgPath = join(options.packageDir, "package.json");
  const pkg = JSON.parse(readFileSync(pkgPath, "utf8"));
  pkg.version = options.version;
  writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + "\n");
}

// Generate the parser manifest from `checksums.txt` and write it to disk.
export function writeParserManifest(options: {
  checksumsContent: string;
  destPath: string;
  repo?: string;
  version: string;
}): ParserManifest {
  const manifest = buildParserManifest(options.checksumsContent, options.version, {
    repo: options.repo,
  });
  writeFileSync(options.destPath, JSON.stringify(manifest, null, 2) + "\n");
  return manifest;
}
