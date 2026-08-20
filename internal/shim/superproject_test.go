package shim

import "testing"

// TestClassifyGitDir pins which shapes are decided from the path alone. Only
// gitLinkAmbiguous costs a git subprocess, and only gitLinkSubmodule reaches the
// registration proof that may let the manifest search climb.
func TestClassifyGitDir(t *testing.T) {
	tests := []struct {
		name     string
		gitdir   string
		root     string
		wantKind gitLink
	}{
		{
			name:     "submodule of an ancestor",
			gitdir:   "/w/outer/.git/modules/vendor/lib",
			root:     "/w/outer/vendor/lib",
			wantKind: gitLinkSubmodule,
		},
		{
			name:     "nested submodule climbs one level",
			gitdir:   "/w/outer/.git/modules/mid/modules/inner",
			root:     "/w/outer/mid/inner",
			wantKind: gitLinkSubmodule,
		},
		{
			// The modules directory of a repository that does not contain this
			// working tree is a link git would not follow.
			name:     "modules of a repository that is not an ancestor",
			gitdir:   "/w/elsewhere/.git/modules/lib",
			root:     "/w/outer/vendor/lib",
			wantKind: gitLinkStandalone,
		},
		{
			name:     "linked worktree has no superproject",
			gitdir:   "/w/main/.git/worktrees/wt",
			root:     "/w/outer/wt",
			wantKind: gitLinkStandalone,
		},
		{
			// A linked worktree of a submodule: which link to follow is not
			// decidable from the path, so git decides it.
			name:     "both markers is left to git",
			gitdir:   "/w/outer/.git/modules/lib/worktrees/wt",
			root:     "/w/outer/lib-wt",
			wantKind: gitLinkAmbiguous,
		},
		{
			name:     "no .git component",
			gitdir:   "/w/outer/repos/lib",
			root:     "/w/outer/vendor/lib",
			wantKind: gitLinkAmbiguous,
		},
		{
			name:     "unknown directory under .git",
			gitdir:   "/w/outer/.git/refs",
			root:     "/w/outer/vendor/lib",
			wantKind: gitLinkAmbiguous,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if kind := classifyGitDir(tt.gitdir, tt.root); kind != tt.wantKind {
				t.Errorf("classifyGitDir(%q, %q) = %v, want %v", tt.gitdir, tt.root, kind, tt.wantKind)
			}
		})
	}
}

// TestParseGitFile pins that only the single `gitdir:` line git writes is
// parsed. Anything else is a shape to hand to git, not to guess at.
func TestParseGitFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantOK  bool
	}{
		{name: "the line git writes", content: "gitdir: ../.git/modules/lib\n", want: "../.git/modules/lib", wantOK: true},
		{name: "no trailing newline", content: "gitdir: /w/outer/.git/modules/lib", want: "/w/outer/.git/modules/lib", wantOK: true},
		{name: "extra content", content: "gitdir: /a\nsomething else\n"},
		{name: "empty target", content: "gitdir:   \n"},
		{name: "not a gitdir line", content: "worktree: /a\n"},
		{name: "empty file", content: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseGitFile(tt.content)

			if ok != tt.wantOK || got != tt.want {
				t.Errorf("parseGitFile(%q) = (%q, %v), want (%q, %v)", tt.content, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
