package managedconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/goccy/go-yaml"
)

func TestReadAISkillRejectsPortableAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"C:/bundle/skill", "c:relative", "/bundle/skill", "../skill", "bundle\\skill", ""} {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			if _, err := readAISkill(root, path); err == nil {
				t.Fatalf("readAISkill(%q) succeeded", path)
			}
		})
	}
}

func TestReadAISkillRejectsCaseFoldedResourcePathCollisions(t *testing.T) {
	for _, files := range [][]string{{"Dir/a", "dir/b"}} {
		t.Run(strings.Join(files, "_"), func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "skill")
			for _, name := range files {
				path := filepath.Join(source, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := readAISkill(root, "skill"); err == nil {
				t.Fatalf("case-fold collision accepted for %v", files)
			}
		})
	}
}

func TestRenderAISkillFrontmatterPreservesFinalBlockScalarNewline(t *testing.T) {
	input := []byte("---\nname: dm-example\ndescription: |\n  first\n  second\n---\nbody\n")
	out, err := renderAISkillFrontmatter(input, config.AISkillTarget{Enabled: nil, Description: nil, DisableModelInvocation: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	body, _, ok := aiFrontmatter(out)
	if !ok {
		t.Fatalf("rendered frontmatter is invalid: %s", out)
	}
	var fields map[string]any
	if err := yaml.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if got := fields["description"]; got != "first\nsecond\n" {
		t.Fatalf("description = %#v, want final newline preserved", got)
	}
}
