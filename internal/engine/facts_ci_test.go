package engine

import "testing"

// facts().ci is the CI detection of internal/cienv, keyed as config JS reads
// it; Gitea and Forgejo set GITHUB_ACTIONS too and must not read as GitHub.
func TestFactsCI(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_EVENT_NAME", "pull_request")
	t.Setenv("GITHUB_REF", "refs/pull/42/merge")
	t.Setenv("GITHUB_SHA", "abc")
	t.Setenv("GITHUB_BASE_REF", "main")
	t.Setenv("GITEA_ACTIONS", "")
	t.Setenv("FORGEJO_ACTIONS", "")

	e := newTestEngine(t)
	v, err := e.vm.RunString("JSON.stringify(facts().ci)")
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	const want = `{"vendor":"github","isCI":true,"isPR":true,"sha":"abc","ref":"refs/pull/42/merge","baseRef":"main","prNumber":"42"}`
	if got := v.String(); got != want {
		t.Errorf("facts().ci = %s, want %s", got, want)
	}

	t.Setenv("GITEA_ACTIONS", "true")
	e = newTestEngine(t)
	v, err = e.vm.RunString("facts().ci.vendor")
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := v.String(); got != "gitea" {
		t.Errorf("facts().ci.vendor under GITEA_ACTIONS = %q, want gitea", got)
	}
}
