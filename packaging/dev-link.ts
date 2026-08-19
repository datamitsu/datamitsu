#!/usr/bin/env tsx

// Dev-link package builder.
//
// Produces a throwaway npm package pair under dist/dev-link/ that stands in for
// the published ones, so a locally patched core can be exercised through the
// real pnpm chain: core -> wrapper config package -> consuming project.
//
//   @datamitsu/datamitsu-<platform>-<arch>  the freshly built binary
//   @datamitsu/datamitsu                    the launcher, depending on it by file:
//
// The binary is built with -X ldflags.LocalArtifacts=1, which is what unlocks
// `file://` parser sources; released binaries never carry it. The locally built
// WASM parser module is copied in alongside, and its SHA-256 printed, so the
// wrapper config can point at it by file:// + hash — the hash stays mandatory
// and verified, exactly as for a release asset.
//
// Everything lands under dist/, which is gitignored.

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";

const ROOT_DIR = join(import.meta.dirname, "..");
const NPM_DIR = join(import.meta.dirname, "npm");
const OUT_DIR = join(ROOT_DIR, "dist", "dev-link");

const LDFLAGS_PKG = "github.com/datamitsu/datamitsu/internal/ldflags";
const WASM_PATH = join(
  ROOT_DIR,
  "parsers",
  "target",
  "wasm32-unknown-unknown",
  "release",
  "datamitsu_parsers.wasm",
);

// npm's platform/arch vocabulary is Node's, not Go's.
const GOOS_TO_NPM: Record<string, string> = {
  darwin: "darwin",
  freebsd: "freebsd",
  linux: "linux",
  windows: "win32",
};
const GOARCH_TO_NPM: Record<string, string> = { amd64: "x64", arm64: "arm64" };

function buildBinary(outPath: string) {
  // The config layer is compiled from TypeScript and embedded into the binary,
  // so it has to be regenerated first — `go build` alone happily embeds a stale
  // config.js/config.d.ts, and the wrapper then sees types from whenever those
  // artifacts were last built. This is the same dependency `task build` declares.
  console.log("🔨 Building the embedded config layer...");
  execFileSync("task", ["build:lib"], { cwd: ROOT_DIR, stdio: "inherit" });

  console.log("🔨 Building the core with local artifacts enabled...");
  // The flag is what makes this build a dev-link build: it is the only way a
  // binary will read a file:// artifact at all.
  go("build", "-ldflags", `-X ${LDFLAGS_PKG}.LocalArtifacts=1`, "-o", outPath, ".");
}

// The programmable API is what the `.` export resolves to. A wrapper config
// imports it for its types, so a dev-link package without lib/ breaks the
// wrapper's own build — CI builds it during packaging, and so must this.
function buildProgrammableAPI(destinationLibrary: string) {
  console.log("🔨 Building the programmable API...");
  execFileSync("pnpm", ["--filter", "@datamitsu/programmable-api-js", "build"], {
    cwd: ROOT_DIR,
    stdio: "inherit",
  });
  rmSync(destinationLibrary, { force: true, recursive: true });
  cpSync(join(ROOT_DIR, "programmable-api", "js", "dist"), destinationLibrary, { recursive: true });
}

function currentPlatform() {
  const goos = go("env", "GOOS");
  const goarch = go("env", "GOARCH");
  const npmPlatform = GOOS_TO_NPM[goos];
  const npmArch = GOARCH_TO_NPM[goarch];
  if (!npmPlatform || !npmArch) {
    throw new Error(`unsupported host for dev-link: ${goos}/${goarch}`);
  }
  return { goarch, goos, npmArch, npmPlatform };
}

function go(...arguments_: string[]): string {
  return execFileSync("go", arguments_, { cwd: ROOT_DIR, encoding: "utf8" }).trim();
}

// The launcher resolves two things through Node: the platform package (from
// get-exe.js) and the programmable API's runtime dependencies (lib/index.js
// imports tinyexec). `pnpm link` installs nothing — it just points at this
// directory — so both have to be present in the package's own node_modules or
// the import fails with ERR_MODULE_NOT_FOUND.
function linkDependencies(
  launcherDir: string,
  platformPackageName: string,
  platformDirName: string,
) {
  const scopeDir = join(launcherDir, "node_modules", "@datamitsu");
  mkdirSync(scopeDir, { recursive: true });
  const linkPath = join(scopeDir, platformPackageName.split("/", 2)[1]);
  rmSync(linkPath, { force: true, recursive: true });
  // Relative target so the whole dist/dev-link tree stays movable.
  symlinkSync(join("..", "..", "..", platformDirName), linkPath, "dir");

  linkRuntimeDependencies(join(launcherDir, "node_modules"));
}

// Copy each runtime dependency out of this repo's own node_modules. Copies, not
// symlinks: pnpm stores them as links into a global content-addressed store, and
// a link-to-a-link resolves relative to the realpath, landing outside the package.
function linkRuntimeDependencies(modulesDir: string) {
  const published = JSON.parse(
    readFileSync(join(NPM_DIR, "datamitsu", "package.json"), "utf8"),
  ) as { dependencies?: Record<string, string> };

  const publishedDependencies = Object.keys(published.dependencies ?? {});
  for (const name of publishedDependencies) {
    const source = resolvePackageDir(name);
    if (!source) {
      // Not fatal: only the programmable API needs these, and the CLI path
      // (bin/index.js -> the binary) works without them.
      console.log(`⚠  ${name} not resolvable — the programmable API import will fail`);
      continue;
    }
    const destination = join(modulesDir, name);
    rmSync(destination, { force: true, recursive: true });
    cpSync(source, destination, { dereference: true, recursive: true });
  }
}

// Locate an installed package's directory. pnpm installs per workspace project,
// so a dependency of the programmable API is under its node_modules, not the
// root's — resolve from that package and fall back to a plain path probe for
// packages that do not export their package.json.
function resolvePackageDir(name: string): string | undefined {
  const apiDir = join(ROOT_DIR, "programmable-api", "js");
  try {
    const require = createRequire(join(apiDir, "package.json"));
    return dirname(require.resolve(`${name}/package.json`));
  } catch {
    // Fall through to the path probe.
  }
  for (const base of [apiDir, ROOT_DIR]) {
    const candidate = join(base, "node_modules", name);
    if (existsSync(candidate)) {
      return candidate;
    }
  }
  return undefined;
}

function writeLauncherPackage(dir: string, platformPackageName: string, platformDirName: string) {
  mkdirSync(dir, { recursive: true });
  linkDependencies(dir, platformPackageName, platformDirName);
  cpSync(join(NPM_DIR, "datamitsu", "bin"), join(dir, "bin"), { recursive: true });
  cpSync(join(NPM_DIR, "datamitsu", "get-exe.js"), join(dir, "get-exe.js"));
  buildProgrammableAPI(join(dir, "lib"));

  const published = JSON.parse(
    readFileSync(join(NPM_DIR, "datamitsu", "package.json"), "utf8"),
  ) as Record<string, unknown>;

  writeFileSync(
    join(dir, "package.json"),
    `${JSON.stringify(
      {
        ...published,
        devDependencies: undefined,
        files: ["bin", "lib", "get-exe.js", "parsers"],
        // Only the host platform exists locally, wired by path rather than by
        // version so no publish step is involved.
        optionalDependencies: { [platformPackageName]: `file:../${platformDirName}` },
        scripts: undefined,
        version: DEV_VERSION,
      },
      null,
      2,
    )}\n`,
  );
}

function writePlatformPackage(dir: string, platform: ReturnType<typeof currentPlatform>) {
  const template = readFileSync(join(NPM_DIR, "templates", "platform-package.json"), "utf8");
  const package_ = template
    .split("{{PLATFORM}}")
    .join(platform.npmPlatform)
    .split("{{ARCH}}")
    .join(platform.npmArch)
    .split("{{ARCH_NAME}}")
    .join(platform.npmArch)
    .split("{{OS_NAME}}")
    .join(platform.npmPlatform)
    .split("{{VERSION}}")
    .join(DEV_VERSION);

  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "package.json"), package_);
  return JSON.parse(package_).name as string;
}

const DEV_VERSION = "0.0.0-dev-link";

function main() {
  const platform = currentPlatform();
  const binaryName = platform.goos === "windows" ? "datamitsu.exe" : "datamitsu";
  const platformDirName = `datamitsu-${platform.npmPlatform}-${platform.npmArch}`;
  const platformDir = join(OUT_DIR, platformDirName);
  const launcherDir = join(OUT_DIR, "datamitsu");

  rmSync(OUT_DIR, { force: true, recursive: true });
  mkdirSync(OUT_DIR, { recursive: true });

  const platformPackageName = writePlatformPackage(platformDir, platform);
  buildBinary(join(platformDir, binaryName));
  writeLauncherPackage(launcherDir, platformPackageName, platformDirName);

  console.log(`\n✓ ${platformPackageName} (binary)`);
  console.log(`✓ @datamitsu/datamitsu (launcher)`);
  console.log(`\n📦 ${OUT_DIR}`);
  console.log(`\nLink it into the wrapper config repo:\n`);
  console.log(`  pnpm link ${launcherDir}\n`);

  if (!existsSync(WASM_PATH)) {
    console.log("ℹ  No WASM parser build found — run `task build:parsers` if you are");
    console.log("   iterating on parsers, then re-run this task.\n");
    return;
  }

  // Ship the module inside the launcher package so linking it also delivers the
  // parser, and print the pin the wrapper config needs.
  const wasm = readFileSync(WASM_PATH);
  const parsersDir = join(launcherDir, "parsers");
  mkdirSync(parsersDir, { recursive: true });
  const wasmDestination = join(parsersDir, "datamitsu_parsers.wasm");
  writeFileSync(wasmDestination, wasm);
  const hash = createHash("sha256").update(wasm).digest("hex");

  console.log("Point the wrapper config's `parsers` entry at the local module:\n");
  console.log("  const parsers = { core: {");
  console.log(`    hash: "${hash}",`);
  console.log(`    url: "file://${wasmDestination}"`);
  console.log("  } };\n");
  console.log("The hash is still mandatory and verified — only the transport is local,");
  console.log("and only this dev-link build can read it.\n");
}

main();
