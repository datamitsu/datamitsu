import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const [target, version] = process.argv.slice(2);
if (!target || !version || !/^\d+\.\d+\.\d+$/.test(version)) {
  throw new Error("usage: prepare.ts <output-directory> <stable-version>");
}

const actionDir = join(import.meta.dirname, "..");
const readme = readFileSync(join(actionDir, "README.md"), "utf8");
if (!readme.includes("__VERSION__")) {
  throw new Error("setup action README has no __VERSION__ placeholders");
}

mkdirSync(join(target, "dist"), { recursive: true });
copyFileSync(join(actionDir, "action.yml"), join(target, "action.yml"));
copyFileSync(join(actionDir, "dist", "index.mjs"), join(target, "dist", "index.mjs"));
writeFileSync(
  join(target, "README.md"),
  readme.replaceAll("__VERSION__", version).replace(/^<!-- TEMPLATE:[\s\S]*?-->\n\n/, ""),
);
