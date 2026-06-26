#!/usr/bin/env tsx

// npm Publishing Flow
//
// This script handles platform-specific binary packaging. The full publish flow:
//
// 1. GoReleaser builds Go binaries -> dist/datamitsu_{version}_{os}_{arch}_*/
// 2. CI workflow normalizes binaries -> dist/binaries/datamitsu-{os}_{arch}[.exe]
// 3. CI workflow builds programmable-api and copies to lib/
// 4. `pack:prepare` (this script) creates platform-specific npm packages
// 5. `pack:publish` runs `npm publish` for each package
//
// The `lib/` directory (programmable API) is built by CI workflow, not by this script.

import { execSync, spawn } from "node:child_process";
import {
  cpSync,
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { join } from "node:path";

// The WASM parser module is NOT packaged here. It ships only as a versioned,
// signed asset on the GitHub Release (datamitsu_parsers_<version>.wasm, hash in
// the signed checksums.txt); the core downloads it by url+hash. It is never
// bundled into the npm/Python/Ruby wrappers.

// Track active child processes for cleanup on Ctrl+C
const activeProcesses = new Set<ReturnType<typeof spawn>>();

// Handle Ctrl+C gracefully
process.on("SIGINT", () => {
  console.log("\n\n🛑 Received SIGINT, killing all active processes...");
  for (const proc of activeProcesses) {
    try {
      proc.kill("SIGTERM");
    } catch {
      // Ignore errors when killing
    }
  }
  process.exit(130); // Exit with standard SIGINT code
});

process.on("SIGTERM", () => {
  console.log("\n\n🛑 Received SIGTERM, killing all active processes...");
  for (const proc of activeProcesses) {
    try {
      proc.kill("SIGTERM");
    } catch {
      // Ignore errors when killing
    }
  }
  process.exit(143); // Exit with standard SIGTERM code
});

const VERSION = process.env.VERSION || "0.0.0";
const ROOT_DIR = join(import.meta.dirname, "..");
const PACKAGING_DIR = import.meta.dirname;
const NPM_DIR = join(PACKAGING_DIR, "npm");
const PYTHON_DIR = join(PACKAGING_DIR, "python");
const PYTHON_BIN_DIR = join(PYTHON_DIR, "datamitsu", "bin");
const RUBY_DIR = join(PACKAGING_DIR, "ruby");
const DIST_DIR = join(ROOT_DIR, "dist");

interface PlatformConfig {
  archName: string;
  goarch: string;
  goos: string;
  npmArch: string;
  npmPlatform: string;
  osName: string;
  // Python-specific fields (empty string = platform not supported, e.g. FreeBSD)
  pythonArch: string;
  pythonPlatform: string;
  // Ruby-specific fields
  rubyArch: string;
  rubyOs: string;
}

const PLATFORMS: PlatformConfig[] = [
  {
    archName: "x64",
    goarch: "amd64",
    goos: "darwin",
    npmArch: "x64",
    npmPlatform: "darwin",
    osName: "macOS",
    pythonArch: "x86_64",
    pythonPlatform: "darwin",
    rubyArch: "x64",
    rubyOs: "darwin",
  },
  {
    archName: "ARM64",
    goarch: "arm64",
    goos: "darwin",
    npmArch: "arm64",
    npmPlatform: "darwin",
    osName: "macOS",
    pythonArch: "arm64",
    pythonPlatform: "darwin",
    rubyArch: "arm64",
    rubyOs: "darwin",
  },
  {
    archName: "x64",
    goarch: "amd64",
    goos: "linux",
    npmArch: "x64",
    npmPlatform: "linux",
    osName: "Linux",
    pythonArch: "x86_64",
    pythonPlatform: "linux",
    rubyArch: "x64",
    rubyOs: "linux",
  },
  {
    archName: "ARM64",
    goarch: "arm64",
    goos: "linux",
    npmArch: "arm64",
    npmPlatform: "linux",
    osName: "Linux",
    pythonArch: "arm64",
    pythonPlatform: "linux",
    rubyArch: "arm64",
    rubyOs: "linux",
  },
  {
    archName: "x64",
    goarch: "amd64",
    goos: "windows",
    npmArch: "x64",
    npmPlatform: "win32",
    osName: "Windows",
    pythonArch: "x86_64",
    pythonPlatform: "windows",
    rubyArch: "x64",
    rubyOs: "windows",
  },
  {
    archName: "ARM64",
    goarch: "arm64",
    goos: "windows",
    npmArch: "arm64",
    npmPlatform: "win32",
    osName: "Windows",
    pythonArch: "arm64",
    pythonPlatform: "windows",
    rubyArch: "arm64",
    rubyOs: "windows",
  },
  {
    archName: "x64",
    goarch: "amd64",
    goos: "freebsd",
    npmArch: "x64",
    npmPlatform: "freebsd",
    osName: "FreeBSD",
    pythonArch: "",
    pythonPlatform: "",
    rubyArch: "x64",
    rubyOs: "freebsd",
  },
  {
    archName: "ARM64",
    goarch: "arm64",
    goos: "freebsd",
    npmArch: "arm64",
    npmPlatform: "freebsd",
    osName: "FreeBSD",
    pythonArch: "",
    pythonPlatform: "",
    rubyArch: "arm64",
    rubyOs: "freebsd",
  },
];

async function buildPythonWheels() {
  console.log("\n📦 Building platform wheels...");

  // Clean dist/ to avoid publishing stale wheels from previous runs
  const pythonDistDir = join(PYTHON_DIR, "dist");
  if (existsSync(pythonDistDir)) {
    rmSync(pythonDistDir, { force: true, recursive: true });
  }

  const pythonPlatforms = PLATFORMS.filter((p) => p.pythonPlatform !== "");

  for (const platform of pythonPlatforms) {
    const target = `${platform.pythonPlatform}-${platform.pythonArch}`;

    console.log(`\nBuilding wheel for ${target}...`);
    const buildResult = await execSafe("uv build --wheel", PYTHON_DIR, {
      DATAMITSU_TARGET_ARCH: platform.pythonArch,
      DATAMITSU_TARGET_PLATFORM: platform.pythonPlatform,
    });

    if (buildResult.success) {
      console.log(`✓ Built wheel for ${target}`);
    } else {
      throw new Error(`Build failed for ${target}`);
    }
  }

  console.log("\n✅ All platform wheels built!");
}

function clean() {
  console.log("\n📦 Cleaning npm packages...");

  // Clean platform-specific packages only (not dist/ - that's managed by GoReleaser)
  for (const platform of PLATFORMS) {
    const platformDir = join(NPM_DIR, `datamitsu-${platform.npmPlatform}-${platform.npmArch}`);
    if (existsSync(platformDir)) {
      rmSync(platformDir, { force: true, recursive: true });
    }
  }
}

function cleanPython() {
  console.log("\n📦 Cleaning Python bin/ directory...");

  if (existsSync(PYTHON_BIN_DIR)) {
    // Remove all subdirectories in bin/ (keep .keep)
    for (const entry of readdirSync(PYTHON_BIN_DIR)) {
      if (entry === ".keep") {
        continue;
      }
      const entryPath = join(PYTHON_BIN_DIR, entry);
      rmSync(entryPath, { force: true, recursive: true });
    }
    console.log("✓ Cleaned datamitsu/bin/");
  }

  // Clean build artifacts
  for (const dir of ["dist", "build"]) {
    const dirPath = join(PYTHON_DIR, dir);
    if (existsSync(dirPath)) {
      rmSync(dirPath, { force: true, recursive: true });
    }
  }
}

function cleanRuby() {
  console.log("\n📦 Cleaning Ruby gem...");

  for (const platform of PLATFORMS) {
    const libexecSubdir = join(
      RUBY_DIR,
      "libexec",
      `datamitsu-${platform.rubyOs}-${platform.rubyArch}`,
    );
    if (existsSync(libexecSubdir)) {
      rmSync(libexecSubdir, { force: true, recursive: true });
      console.log(`✓ Cleaned libexec/datamitsu-${platform.rubyOs}-${platform.rubyArch}/`);
    }
  }

  const pkgDir = join(RUBY_DIR, "pkg");
  if (existsSync(pkgDir)) {
    rmSync(pkgDir, { force: true, recursive: true });
    console.log("✓ Cleaned pkg/");
  }

  // Clean any .gem files in the ruby dir
  if (existsSync(RUBY_DIR)) {
    for (const entry of readdirSync(RUBY_DIR)) {
      if (entry.endsWith(".gem")) {
        rmSync(join(RUBY_DIR, entry), { force: true });
        console.log(`✓ Cleaned ${entry}`);
      }
    }
  }
}

function exec(command: string, cwd?: string): void {
  console.log(`$ ${command}`);
  execSync(command, { cwd, stdio: "inherit" });
}

function execSafe(
  command: string,
  cwd?: string,
  env?: Record<string, string>,
): Promise<{ error?: any; success: boolean }> {
  console.log(`$ ${command}`);

  const child = spawn(command, {
    cwd,
    env: env ? { ...process.env, ...env } : undefined,
    shell: true,
    stdio: "inherit",
  });

  activeProcesses.add(child);

  return new Promise((resolve) => {
    child.on("close", (code) => {
      activeProcesses.delete(child);
      if (code === 0) {
        resolve({ success: true });
      } else {
        resolve({ error: new Error(`Command exited with code ${code}`), success: false });
      }
    });

    child.on("error", (error) => {
      activeProcesses.delete(child);
      resolve({ error, success: false });
    });
  });
}

// ============================================================================
// Python Packaging
// ============================================================================

function normalizePythonVersion(version: string): string {
  // Strip 'v' prefix
  let normalized = version.replace(/^v/, "");

  // Convert -rc.N to rcN (PEP 440)
  normalized = normalized.replace(/-rc\.(\d+)/, "rc$1");

  // Convert -alpha.N to aN
  normalized = normalized.replace(/-alpha\.(\d+)/, "a$1");

  // Convert -beta.N to bN
  normalized = normalized.replace(/-beta\.(\d+)/, "b$1");

  return normalized;
}

function normalizeRubyVersion(version: string): string {
  // Strip 'v' prefix
  let normalized = version.replace(/^v/, "");

  // Convert -rc.N to .rc.N (RubyGems pre-release convention)
  normalized = normalized.replace(/-rc\.(\d+)/, ".rc.$1");

  // Convert -alpha.N to .alpha.N
  normalized = normalized.replace(/-alpha\.(\d+)/, ".alpha.$1");

  // Convert -beta.N to .beta.N
  normalized = normalized.replace(/-beta\.(\d+)/, ".beta.$1");

  // Convert any remaining - to . for pre-release segments
  normalized = normalized.replace(/-/, ".");

  return normalized;
}

function preparePlatformPackages() {
  console.log("\n📦 Preparing platform-specific npm packages...");

  const templatePath = join(NPM_DIR, "templates", "platform-package.json");
  const template = readFileSync(templatePath, "utf8");

  for (const platform of PLATFORMS) {
    const packageName = `datamitsu-${platform.npmPlatform}-${platform.npmArch}`;
    const packageDir = join(NPM_DIR, packageName);
    const binaryName = platform.goos === "windows" ? "datamitsu.exe" : "datamitsu";

    // CI workflow normalizes binaries to:
    // dist/binaries/datamitsu-{goos}_{goarch}[.exe]
    const releaseBinaryName =
      platform.goos === "windows"
        ? `datamitsu-${platform.goos}_${platform.goarch}.exe`
        : `datamitsu-${platform.goos}_${platform.goarch}`;

    const sourceBinary = join(DIST_DIR, "binaries", releaseBinaryName);

    // Verify binary exists before creating package
    if (!existsSync(sourceBinary)) {
      throw new Error(
        `Binary not found: ${sourceBinary}\n` +
          `Did GoReleaser build complete successfully for ${platform.goos}/${platform.goarch}?`,
      );
    }

    // Create package directory
    mkdirSync(packageDir, { recursive: true });

    // Create package.json
    const packageJson = replaceVariables(template, {
      ARCH: platform.npmArch,
      ARCH_NAME: platform.archName,
      OS_NAME: platform.osName,
      PLATFORM: platform.npmPlatform,
      VERSION: VERSION,
    });
    writeFileSync(join(packageDir, "package.json"), packageJson);

    // Copy binary
    cpSync(sourceBinary, join(packageDir, binaryName));
    console.log(`✓ Created ${packageName}`);
  }
}

function preparePythonPackages() {
  console.log("\n📦 Preparing Python package (single package, multi-platform wheels)...");

  // Filter platforms: only process those with pythonPlatform defined (skip FreeBSD)
  const pythonPlatforms = PLATFORMS.filter((p) => p.pythonPlatform !== "");

  // Copy all platform binaries into datamitsu/bin/datamitsu-{os}-{arch}/
  for (const platform of pythonPlatforms) {
    const binaryName = platform.goos === "windows" ? "datamitsu.exe" : "datamitsu";
    const releaseBinaryName =
      platform.goos === "windows"
        ? `datamitsu-${platform.goos}_${platform.goarch}.exe`
        : `datamitsu-${platform.goos}_${platform.goarch}`;

    const sourceBinary = join(DIST_DIR, "binaries", releaseBinaryName);

    // Verify binary exists
    if (!existsSync(sourceBinary)) {
      throw new Error(
        `Binary not found: ${sourceBinary}\n` +
          `Did GoReleaser build complete successfully for ${platform.goos}/${platform.goarch}?`,
      );
    }

    const targetDir = join(
      PYTHON_BIN_DIR,
      `datamitsu-${platform.pythonPlatform}-${platform.pythonArch}`,
    );
    mkdirSync(targetDir, { recursive: true });
    cpSync(sourceBinary, join(targetDir, binaryName));
    console.log(`✓ Copied binary for ${platform.pythonPlatform}-${platform.pythonArch}`);
  }

  // Update version in pyproject.toml
  updateMainPythonPackage();
}

function prepareRubyPackage() {
  console.log("\n📦 Preparing Ruby gem...");

  for (const platform of PLATFORMS) {
    const binaryName = platform.goos === "windows" ? "datamitsu.exe" : "datamitsu";
    const releaseBinaryName =
      platform.goos === "windows"
        ? `datamitsu-${platform.goos}_${platform.goarch}.exe`
        : `datamitsu-${platform.goos}_${platform.goarch}`;
    const sourceBinary = join(DIST_DIR, "binaries", releaseBinaryName);

    if (!existsSync(sourceBinary)) {
      throw new Error(
        `Binary not found: ${sourceBinary}\n` +
          `Did GoReleaser build complete successfully for ${platform.goos}/${platform.goarch}?`,
      );
    }

    const targetDir = join(
      RUBY_DIR,
      "libexec",
      `datamitsu-${platform.rubyOs}-${platform.rubyArch}`,
    );
    mkdirSync(targetDir, { recursive: true });
    cpSync(sourceBinary, join(targetDir, binaryName));
    console.log(`✓ Copied ${platform.rubyOs}-${platform.rubyArch} binary`);
  }

  // Update gemspec version
  updateRubyGemspec();

  console.log("✓ Ruby gem prepared");
}

async function publishNpm(dryRun = true) {
  console.log(`\n🚀 Publishing to npm (dry-run: ${dryRun})...`);

  // Read version from main package.json if VERSION env var is not set
  let publishVersion = VERSION;
  if (publishVersion === "0.0.0") {
    const mainPackagePath = join(NPM_DIR, "datamitsu", "package.json");
    if (existsSync(mainPackagePath)) {
      const packageJson = JSON.parse(readFileSync(mainPackagePath, "utf8"));
      publishVersion = packageJson.version || "0.0.0";
      console.log(`Using version from package.json: ${publishVersion}`);
    }
  }

  // Determine npm tag based on version
  const isPrerelease =
    publishVersion.includes("-alpha") ||
    publishVersion.includes("-beta") ||
    publishVersion.includes("-rc");
  const tag = isPrerelease ? "next" : "latest";

  console.log(`Publishing with tag: ${tag} (version: ${publishVersion})`);

  // Use --provenance for transparent publishing with OIDC
  // Only enable provenance when OIDC token is available (GitHub Actions with id-token: write)
  const hasOIDC = Boolean(process.env.ACTIONS_ID_TOKEN_REQUEST_URL);
  const provenanceFlag = dryRun || !hasOIDC ? "" : "--provenance";
  const baseCommand = dryRun
    ? "npm publish --dry-run"
    : `npm publish --access public ${provenanceFlag}`;
  const npmCommand = `${baseCommand} --tag ${tag}`;
  console.log(
    `Provenance: ${hasOIDC && !dryRun ? "enabled" : "disabled"} (OIDC=${hasOIDC}, dry-run=${dryRun})`,
  );

  let hasErrors = false;

  // Publish platform-specific packages first
  for (const platform of PLATFORMS) {
    const packageName = `datamitsu-${platform.npmPlatform}-${platform.npmArch}`;
    const packageDir = join(NPM_DIR, packageName);

    console.log(`\nPublishing ${packageName}...`);
    const result = await execSafe(npmCommand, packageDir);

    if (result.success) {
      console.log(`✓ Published ${packageName}`);
    } else {
      console.error(`✗ Failed to publish ${packageName}`);
      hasErrors = true;
      if (!dryRun) {
        throw new Error(
          `Failed to publish ${packageName}: ${result.error?.message || "Unknown error"}`,
        );
      }
    }
  }

  // Publish main package last
  console.log("\nPublishing main datamitsu package...");
  const mainPackageDir = join(NPM_DIR, "datamitsu");
  const mainResult = await execSafe(npmCommand, mainPackageDir);

  if (mainResult.success) {
    console.log("✓ Published main package");
  } else {
    console.error("✗ Failed to publish main package");
    hasErrors = true;
    if (!dryRun) {
      throw new Error(
        `Failed to publish main package: ${mainResult.error?.message || "Unknown error"}`,
      );
    }
  }

  if (hasErrors && dryRun) {
    console.log(
      "\n⚠️  Some packages had errors during dry-run (this is normal for already published versions)",
    );
  } else {
    console.log("\n✅ All packages published successfully!");
  }
}

async function publishPyPI(dryRun = true) {
  console.log(`\n🚀 Publishing to PyPI (dry-run: ${dryRun})...`);

  const normalizedVersion = normalizePythonVersion(VERSION);
  console.log(`Version: ${normalizedVersion}`);

  // Build all wheels
  await buildPythonWheels();

  // Publish all wheels
  if (dryRun) {
    console.log("\n[DRY RUN] Would publish datamitsu wheels");
  } else {
    console.log("\n📤 Publishing wheels...");
    const result = await execSafe("uv publish", PYTHON_DIR);

    if (result.success) {
      console.log("✓ Published all wheels");
    } else {
      console.error("✗ Failed to publish");
      throw new Error("Publishing failed");
    }
  }

  console.log(dryRun ? "\n✅ Dry-run completed!" : "\n✅ All Python wheels published!");
}

async function publishRubyGems(dryRun = true) {
  console.log(`\n🚀 Publishing to RubyGems (dry-run: ${dryRun})...`);

  const rubyVersion = normalizeRubyVersion(VERSION);
  console.log(`Version: ${rubyVersion}`);

  // Build gem
  console.log("\n📦 Building gem...");
  const buildResult = await execSafe("gem build datamitsu.gemspec", RUBY_DIR);

  if (!buildResult.success) {
    throw new Error("Failed to build gem");
  }
  console.log("✓ Gem built");

  const gemFile = `datamitsu-${rubyVersion}.gem`;
  const gemPath = join(RUBY_DIR, gemFile);

  if (!existsSync(gemPath)) {
    throw new Error(`Built gem not found: ${gemPath}`);
  }

  if (dryRun) {
    console.log(`\n[DRY RUN] Would publish ${gemFile}`);
    console.log("\n✅ Dry-run completed!");
    return;
  }

  console.log(`\nPublishing ${gemFile}...`);
  const publishResult = await execSafe(`gem push ${gemFile}`, RUBY_DIR);

  if (publishResult.success) {
    console.log("✓ Published to RubyGems");
  } else {
    throw new Error("Failed to publish gem");
  }

  console.log("\n✅ RubyGems publishing completed!");
}

function replaceVariables(content: string, vars: Record<string, string>): string {
  let result = content;
  for (const [key, value] of Object.entries(vars)) {
    // eslint-disable-next-line security/detect-non-literal-regexp
    result = result.replaceAll(new RegExp(`{{${key}}}`, "g"), value);
  }
  return result;
}

function updateMainPackage() {
  console.log("\n📝 Updating main package version...");

  const mainPackagePath = join(NPM_DIR, "datamitsu", "package.json");
  const packageJson = JSON.parse(readFileSync(mainPackagePath, "utf8"));

  // Update version
  packageJson.version = VERSION;

  // Update optional dependencies versions
  for (const dep in packageJson.optionalDependencies) {
    if (Object.hasOwn(packageJson.optionalDependencies, dep)) {
      packageJson.optionalDependencies[dep] = VERSION;
    }
  }

  writeFileSync(mainPackagePath, JSON.stringify(packageJson, null, 2) + "\n");
  console.log(`✓ Updated main package to version ${VERSION}`);
}

function updateMainPythonPackage() {
  console.log("\n📝 Updating Python package version...");

  const pyprojectPath = join(PYTHON_DIR, "pyproject.toml");
  let content = readFileSync(pyprojectPath, "utf8");

  const normalizedVersion = normalizePythonVersion(VERSION);

  // Replace version in pyproject.toml
  content = content.replace(/version = "[^"]*"/, `version = "${normalizedVersion}"`);
  writeFileSync(pyprojectPath, content);

  console.log(`✓ Updated Python package to version ${normalizedVersion}`);
}

function updateRubyGemspec() {
  console.log("\n📝 Updating Ruby gemspec version...");

  const gemspecPath = join(RUBY_DIR, "datamitsu.gemspec");
  let content = readFileSync(gemspecPath, "utf8");

  const rubyVersion = normalizeRubyVersion(VERSION);
  content = content.replace(/spec\.version\s*=\s*"[^"]*"/, `spec.version       = "${rubyVersion}"`);

  writeFileSync(gemspecPath, content);
  console.log(`✓ Updated gemspec to version ${rubyVersion}`);
}

// CLI
const command = process.argv[2];

async function main() {
  switch (command) {
    case "all": {
      // Full packaging workflow for npm, Python, and Ruby
      console.log("\n🎯 Running full packaging workflow...");

      // npm
      clean();
      preparePlatformPackages();
      updateMainPackage();

      // Python
      cleanPython();
      preparePythonPackages();

      // Ruby
      cleanRuby();
      prepareRubyPackage();

      console.log("\n✅ All packages prepared!");
      console.log("\nTo publish:");
      console.log("  npm:    tsx pack.ts publish [--dry-run]");
      console.log("  Python: tsx pack.ts publish-python [--dry-run]");
      console.log("  Ruby:   tsx pack.ts publish-ruby [--dry-run]");
      break;
    }

    case "build-python": {
      await buildPythonWheels();
      break;
    }

    case "clean": {
      clean();
      break;
    }

    case "prepare": {
      clean();
      preparePlatformPackages();
      updateMainPackage();
      break;
    }

    case "prepare-python": {
      cleanPython();
      preparePythonPackages();
      break;
    }

    case "prepare-ruby": {
      cleanRuby();
      prepareRubyPackage();
      break;
    }

    case "publish": {
      const dryRun = process.argv.includes("--dry-run");
      await publishNpm(dryRun);
      break;
    }

    case "publish-python": {
      const dryRun = process.argv.includes("--dry-run");
      await publishPyPI(dryRun);
      break;
    }

    case "publish-ruby": {
      const dryRun = process.argv.includes("--dry-run");
      await publishRubyGems(dryRun);
      break;
    }

    default: {
      console.log(`
Usage: tsx pack.ts <command>

Commands:
  clean              Clean npm packages
  prepare            Prepare npm packages from GoReleaser binaries
  publish            Publish to npm (use --dry-run for testing)
  prepare-python     Prepare Python packages from GoReleaser binaries
  build-python       Build Python wheels for all platforms
  publish-python     Build and publish to PyPI (use --dry-run for testing)
  prepare-ruby       Prepare Ruby gem from GoReleaser binaries
  publish-ruby       Publish to RubyGems (use --dry-run for testing)
  all                Prepare npm, Python, and Ruby packages

Examples:
  tsx pack.ts prepare
  tsx pack.ts publish --dry-run
  tsx pack.ts prepare-python
  tsx pack.ts build-python
  tsx pack.ts publish-python --dry-run
  tsx pack.ts prepare-ruby
  tsx pack.ts publish-ruby --dry-run
  VERSION=1.0.0 tsx pack.ts all

Note: Binaries are built by GoReleaser. This script only handles packaging.
      The lib/ directory (programmable API) is built by Taskfile (pack:build-api).
`);
      process.exit(1);
    }
  }
}

await main();
