export interface Target {
  goarch: string;
  goos: string;
}

const GOOS: Record<string, string> = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows",
};

const GOARCH: Record<string, string> = {
  arm64: "arm64",
  x64: "amd64",
};

// The datamitsu executable name inside a release archive for this OS.
export function binaryName(goos: string): string {
  return goos === "windows" ? "datamitsu.exe" : "datamitsu";
}

// Maps a Node host (process.platform/process.arch) to the datamitsu release
// target naming used in GoReleaser asset filenames.
export function resolveTarget(platform: string, arch: string): Target {
  const goos = GOOS[platform];
  const goarch = GOARCH[arch];
  if (goos === undefined || goarch === undefined) {
    throw new Error(`Unsupported platform: ${platform}/${arch}`);
  }
  return { goarch, goos };
}

export function targetKey(target: Target): string {
  return `${target.goos}_${target.goarch}`;
}
