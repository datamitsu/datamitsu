package bundled

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

// FindPlacementViolations reports Datamitsu-owned config artifacts below the
// git root. Datamitsu reads its configuration only at the git root (facts.
// GetGitRoot answers the topmost repository, and config discovery stats only
// that root), so the same names in any subdirectory are dead, misleading
// artifacts; the core config-placement check turns each one into a finding.
//
// The inputs are a gitignore-aware file list and the directories the walk
// descended into (traverser.FindFilesFromPathVisit), so the same visibility
// rules that govern a run govern this check: gitignored artifacts, .git trees
// and symlinked paths are invisible. Names directly at the git root — files
// and the root's own .datamitsu directory — are legal and never reported.
//
// The reserved names and their owners:
//   - datamitsu.config.{js,mjs,ts}: sourcefarm.AutoConfigNames;
//   - .datamitsuignore: the file cmd writes at the git root
//     (cmd/config_reconcile.go optInIgnoreFilename, unexported);
//   - .datamitsu (directory): config.DatamitsuDirName.
func FindPlacementViolations(root string, files, dirs []string) []string {
	reservedFiles := make(map[string]bool, len(sourcefarm.AutoConfigNames)+1)
	for _, name := range sourcefarm.AutoConfigNames {
		reservedFiles[name] = true
	}
	// The owner constant is unexported in cmd; keep the single literal here.
	reservedFiles[".datamitsuignore"] = true

	var out []string
	for _, f := range files {
		if !reservedFiles[filepath.Base(f)] {
			continue
		}
		if belowRoot(root, f) {
			out = append(out, f)
		}
	}
	for _, d := range dirs {
		if filepath.Base(d) == config.DatamitsuDirName && belowRoot(root, d) {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// belowRoot reports whether path sits strictly below root: a direct child of
// the root has a one-element relative path, anything deeper has more.
func belowRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && strings.ContainsRune(rel, filepath.Separator)
}
