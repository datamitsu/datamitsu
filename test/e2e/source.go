// Package e2e holds the gated, OCI-seeded end-to-end CLI tier. Unlike the
// offline golden suite in test/cli, this tier exercises the real
// seed/install/exec/init/check/fix/lint pipelines against the user's released,
// digest-pinned config and therefore needs network and registry access.
//
// Every test file in this tier carries the `//go:build e2e_oci` build tag and
// additionally calls RequireOCIE2E, so the tier never runs in the default build
// or default CI. This file deliberately has NO build tag so the package always
// contains at least one compilable Go source (and `go test ./test/e2e/...` in
// the default build reports "no test files" rather than failing to build).
package e2e

// OCIConfigSource is the canonical upstream location of the vendored,
// digest-pinned config in testdata/datamitsu.config.oci-ghcr.js. It is the
// single source of truth for that fixture.
//
// No stable datamitsu-config release declares a pnpm runtime yet, so the
// fixture comes from the unstable npm package this repository already pins in
// its pnpm catalog. To update it, re-vendor from the matching package version:
//
//	curl -sSL https://registry.npmjs.org/@shibanet0/datamitsu-config/-/datamitsu-config-0.0.0-unstable.20260912.f201136.tgz \
//	  | tar -xzO package/datamitsu.config.oci-ghcr.js > test/e2e/testdata/datamitsu.config.oci-ghcr.js
//
// The vendored file carries the bundle's oci.ref + oci.digest, so it pins the
// whole OCI bundle by content; no separate hash field is needed here.
const OCIConfigSource = "https://registry.npmjs.org/@shibanet0/datamitsu-config/-/datamitsu-config-0.0.0-unstable.20260912.f201136.tgz"

// VendoredConfigRelPath is the path of the vendored config relative to this
// package directory.
const VendoredConfigRelPath = "testdata/datamitsu.config.oci-ghcr.js"
