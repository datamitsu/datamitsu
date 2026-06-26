// AUTO-GENERATED at release time. Do not edit by hand. Maps "<goos>_<goarch>" to
// the datamitsu release asset filename and its SHA-256. The placeholder below
// (version 0.0.0, no assets) makes the bundled-binary path REFUSE to download — a
// real extension build bakes the matching datamitsu version and verified hashes
// here. Until then, install datamitsu on PATH (binaryMode "auto"/"system") or set
// datamitsu.path.
export interface ReleaseAsset {
  file: string;
  sha256: string;
}

export const VERSION = "0.0.0";

export const RELEASES: Record<string, ReleaseAsset> = {};
