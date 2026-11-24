import { execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFileSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";

const version = process.env.VERSION ?? "";
if (!/^\d+\.\d+\.\d+$/.test(version)) {
  throw new Error("VERSION must be a stable numeric SemVer (e.g. 0.1.5)");
}

const targetValue = process.env.TARGET_DIR;
const sourceValue = process.env.APPROVED_DIST_DIR;
if (!targetValue || !sourceValue) {
  throw new Error("TARGET_DIR (checked destination checkout) and APPROVED_DIST_DIR are required");
}
const target = resolve(targetValue);
const approvedDist = resolve(sourceValue);
const tag = `v${version}`;
const repo = "datamitsu/setup-datamitsu";
const run = (command: string, args: string[]): string =>
  execFileSync(command, args, { encoding: "utf8", stdio: ["ignore", "pipe", "inherit"] }).trim();
const git = (...args: string[]): string => run("git", ["-C", target, ...args]);
const digest = (path: string): string =>
  createHash("sha256").update(readFileSync(path)).digest("hex");
const expected = ["action.yml", "README.md", "dist/index.mjs"];

// Preserve all unrelated destination content; only managed publication paths are staged.
const files = (dir: string, prefix = ""): string[] =>
  readdirSync(dir)
    .flatMap((name) => {
      const path = join(dir, name);
      const relative = prefix ? `${prefix}/${name}` : name;
      return statSync(path).isDirectory() ? files(path, relative) : [relative];
    })
    .sort();

const templateDir = resolve(import.meta.dirname, "..");
const approvedFiles = ["action.yml", "README.md", "dist/index.mjs", "SHA256SUMS"];
const approvedActual = files(approvedDist);
if (approvedActual.join("\0") !== [...approvedFiles].sort().join("\0")) {
  throw new Error(`approved action artifact must contain exactly ${approvedFiles.join(", ")}`);
}
const expectedHashes: Record<string, string> = {
  "action.yml": digest(join(templateDir, "action.yml")),
  "dist/index.mjs": digest(join(approvedDist, "dist/index.mjs")),
  "README.md": digest(join(approvedDist, "README.md")),
};
const manifest = readFileSync(join(approvedDist, "SHA256SUMS"), "utf8");
for (const path of expected) {
  const listed = manifest
    .split("\n")
    .find((line) => line.endsWith(`  ${path}`))
    ?.split(/\s+/)[0];
  if (listed !== expectedHashes[path]) throw new Error(`SHA256SUMS does not approve ${path}`);
}
for (const path of expected) {
  if (path !== "dist/index.mjs" && digest(join(approvedDist, path)) !== expectedHashes[path]) {
    throw new Error(`approved ${path} does not match trusted SHA-256`);
  }
}
if (git("status", "--porcelain").length !== 0)
  throw new Error("destination checkout must be clean before publication");
if (git("branch", "--show-current") !== "main")
  throw new Error("destination checkout must be on main");
if (
  git("remote", "get-url", "origin")
    .replace(/\.git$/, "")
    .toLowerCase() !== `https://github.com/${repo}`
) {
  throw new Error("destination origin is not the approved repository");
}
git("fetch", "origin", "main", "--tags");
if (git("rev-parse", "HEAD") !== git("rev-parse", "origin/main")) {
  throw new Error("destination checkout must match current origin/main");
}
const currentReadme = readFileSync(join(target, "README.md"), "utf8");
const currentVersion = currentReadme.match(/setup-datamitsu@v(\d+\.\d+\.\d+)/)?.[1];
const compareVersions = (left: string, right: string): number => {
  const a = left.split(".").map(Number);
  const b = right.split(".").map(Number);
  for (let index = 0; index < 3; index++) {
    const first = a[index];
    const second = b[index];
    if (first === undefined || second === undefined) throw new Error("Invalid version comparison");
    if (first !== second) return first - second;
  }
  return 0;
};
const checkAdvance = (): void => {
  if (currentVersion && compareVersions(version, currentVersion) < 0) {
    throw new Error(`refusing to publish ${version} below destination version ${currentVersion}`);
  }
  const tags = git("tag", "--list", "v[0-9]*").split("\n").filter(Boolean);
  for (const existing of tags) {
    const existingVersion = existing.slice(1);
    if (/^\d+\.\d+\.\d+$/.test(existingVersion) && compareVersions(version, existingVersion) < 0) {
      throw new Error(`refusing to publish ${version} below existing stable tag ${existing}`);
    }
  }
};
const releaseDetails = ():
  | undefined
  | { isDraft: boolean; isPrerelease: boolean; tagName: string } => {
  const result = spawnSync(
    "gh",
    ["release", "view", tag, "--repo", repo, "--json", "tagName,isDraft,isPrerelease"],
    { encoding: "utf8" },
  );
  if (result.status === 0) return JSON.parse(result.stdout);
  if (result.status === 1 && /^release not found\s*$/.test(result.stderr)) return undefined;
  throw result.error ?? new Error(`cannot check the destination GitHub Release: ${result.stderr}`);
};
const tagExists = (): boolean => {
  try {
    git("rev-parse", "--verify", `refs/tags/${tag}`);
    return true;
  } catch {
    return false;
  }
};
const verifyExistingTag = (): boolean => {
  if (!tagExists()) return false;
  const commit = git("rev-parse", `refs/tags/${tag}^{commit}`);
  const readme = git("show", `${tag}:README.md`);
  const taggedVersion = readme.match(/setup-datamitsu@v(\d+\.\d+\.\d+)/)?.[1];
  if (taggedVersion !== version) throw new Error(`existing ${tag} has mismatched README identity`);
  for (const path of expected) {
    const approvedBlob = git("hash-object", join(approvedDist, path));
    if (git("rev-parse", `${tag}:${path}`) !== approvedBlob)
      throw new Error(`existing ${tag} has different ${path}`);
  }
  const release = releaseDetails();
  if (release && (release.tagName !== tag || release.isDraft || release.isPrerelease)) {
    throw new Error(`existing ${tag} is not a confirmed stable GitHub Release`);
  }
  if (!release) {
    if (git("rev-parse", "HEAD") !== commit)
      throw new Error(`cannot recover ${tag} release from this checkout`);
    run("gh", [
      "release",
      "create",
      tag,
      "--repo",
      repo,
      "--verify-tag",
      "--title",
      tag,
      "--notes",
      `Installs datamitsu ${version}.\n\nCLI changelog: https://github.com/datamitsu/datamitsu/releases/tag/${tag}`,
    ]);
  }
  return true;
};

if (verifyExistingTag()) {
  console.log(`✓ ${tag} has matching action files and a confirmed stable GitHub Release`);
} else {
  checkAdvance();
  if (releaseDetails())
    throw new Error(`GitHub Release ${tag} exists without a matching destination tag`);
  for (const path of expected) copyFileSync(join(approvedDist, path), join(target, path));
  git("add", "--", ...expected);
  git(
    "-c",
    "user.name=datamitsu-bot",
    "-c",
    "user.email=datamitsu-bot@users.noreply.github.com",
    "commit",
    "-m",
    `chore: release ${tag}`,
  );
  const tree = git("rev-parse", "HEAD^{tree}");
  git("tag", tag);
  git("push", "--atomic", "origin", "HEAD:main", `refs/tags/${tag}:refs/tags/${tag}`);
  git("fetch", "origin", `refs/tags/${tag}:refs/tags/${tag}`);
  if (git("rev-parse", `${tag}^{tree}`) !== tree)
    throw new Error(`remote tree mismatch for ${tag}`);
  if (!verifyExistingTag()) throw new Error(`destination tag ${tag} is missing after push`);
  const release = releaseDetails();
  if (!release || release.tagName !== tag || release.isDraft || release.isPrerelease)
    throw new Error(`could not confirm GitHub Release ${tag}`);
}
console.log(`✓ published ${tag} to ${repo}`);
