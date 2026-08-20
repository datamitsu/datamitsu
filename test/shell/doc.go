// Package shell holds the real-shell integration tier for source mode.
//
// Every other tier tests Go. This one tests the thing the user actually
// experiences: the activation code is parsed by the shell it targets, the farm
// is on that shell's PATH, and the commands typed into it reach the versions
// the repository pins. It is the only tier that can prove the three properties
// source mode exists for — transparent versions, transparent branch switching,
// and lazy materialization — because all three are statements about a shell,
// not about a function.
//
// The tier is hermetic but not offline: a loopback httptest server stands in
// for the release host, so the real download, SHA-256 verification and install
// path run end to end against a stub "binary" that prints its version. Nothing
// leaves the machine.
//
// Tests skip cleanly when the shell they drive is not installed, and say in the
// skip message which property is therefore unverified on that machine.
//
// This file deliberately holds no tests so the package always contains one
// compilable Go source and carries its documentation in the usual place.
package shell
