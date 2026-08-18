// Package ociref parses OCI repository references. It is the single definition
// of datamitsu's reference grammar, shared by config validation, bundle
// seeding, bundle status and the `store seed` CLI argument — all of which used
// to carry their own copy of the same strings.Cut and, in one case, no syntax
// check at all.
//
// It is a leaf: stdlib only. Config validation must be able to check a
// reference without dragging in the registry client, the progress UI and the
// HTTP stack that a puller needs.
package ociref

import (
	"errors"
	"regexp"
	"strings"
)

// ErrRefSyntax and ErrRefNoHost are the two ways a reference can be rejected.
// They are sentinels rather than formatted errors because each caller frames
// the reference differently ("oci: ref %q", "parser %q: oci.ref %q",
// "reference %q") and those messages are part of the CLI contract.
var (
	ErrRefSyntax = errors.New("not a valid repository reference (expected host[:port]/path, lowercase, no tag and no digest)")
	ErrRefNoHost = errors.New("must include the registry host as its first segment (e.g. ghcr.io/owner/repo)")
)

// refPattern matches a repository reference: a registry host (optionally with
// a port) followed by at least one path component. The character set excludes
// ":" outside the port position and "@" entirely, so a ref carrying a ":tag"
// or "@digest" suffix cannot match — content is pinned exclusively by the
// digest declared alongside the reference.
var refPattern = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]+)?(/[a-z0-9._-]+)+$`)

// Parse splits a repository reference into its registry host and repository
// path.
//
// The two checks run in this order — syntax, then host — and the order is part
// of the contract: "ghcr.io" (a host with no path) is a syntax error, while
// "owner/repo" parses fine and fails only for want of a registry host. Callers
// discriminate the two to produce their own message, and config validation has
// a test pinning exactly that distinction.
func Parse(ref string) (host, repo string, err error) {
	if !refPattern.MatchString(ref) {
		return "", "", ErrRefSyntax
	}
	host, repo, _ = strings.Cut(ref, "/") // the pattern guarantees a "/"
	// Require an explicit registry host: the first segment must look like a
	// hostname (contain a dot or a port) or be "localhost". There is no default
	// host and no docker.io magic.
	if host != "localhost" && !strings.Contains(host, ".") && !strings.Contains(host, ":") {
		return "", "", ErrRefNoHost
	}
	return host, repo, nil
}

// SplitTag separates a "<ref>:<tag>" pair. The colon is sought after the last
// "/" so a ported registry host is not mistaken for a tag separator:
// "localhost:5000/o/r:latest" splits into "localhost:5000/o/r" and "latest",
// not into "localhost" and "5000/o/r:latest". A reference with no tag returns
// ok=false.
func SplitTag(s string) (ref, tag string, ok bool) {
	lastSlash := strings.LastIndexByte(s, '/')
	colon := strings.LastIndexByte(s, ':')
	if colon < 0 || colon < lastSlash {
		return s, "", false
	}
	return s[:colon], s[colon+1:], true
}
