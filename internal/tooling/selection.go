package tooling

// SelectionMode is what a run was asked to cover.
type SelectionMode int

const (
	// SelectionAll is the whole repository: no paths named, cwd at the git root.
	SelectionAll SelectionMode = iota
	// SelectionSubtree is the directory the user is standing in, below the root.
	SelectionSubtree
	// SelectionPaths is an explicit set of paths.
	SelectionPaths
	// SelectionEmpty is explicitly nothing — --file-scoped with an empty index.
	// Distinct from SelectionAll, which a nil file slice could not express: the
	// planner used to read both as "no files given" and sweep every glob, so an
	// empty staged set ran the whole repository.
	SelectionEmpty
)

// Selection is what the run targets. It replaces a bare []string, which
// conflated "nothing was selected" with "everything is selected".
type Selection struct {
	Mode  SelectionMode
	Dir   string   // subtree root, for SelectionSubtree
	Paths []string // absolute paths, for SelectionPaths
}

// NewSelection classifies a run from its inputs. paths must already be absolute.
func NewSelection(rootPath, cwdPath string, paths []string, fileScoped bool) Selection {
	switch {
	case len(paths) > 0:
		return Selection{Mode: SelectionPaths, Paths: paths}
	case fileScoped:
		return Selection{Mode: SelectionEmpty}
	case cwdPath != rootPath:
		return Selection{Mode: SelectionSubtree, Dir: cwdPath}
	default:
		return Selection{Mode: SelectionAll}
	}
}

// Files returns the explicitly named paths, or nil for every other mode.
func (s Selection) Files() []string {
	if s.Mode == SelectionPaths {
		return s.Paths
	}
	return nil
}

func (s Selection) String() string {
	switch s.Mode {
	case SelectionAll:
		return "all"
	case SelectionSubtree:
		return "subtree"
	case SelectionPaths:
		return "paths"
	case SelectionEmpty:
		return "empty"
	}
	return "all"
}
