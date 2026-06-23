// Package clitest provides a reusable harness for blackbox/CLI tests that
// drive the built datamitsu binary as a subprocess and collect coverage via
// GOCOVERDIR.
//
// This file is an intentional placeholder that grows into the harness over the
// course of the CLI blackbox test suite (binary.go, run.go, project.go,
// golden.go). For now it asserts the toolchain prerequisite that the whole tier
// depends on: Go >= 1.20, where `go build -cover` + GOCOVERDIR can collect
// coverage from subprocess runs.
package clitest

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestToolchainSupportsGOCOVERDIR fails the build if the toolchain predates the
// Go 1.20 coverage-from-subprocess support that this test tier relies on.
func TestToolchainSupportsGOCOVERDIR(t *testing.T) {
	v := runtime.Version() // e.g. "go1.26.4"
	rest, ok := strings.CutPrefix(v, "go")
	if !ok {
		// Non-standard toolchain string (e.g. a devel build); don't block.
		t.Skipf("unrecognized runtime version %q, skipping toolchain gate", v)
	}

	major, minor := parseGoMajorMinor(t, rest)
	if major < 1 || (major == 1 && minor < 20) {
		t.Fatalf("Go >= 1.20 required for GOCOVERDIR subprocess coverage, got %q", v)
	}
}

// parseGoMajorMinor extracts the major and minor numbers from a version string
// like "1.26.4" or "1.21rc1".
func parseGoMajorMinor(t *testing.T, rest string) (major, minor int) {
	t.Helper()
	parts := strings.SplitN(rest, ".", 3)
	if len(parts) < 2 {
		t.Fatalf("cannot parse Go version %q", rest)
	}
	major = mustAtoiPrefix(t, parts[0])
	minor = mustAtoiPrefix(t, parts[1])
	return major, minor
}

// mustAtoiPrefix parses the leading integer of s (tolerating suffixes like
// "rc1" or "beta2" on pre-release minor versions).
func mustAtoiPrefix(t *testing.T, s string) int {
	t.Helper()
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		t.Fatalf("no leading integer in version component %q", s)
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		t.Fatalf("parse version component %q: %v", s, err)
	}
	return n
}
