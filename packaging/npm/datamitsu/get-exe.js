import { existsSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";

export function getExePath() {
  const platform = getPlatform();
  const arch = getArch();
  const ext = platform === "win32" ? ".exe" : "";

  const packageName = `@datamitsu/datamitsu-${platform}-${arch}`;
  const exeName = `datamitsu${ext}`;

  try {
    // Try to resolve the platform-specific package using createRequire for ES modules
    const require = createRequire(import.meta.url);
    const packagePath = require.resolve(`${packageName}/package.json`);
    const packageDir = join(packagePath, "..");
    const exePath = join(packageDir, exeName);

    if (existsSync(exePath)) {
      return exePath;
    }
  } catch {
    // Package not found
  }

  throw new Error(
    `datamitsu binary not found for platform ${platform}-${arch}.\n` +
      `Please make sure the package "${packageName}" is installed.\n` +
      `If you're seeing this error, try reinstalling datamitsu: npm install @datamitsu/datamitsu`,
  );
}

function getArch() {
  const arch = process.arch;
  // Normalize architecture names
  if (arch === "x64") {
    return "x64";
  }
  if (arch === "arm64" || arch === "aarch64") {
    return "arm64";
  }
  return arch;
}

function getPlatform() {
  const platform = process.platform;
  // Keep win32 as-is to match package names
  if (platform === "cygwin") {
    return "win32";
  }
  return platform;
}
