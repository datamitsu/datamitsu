package config

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func TestApplyDerivedOfficialURLs(t *testing.T) {
	apps := binmanager.MapOfApps{
		// The maintainer points eslint at its own documentation rather than at npm.
		"eslint": {
			Node:        &binmanager.AppConfigNode{PackageName: "eslint", Version: "9.0.0"},
			OfficialURL: "https://eslint.org/",
		},
		"ruff":  {Uv: &binmanager.AppConfigUV{PackageName: "ruff", Version: "0.6.0"}},
		"git":   {Shell: &binmanager.AppConfigShell{Name: "git"}},
		"spacy": {Node: &binmanager.AppConfigNode{PackageName: "  prettier  ", Version: "3"}},
	}
	ApplyDerivedOfficialURLs(apps)

	// Explicit beats derived, and stays marked as the configuration's own answer.
	if got := apps["eslint"]; got.OfficialURL != "https://eslint.org/" || got.OfficialURLDerived {
		t.Fatalf("eslint = %q (derived %v), want the declared URL and derived=false", got.OfficialURL, got.OfficialURLDerived)
	}
	if got := apps["ruff"]; got.OfficialURL != "https://pypi.org/project/ruff/" || !got.OfficialURLDerived {
		t.Fatalf("ruff = %q (derived %v), want the PyPI page marked derived", got.OfficialURL, got.OfficialURLDerived)
	}
	// A shell app resolves through the host PATH; there is nothing to link to.
	if got := apps["git"]; got.OfficialURL != "" || got.OfficialURLDerived {
		t.Fatalf("git = %q (derived %v), want no link at all", got.OfficialURL, got.OfficialURLDerived)
	}
	if got := apps["spacy"]; got.OfficialURL != "https://www.npmjs.com/package/prettier" {
		t.Fatalf("spacy = %q, want the package name trimmed before deriving", got.OfficialURL)
	}
}

func TestOfficialURLRemovedInChildLayer(t *testing.T) {
	// Removal uses the config's own undefined semantics: the child layer leaves
	// the app without a declared URL, which is the same state as never having had
	// one. An app that can derive one falls back to it; an app that cannot — here
	// a shell app — is left with no link, which is what the reader sees.
	inherited := binmanager.MapOfApps{
		"tool": {Shell: &binmanager.AppConfigShell{Name: "tool"}, OfficialURL: "https://wiki.example.com/tool"},
	}
	ApplyDerivedOfficialURLs(inherited)
	if got := inherited["tool"].OfficialURL; got != "https://wiki.example.com/tool" {
		t.Fatalf("inherited tool = %q, want the declared URL", got)
	}

	removed := binmanager.MapOfApps{"tool": {Shell: &binmanager.AppConfigShell{Name: "tool"}}}
	ApplyDerivedOfficialURLs(removed)
	if got := removed["tool"]; got.OfficialURL != "" || got.OfficialURLDerived {
		t.Fatalf("tool after removal = %q (derived %v), want none", got.OfficialURL, got.OfficialURLDerived)
	}

	// The same removal on an app that declares where it comes from falls back to
	// the derived link rather than to nothing.
	derivable := binmanager.MapOfApps{"ruff": {Uv: &binmanager.AppConfigUV{PackageName: "ruff", Version: "0.6.0"}}}
	ApplyDerivedOfficialURLs(derivable)
	if got := derivable["ruff"]; got.OfficialURL != "https://pypi.org/project/ruff/" || !got.OfficialURLDerived {
		t.Fatalf("ruff after removal = %q (derived %v), want the derived page", got.OfficialURL, got.OfficialURLDerived)
	}
}

func TestValidateOfficialURLShapeOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "unset"},
		{name: "https", value: "https://eslint.org/docs/latest/"},
		{name: "http", value: "http://internal.example/wiki/tool"},
		{name: "relative", value: "/docs/tool", wantErr: "absolute http or https URL"},
		{name: "scheme", value: "ftp://example.com/tool", wantErr: "absolute http or https URL"},
		{name: "javascript", value: "javascript:alert(1)", wantErr: "absolute http or https URL"},
		{name: "no host", value: "https:///docs", wantErr: "must name a host"},
		{name: "untrimmed", value: " https://example.com ", wantErr: "whitespace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOfficialURL(tc.value)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateOfficialURL(%q) = %v, want nil", tc.value, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateOfficialURL(%q) = %v, want an error containing %q", tc.value, err, tc.wantErr)
			}
		})
	}
}
