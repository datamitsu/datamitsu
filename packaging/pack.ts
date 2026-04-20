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
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";

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
const PYTHON_PLATFORM_DIR = join(PYTHON_DIR, "platform-packages");
const DIST_DIR = join(ROOT_DIR, "dist");

interface PlatformConfig {
  archName: string;
  goarch: string;
  goos: string;
  npmArch: string;
  npmPlatform: string;
  osName: string;
  pythonArch: string;
  // Python-specific fields
  pythonPlatform: string;
  wheelTag: string;
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
    wheelTag: "macosx_11_0_x86_64",
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
    wheelTag: "macosx_11_0_arm64",
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
    wheelTag: "manylinux2014_x86_64",
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
    wheelTag: "manylinux2014_aarch64",
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
    wheelTag: "win_amd64",
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
    wheelTag: "win_arm64",
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
    wheelTag: "",
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
    wheelTag: "",
  },
];

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
  console.log("\n📦 Cleaning Python packages...");

  if (existsSync(PYTHON_PLATFORM_DIR)) {
    rmSync(PYTHON_PLATFORM_DIR, { force: true, recursive: true });
    console.log("✓ Cleaned platform-packages/");
  }
}

function exec(command: string, cwd?: string): void {
  console.log(`$ ${command}`);
  execSync(command, { cwd, stdio: "inherit" });
}

function execSafe(command: string, cwd?: string): Promise<{ error?: any; success: boolean }> {
  console.log(`$ ${command}`);

  const child = spawn(command, {
    cwd,
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

function getOsClassifier(goos: string): string {
  switch (goos) {
    case "darwin": {
      return "MacOS";
    }
    case "linux": {
      return "POSIX :: Linux";
    }
    case "windows": {
      return "Microsoft :: Windows";
    }
    default: {
      return "OS Independent";
    }
  }
}

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

    // Copy README
    const readmePath = join(PACKAGING_DIR, "PACKAGE_README.md");
    if (existsSync(readmePath)) {
      cpSync(readmePath, join(packageDir, "README.md"));
    }
  }
}

// ============================================================================
// Python Packaging
// ============================================================================

function preparePythonPackages() {
  console.log("\n📦 Preparing Python packages...");

  // Read template
  const templatePath = join(PYTHON_DIR, "templates", "pyproject.toml.template");
  const template = readFileSync(templatePath, "utf8");

  // Filter platforms: only process those with pythonPlatform defined (skip FreeBSD)
  const pythonPlatforms = PLATFORMS.filter((p) => p.pythonPlatform !== "");

  for (const platform of pythonPlatforms) {
    const packageName = `datamitsu-${platform.pythonPlatform}-${platform.pythonArch}`;
    const packageDir = join(PYTHON_PLATFORM_DIR, packageName);
    const moduleName = `datamitsu_${platform.pythonPlatform}_${platform.pythonArch}`;
    const moduleDir = join(packageDir, moduleName);
    const binDir = join(packageDir, "bin");

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

    // Create directories
    mkdirSync(moduleDir, { recursive: true });
    mkdirSync(binDir, { recursive: true });

    // Create pyproject.toml from template
    const pyprojectToml = replaceVariables(template, {
      ARCH: platform.pythonArch,
      ARCH_NAME: platform.archName,
      OS_CLASSIFIER: getOsClassifier(platform.goos),
      OS_NAME: platform.osName,
      PLATFORM: platform.pythonPlatform,
      VERSION: normalizePythonVersion(VERSION),
    });
    writeFileSync(join(packageDir, "pyproject.toml"), pyprojectToml);

    // Create __init__.py
    const initPy = `"""datamitsu binary for ${platform.pythonPlatform}-${platform.pythonArch}."""

__version__ = "${normalizePythonVersion(VERSION)}"

import os
from pathlib import Path

__file__ = Path(__file__)
`;
    writeFileSync(join(moduleDir, "__init__.py"), initPy);

    // Copy binary to bin/
    cpSync(sourceBinary, join(binDir, binaryName));
    console.log(`✓ Created ${packageName}`);

    // Copy README
    const readmePath = join(PACKAGING_DIR, "PACKAGE_README.md");
    if (existsSync(readmePath)) {
      cpSync(readmePath, join(packageDir, "README.md"));
    }
  }

  // Update main package version
  updateMainPythonPackage();
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
  const provenanceFlag = dryRun ? "" : "--provenance";
  const baseCommand = dryRun
    ? "npm publish --dry-run"
    : `npm publish --access public ${provenanceFlag}`;
  const npmCommand = `${baseCommand} --tag ${tag}`;

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
  const isPrerelease =
    normalizedVersion.includes("a") ||
    normalizedVersion.includes("b") ||
    normalizedVersion.includes("rc");

  console.log(`Version: ${normalizedVersion} (prerelease: ${isPrerelease})`);

  let hasErrors = false;

  // Build all wheels first
  console.log("\n📦 Building wheels...");

  // Filter platforms for Python (skip FreeBSD)
  const pythonPlatforms = PLATFORMS.filter((p) => p.pythonPlatform !== "");

  // Build platform-specific packages
  for (const platform of pythonPlatforms) {
    const packageName = `datamitsu-${platform.pythonPlatform}-${platform.pythonArch}`;
    const packageDir = join(PYTHON_PLATFORM_DIR, packageName);

    console.log(`\nBuilding ${packageName}...`);
    const buildResult = await execSafe("uv build", packageDir);

    if (buildResult.success) {
      console.log(`✓ Built ${packageName}`);
    } else {
      console.error(`✗ Failed to build ${packageName}`);
      hasErrors = true;
      if (!dryRun) {
        throw new Error(`Build failed for ${packageName}`);
      }
    }
  }

  // Build main package
  console.log("\nBuilding main datamitsu package...");
  const mainBuildResult = await execSafe("uv build", PYTHON_DIR);

  if (mainBuildResult.success) {
    console.log("✓ Built main package");
  } else {
    console.error("✗ Failed to build main package");
    hasErrors = true;
    if (!dryRun) {
      throw new Error("Build failed for main package");
    }
  }

  if (hasErrors && dryRun) {
    console.log("\n⚠️  Some packages had build errors during dry-run");
    return;
  }

  // Publish wheels
  console.log("\n📤 Publishing wheels...");

  // Publish platform packages
  for (const platform of pythonPlatforms) {
    const packageName = `datamitsu-${platform.pythonPlatform}-${platform.pythonArch}`;
    const packageDir = join(PYTHON_PLATFORM_DIR, packageName);

    if (dryRun) {
      console.log(`\n[DRY RUN] Would publish ${packageName}`);
    } else {
      console.log(`\nPublishing ${packageName}...`);
      const result = await execSafe("uv publish", packageDir);

      if (result.success) {
        console.log(`✓ Published ${packageName}`);
      } else {
        console.error(`✗ Failed to publish ${packageName}`);
        hasErrors = true;
      }
    }
  }

  // Publish main package
  if (dryRun) {
    console.log("\n[DRY RUN] Would publish main datamitsu package");
  } else {
    console.log("\nPublishing main datamitsu package...");
    const mainResult = await execSafe("uv publish", PYTHON_DIR);

    if (mainResult.success) {
      console.log("✓ Published main package");
    } else {
      console.error("✗ Failed to publish main package");
      hasErrors = true;
    }
  }

  if (hasErrors && !dryRun) {
    throw new Error("Some packages failed to publish");
  }

  if (dryRun) {
    console.log("\n✅ Dry-run completed!");
  } else {
    console.log("\n✅ All Python packages published successfully!");
  }
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
  console.log("\n📝 Updating main Python package version...");

  const pyprojectPath = join(PYTHON_DIR, "pyproject.toml");
  let content = readFileSync(pyprojectPath, "utf8");

  const normalizedVersion = normalizePythonVersion(VERSION);

  // Replace version in pyproject.toml
  content = content.replace(/version = "[^"]*"/, `version = "${normalizedVersion}"`);

  // Replace versions in dependencies
  content = content.replaceAll(/datamitsu-[^=]+==[^;]*/g, (match) => {
    const pkgName = match.split("==")[0];
    return `${pkgName}==${normalizedVersion}`;
  });

  writeFileSync(pyprojectPath, content);

  // Update __init__.py version
  const initPath = join(PYTHON_DIR, "datamitsu", "__init__.py");
  let initContent = readFileSync(initPath, "utf8");
  initContent = initContent.replace(
    /__version__ = "[^"]*"/,
    `__version__ = "${normalizedVersion}"`,
  );
  writeFileSync(initPath, initContent);

  console.log(`✓ Updated main Python package to version ${normalizedVersion}`);
}

// CLI
const command = process.argv[2];

async function main() {
  switch (command) {
    case "all": {
      // Full packaging workflow for both npm and Python
      console.log("\n🎯 Running full packaging workflow...");

      // npm
      clean();
      preparePlatformPackages();
      updateMainPackage();

      // Python
      cleanPython();
      preparePythonPackages();

      console.log("\n✅ All packages prepared!");
      console.log("\nTo publish:");
      console.log("  npm:    tsx pack.ts publish [--dry-run]");
      console.log("  Python: tsx pack.ts publish-python [--dry-run]");
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

    default: {
      console.log(`
Usage: tsx pack.ts <command>

Commands:
  clean              Clean npm packages
  prepare            Prepare npm packages from GoReleaser binaries
  publish            Publish to npm (use --dry-run for testing)
  prepare-python     Prepare Python packages from GoReleaser binaries
  publish-python     Publish to PyPI (use --dry-run for testing)
  all                Prepare both npm and Python packages

Examples:
  tsx pack.ts prepare
  tsx pack.ts publish --dry-run
  tsx pack.ts prepare-python
  tsx pack.ts publish-python --dry-run
  VERSION=1.0.0 tsx pack.ts all

Note: Binaries are built by GoReleaser. This script only handles packaging.
      The lib/ directory (programmable API) is built by Taskfile (pack:build-api).
`);
      process.exit(1);
    }
  }
}

await main();
