#!/usr/bin/env tsx

// npm Publishing Flow
//
// This script handles platform-specific binary packaging. The full publish flow:
//
// 1. GoReleaser builds Go binaries -> dist/datamitsu_{os}_{arch}_*/
// 2. CI workflow normalizes to -> dist/release/datamitsu-{os}_{arch}[.exe]
// 3. CI workflow builds programmable-api and copies to lib/
// 4. `pack:prepare` (this script) creates platform-specific npm packages
// 5. `pack:publish` runs `npm publish` for each package
//
// The `lib/` directory (programmable API) is built by CI workflow, not by this script.

import { execSync, spawn } from "node:child_process";
import {
  chmodSync,
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
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
const DIST_DIR = join(ROOT_DIR, "dist");

interface PlatformConfig {
  archName: string;
  goarch: string;
  goos: string;
  npmArch: string;
  npmPlatform: string;
  osName: string;
}

const PLATFORMS: PlatformConfig[] = [
  {
    archName: "x64",
    goarch: "amd64",
    goos: "darwin",
    npmArch: "x64",
    npmPlatform: "darwin",
    osName: "macOS",
  },
  {
    archName: "ARM64",
    goarch: "arm64",
    goos: "darwin",
    npmArch: "arm64",
    npmPlatform: "darwin",
    osName: "macOS",
  },
  {
    archName: "x64",
    goarch: "amd64",
    goos: "linux",
    npmArch: "x64",
    npmPlatform: "linux",
    osName: "Linux",
  },
  {
    archName: "ARM64",
    goarch: "arm64",
    goos: "linux",
    npmArch: "arm64",
    npmPlatform: "linux",
    osName: "Linux",
  },
  {
    archName: "x64",
    goarch: "amd64",
    goos: "windows",
    npmArch: "x64",
    npmPlatform: "win32",
    osName: "Windows",
  },
  {
    archName: "ARM64",
    goarch: "arm64",
    goos: "windows",
    npmArch: "arm64",
    npmPlatform: "win32",
    osName: "Windows",
  },
  {
    archName: "x64",
    goarch: "amd64",
    goos: "freebsd",
    npmArch: "x64",
    npmPlatform: "freebsd",
    osName: "FreeBSD",
  },
  {
    archName: "ARM64",
    goarch: "arm64",
    goos: "freebsd",
    npmArch: "arm64",
    npmPlatform: "freebsd",
    osName: "FreeBSD",
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

function preparePlatformPackages() {
  console.log("\n📦 Preparing platform-specific npm packages...");

  const templatePath = join(NPM_DIR, "templates", "platform-package.json");
  const template = readFileSync(templatePath, "utf8");

  for (const platform of PLATFORMS) {
    const packageName = `datamitsu-${platform.npmPlatform}-${platform.npmArch}`;
    const packageDir = join(NPM_DIR, packageName);
    const binaryName = platform.goos === "windows" ? "datamitsu.exe" : "datamitsu";

    // CI workflow normalizes GoReleaser output to:
    // dist/release/datamitsu-{goos}_{goarch}[.exe]
    const releaseBinaryName =
      platform.goos === "windows"
        ? `datamitsu-${platform.goos}_${platform.goarch}.exe`
        : `datamitsu-${platform.goos}_${platform.goarch}`;

    const sourceBinary = join(DIST_DIR, "release", releaseBinaryName);

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
    if (existsSync(sourceBinary)) {
      const targetBinary = join(packageDir, binaryName);
      cpSync(sourceBinary, targetBinary);
      // Set executable permissions (0o755 = owner: read+write+execute, group/others: read+execute)
      chmodSync(targetBinary, 0o755);
      console.log(`✓ Created ${packageName}`);
    } else {
      console.error(`✗ Binary not found: ${sourceBinary}`);
    }

    // Copy README
    const readmePath = join(PACKAGING_DIR, "PACKAGE_README.md");
    if (existsSync(readmePath)) {
      cpSync(readmePath, join(packageDir, "README.md"));
    }
  }
}

async function publishNpm(dryRun: boolean = true) {
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
    packageJson.optionalDependencies[dep] = VERSION;
  }

  writeFileSync(mainPackagePath, JSON.stringify(packageJson, null, 2) + "\n");
  console.log(`✓ Updated main package to version ${VERSION}`);
}

// CLI
const command = process.argv[2];

async function main() {
  switch (command) {
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

    case "publish": {
      const dryRun = process.argv.includes("--dry-run");
      await publishNpm(dryRun);
      break;
    }

    default: {
      {
        console.log(`
Usage: tsx pack.ts <command>

Commands:
	clean       Clean npm packages
	prepare     Prepare npm packages from GoReleaser binaries
	publish     Publish to npm (use --dry-run for testing)

Examples:
	tsx pack.ts prepare
	tsx pack.ts publish --dry-run
	VERSION=1.0.0 tsx pack.ts prepare

Note: Binaries are built by GoReleaser. This script only handles npm packaging.
      The lib/ directory (programmable API) is built by Taskfile (pack:build-api).
`);
      }
      process.exit(1);
    }
  }
}

await main();
