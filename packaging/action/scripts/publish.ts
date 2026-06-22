import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";

const ACTION_DIR = join(import.meta.dirname, "..");

const version = (process.env.VERSION ?? "").replace(/^v/, "");
const target = process.env.TARGET_DIR;
if (version === "") {
  throw new Error("VERSION env var is required (e.g. v0.1.5)");
}
if (target === undefined || target === "") {
  throw new Error("TARGET_DIR env var is required (a checked-out setup-datamitsu repo)");
}

mkdirSync(join(target, "dist"), { recursive: true });
copyFileSync(join(ACTION_DIR, "action.yml"), join(target, "action.yml"));
copyFileSync(join(ACTION_DIR, "dist", "index.mjs"), join(target, "dist", "index.mjs"));
copyFileSync(join(ACTION_DIR, "README.md"), join(target, "README.md"));
// Ship the human-readable baked hashes alongside the bundle so reviewers can
// audit exactly which binaries a published tag verifies.
copyFileSync(join(ACTION_DIR, "src", "generated.ts"), join(target, "hashes.ts"));

const tag = `v${version}`;
const git = (...args: string[]): void => {
  execFileSync("git", ["-C", target, ...args], { stdio: "inherit" });
};

git("config", "user.name", "datamitsu-bot");
git("config", "user.email", "datamitsu-bot@users.noreply.github.com");
git("add", "-A");
git("commit", "-m", `chore: release ${tag}`);
// Immutable: tag is created without force; a duplicate release fails loudly.
git("tag", tag);
git("push", "origin", "HEAD:main");
git("push", "origin", tag);

// Pushing a tag alone does not create a Release; create one so the version
// appears in the GitHub Marketplace listing with notes. Auth via GH_TOKEN.
const notes = `Installs datamitsu ${version}.\n\nCLI changelog: https://github.com/datamitsu/datamitsu/releases/tag/${tag}`;
execFileSync(
  "gh",
  [
    "release",
    "create",
    tag,
    "--repo",
    "datamitsu/setup-datamitsu",
    "--verify-tag",
    "--title",
    tag,
    "--notes",
    notes,
  ],
  { stdio: "inherit" },
);
console.log(`✓ published ${tag}: pushed to ${target} and created GitHub Release`);
