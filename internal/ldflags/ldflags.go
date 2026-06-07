// Package ldflags holds build-time identity values, some of which are
// overridden via the Go linker's -X flag at release time.
package ldflags

// PackageName is the canonical project name, used to derive env var prefixes,
// cache directories and config paths.
const PackageName = "datamitsu"

// ConfigDTSFilename is the filename of the TypeScript ambient declaration file
// shipped for user config authoring.
const ConfigDTSFilename = "config.d.ts"

// Version is the program version, injected at build time via -ldflags -X and
// defaulting to "dev" for local builds.
var Version = "dev"

// ImageRepo is the OCI repository (without registry host) of the published
// datamitsu base image this binary corresponds to, injected at build time. It
// differs by release channel — datamitsu/datamitsu (stable) vs
// datamitsu/datamitsu-unstable (unstable) — because the version string alone
// cannot identify the channel's repository. Used by `devtools dockerfile` to
// write a correct FROM. Defaults to the stable repo for local builds.
var ImageRepo = "datamitsu/datamitsu"

// ImageTag is the published image tag matching this build (the exact GHCR tag),
// injected at build time. Stable releases tag by version (e.g. 0.0.19); unstable
// builds use a distinct tag_name (e.g. unstable-<date>-<sha>) that is NOT the
// version string. Empty for local builds, where callers fall back to Version.
var ImageTag = ""
