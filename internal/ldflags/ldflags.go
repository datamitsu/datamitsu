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
