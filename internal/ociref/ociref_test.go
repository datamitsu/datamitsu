package ociref

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		wantHost string
		wantRepo string
		wantErr  error
	}{
		{name: "ghcr", ref: "ghcr.io/datamitsu/datamitsu-parsers", wantHost: "ghcr.io", wantRepo: "datamitsu/datamitsu-parsers"},
		{name: "ported localhost", ref: "localhost:5000/dm/parsers", wantHost: "localhost:5000", wantRepo: "dm/parsers"},
		{name: "plain localhost", ref: "localhost/dm/parsers", wantHost: "localhost", wantRepo: "dm/parsers"},
		{name: "corporate mirror", ref: "harbor.corp.example/dm/parsers", wantHost: "harbor.corp.example", wantRepo: "dm/parsers"},
		{name: "deep path", ref: "ghcr.io/a/b/c/d", wantHost: "ghcr.io", wantRepo: "a/b/c/d"},

		// Syntax: the reference itself is malformed.
		{name: "host only", ref: "ghcr.io", wantErr: ErrRefSyntax},
		{name: "empty", ref: "", wantErr: ErrRefSyntax},
		{name: "tag suffix", ref: "ghcr.io/a/b:latest", wantErr: ErrRefSyntax},
		{name: "digest suffix", ref: "ghcr.io/a/b@sha256:abc", wantErr: ErrRefSyntax},
		{name: "uppercase", ref: "GHCR.io/a/b", wantErr: ErrRefSyntax},
		{name: "non-numeric port", ref: "ghcr.io:port/a/b", wantErr: ErrRefSyntax},
		{name: "trailing slash", ref: "ghcr.io/a/", wantErr: ErrRefSyntax},
		{name: "leading slash", ref: "/a/b", wantErr: ErrRefSyntax},

		// Host: syntactically fine, but no registry host.
		{name: "no host", ref: "owner/repo", wantErr: ErrRefNoHost},
		{name: "single segment path", ref: "parsers/core", wantErr: ErrRefNoHost},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, repo, err := Parse(tt.ref)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Parse(%q) error = %v, want %v", tt.ref, err, tt.wantErr)
				}
				if host != "" || repo != "" {
					t.Errorf("Parse(%q) returned host=%q repo=%q alongside an error", tt.ref, host, repo)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.ref, err)
			}
			if host != tt.wantHost || repo != tt.wantRepo {
				t.Errorf("Parse(%q) = (%q, %q), want (%q, %q)", tt.ref, host, repo, tt.wantHost, tt.wantRepo)
			}
		})
	}
}

// The order of the two checks is what lets a caller tell "this is not a
// reference at all" from "this is a reference but names no registry".
func TestParse_HostOnlyIsSyntaxNotHost(t *testing.T) {
	if _, _, err := Parse("ghcr.io"); !errors.Is(err, ErrRefSyntax) {
		t.Errorf("Parse(\"ghcr.io\") = %v, want ErrRefSyntax (a host with no path is malformed, not host-less)", err)
	}
	if _, _, err := Parse("owner/repo"); !errors.Is(err, ErrRefNoHost) {
		t.Errorf("Parse(\"owner/repo\") = %v, want ErrRefNoHost", err)
	}
}

func TestSplitTag(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantRef string
		wantTag string
		wantOK  bool
	}{
		{name: "plain", in: "ghcr.io/a/b:latest", wantRef: "ghcr.io/a/b", wantTag: "latest", wantOK: true},
		// The bug this function exists for: cutting at the first colon made the
		// port the tag, so --resolve-tag could never work with a ported host.
		{name: "ported host", in: "localhost:5000/o/r:latest", wantRef: "localhost:5000/o/r", wantTag: "latest", wantOK: true},
		{name: "ported host no tag", in: "localhost:5000/o/r", wantRef: "localhost:5000/o/r", wantOK: false},
		{name: "no tag", in: "ghcr.io/a/b", wantRef: "ghcr.io/a/b", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, tag, ok := SplitTag(tt.in)
			if ok != tt.wantOK || ref != tt.wantRef || tag != tt.wantTag {
				t.Errorf("SplitTag(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.in, ref, tag, ok, tt.wantRef, tt.wantTag, tt.wantOK)
			}
		})
	}
}
