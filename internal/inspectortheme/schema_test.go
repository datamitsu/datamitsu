package inspectortheme

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// SchemaID is where the published schema lives, and what a theme file points its
// $schema at.
const SchemaID = "https://datamitsu.com/schemas/inspector-theme.schema.json"

var schemaPath = filepath.Join("..", "..", "website", "static", "schemas", "inspector-theme.schema.json")

var update = flag.Bool("update", false, "rewrite the published inspector theme schema")

// The schema editors read must describe exactly what the parser accepts, so it
// is generated from the same table rather than maintained by hand.
func TestPublishedSchemaMatchesTheParser(t *testing.T) {
	want, err := Schema(SchemaID)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(schemaPath, want, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("%v; regenerate with: go test ./internal/inspectortheme -update", err)
	}
	if string(got) != string(want) {
		t.Fatal("the published theme schema is stale; regenerate with: go test ./internal/inspectortheme -update")
	}
}
