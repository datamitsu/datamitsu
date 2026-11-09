package bundled

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

// TestFindPlacementViolations pins the reserved-name matcher: everything
// Datamitsu owns is legal directly at the git root and a violation anywhere
// below it.
func TestFindPlacementViolations(t *testing.T) {
	root := string(filepath.Separator) + "repo"

	configNames := sourcefarm.AutoConfigNames
	if len(configNames) == 0 {
		t.Fatal("sourcefarm.AutoConfigNames is empty")
	}
	nestedConfig := filepath.Join(root, "pkg", configNames[0])
	nestedIgnore := filepath.Join(root, "deep", "nest", ".datamitsuignore")
	nestedDir := filepath.Join(root, "sub", ".datamitsu")

	files := []string{
		filepath.Join(root, configNames[0]),                   // root: legal
		filepath.Join(root, ".datamitsuignore"),               // root: legal
		nestedConfig,                                          // violation
		nestedIgnore,                                          // violation
		filepath.Join(root, "pkg", "main.go"),                 // unrelated
		filepath.Join(root, ".datamitsu", "configs", "x"),     // inside root dir: not a reserved base
		filepath.Join(root, "vendor", "datamitsu.config.mjs"), // violation
	}
	dirs := []string{
		root,                              // the root itself: legal
		filepath.Join(root, ".datamitsu"), // the root's own dir: legal
		nestedDir,                         // violation
		filepath.Join(root, "sub"),        // unrelated
	}

	got := FindPlacementViolations(root, files, dirs)

	want := []string{nestedConfig, nestedDir, nestedIgnore, filepath.Join(root, "vendor", "datamitsu.config.mjs")}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("FindPlacementViolations = %v, want %v", got, want)
	}
}

func TestFindPlacementViolationsNone(t *testing.T) {
	root := string(filepath.Separator) + "repo"
	if got := FindPlacementViolations(root, nil, nil); len(got) != 0 {
		t.Errorf("empty inputs produced %v", got)
	}

	files := []string{filepath.Join(root, "datamitsu.config.ts")}
	dirs := []string{root, filepath.Join(root, ".datamitsu")}
	if got := FindPlacementViolations(root, files, dirs); len(got) != 0 {
		t.Errorf("root-level names must not be violations, got %v", got)
	}
}
