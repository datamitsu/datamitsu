import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = new URL("../../", import.meta.url);
const pkg = JSON.parse(
  readFileSync(new URL("node_modules/@shibanet0/datamitsu-config/package.json", root), "utf8"),
);
const html = execFileSync(
  fileURLToPath(new URL("datamitsu", root)),
  [
    "--no-auto-config",
    "--config",
    "node_modules/@shibanet0/datamitsu-config/datamitsu.config.base.js",
    "inspect",
    "--output",
    "-",
  ],
  { cwd: root, encoding: "utf8", maxBuffer: 8 * 1024 * 1024 },
);
const match = html.match(
  /<script type="application\/json" id="inspector-manifest">([\s\S]*?)<\/script>/,
);
if (!match) {
  throw new Error("Export has no manifest");
}
// Everything naming the configuration comes from the config itself (the
// manifest's name) or from the installed package — nothing is typed in here.
const capture = {
  capturedAt: new Date().toISOString(),
  manifest: JSON.parse(match[1]!),
  package: pkg.name,
  version: pkg.version,
};
writeFileSync(
  new URL("../src/data/reference-config.json", import.meta.url),
  JSON.stringify(capture, null, 2) + "\n",
);
