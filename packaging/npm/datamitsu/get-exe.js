import { existsSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";

export function getExePath() {
  const ext = process.platform === "win32" ? ".exe" : "";
  const packageName = `@datamitsu/datamitsu-${process.platform}-${process.arch}`;
  const exeName = `datamitsu${ext}`;

  try {
    const require = createRequire(import.meta.url);
    const packageJson = require.resolve(`${packageName}/package.json`);
    const exePath = join(dirname(packageJson), exeName);

    if (existsSync(exePath)) {
      return exePath;
    }
  } catch {
    // Package not found
  }

  throw new Error(
    `datamitsu binary not found for platform ${process.platform}-${process.arch}.\n` +
      `Please make sure the package "${packageName}" is installed.\n` +
      `If you're seeing this error, try reinstalling: npm install @datamitsu/datamitsu`,
  );
}
