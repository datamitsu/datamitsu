package toolenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The committed page must be exactly what the lists render. Markdown emits the
// formatters' own layout, so task gen:toolenv-doc's formatting step changes
// nothing and the comparison needs no formatter.
func TestDocMatchesTheCommittedPage(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(DocPath)))
	if err != nil {
		t.Fatalf("read the committed page: %v", err)
	}
	if string(committed) != Markdown() {
		t.Fatalf("%s is stale or was edited by hand; run `task gen:toolenv-doc` and commit the result", DocPath)
	}
}

func TestDocListsEveryEntry(t *testing.T) {
	page := Markdown()
	for _, e := range exactNames {
		if !strings.Contains(page, "| `"+e.Name+"` ") {
			t.Errorf("the page has no row for %s", e.Name)
		}
	}
	for _, e := range prefixes {
		if !strings.Contains(page, "| `"+e.Name+"*` ") {
			t.Errorf("the page has no row for %s*", e.Name)
		}
	}
	for _, e := range kept {
		if !strings.Contains(page, "| `"+e.Name+"` ") {
			t.Errorf("the page has no row for kept %s", e.Name)
		}
	}
}
