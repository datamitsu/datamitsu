export interface PlatformAsset {
  file: string;
  key: string;
}

export interface ReleaseAsset {
  file: string;
  sha256: string;
}

// Parses a GoReleaser checksums.txt: each line is "<sha256>  <filename>"
// (BSD-style "*" binary marker tolerated). Returns filename -> lowercase sha256.
export function parseChecksums(content: string): Map<string, string> {
  const map = new Map<string, string>();
  for (const line of content.split("\n")) {
    const match = /^([0-9a-f]{64})[ \t]+\*?([^\s](?:.*[^\s])?)$/i.exec(line.trim());
    const sha = match?.[1];
    const file = match?.[2];
    if (sha !== undefined && file !== undefined) {
      map.set(file, sha.toLowerCase());
    }
  }
  return map;
}

// The GitHub-hosted runner platforms the action installs for, with their
// GoReleaser archive filenames for a given version (no leading "v").
export function platformAssets(version: string): PlatformAsset[] {
  const targets = [
    { goarch: "amd64", goos: "linux" },
    { goarch: "arm64", goos: "linux" },
    { goarch: "amd64", goos: "darwin" },
    { goarch: "arm64", goos: "darwin" },
    { goarch: "amd64", goos: "windows" },
    { goarch: "arm64", goos: "windows" },
  ];
  return targets.map(({ goarch, goos }) => ({
    file:
      goos === "windows"
        ? `datamitsu_${version}_${goos}_${goarch}.zip`
        : `datamitsu_${version}_${goos}_${goarch}.tar.gz`,
    key: `${goos}_${goarch}`,
  }));
}
