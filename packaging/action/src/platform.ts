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

// Maps a Node runner (process.platform/process.arch) to the datamitsu release
// target naming used in GoReleaser asset filenames.
export function resolveTarget(platform: string, arch: string): Target {
  const goos = GOOS[platform];
  const goarch = GOARCH[arch];
  if (!goos || !goarch) {
    throw new Error(`Unsupported runner platform: ${platform}/${arch}`);
  }
  return { goarch, goos };
}
