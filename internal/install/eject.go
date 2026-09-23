package install

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/datamitsu/datamitsu/internal/config"
)

// EjectConflictError is a repository file reconciliation would delete for an
// ejectable entry although the file differs from what datamitsu renders for
// it. A difference may be the project's own work, so it is never deleted.
type EjectConflictError struct {
	ConfigName string
	// Path is absolute; RelPath is the same file relative to the git root, for
	// messages.
	Path      string
	RelPath   string
	Tools     []string
	Alternate bool
}

func (c *EjectConflictError) Error() string {
	keep := "keep it in the repository by adding the tool to ejectConfigs"
	if len(c.Tools) > 0 {
		keep = fmt.Sprintf("keep it in the repository by adding %q to ejectConfigs", c.Tools[0])
	}
	name := c.RelPath
	if name == "" {
		name = c.Path
	}
	if c.Alternate {
		return fmt.Sprintf("%s is another name for %s and differs from what datamitsu renders for it; move any changes you need into %s, then delete %s",
			name, c.ConfigName, c.ConfigName, name)
	}
	return fmt.Sprintf("%s differs from what datamitsu renders for it, so deleting it could lose your changes; %s, or delete it yourself",
		name, keep)
}

// EjectConflicts reports every repository file this installer would delete
// for an ejectable entry although it differs from the entry's pristine render.
// It does not touch the filesystem, so reconciliation can refuse the whole run
// before its first write.
func (i *Installer) EjectConflicts() []*EjectConflictError {
	names := make([]string, 0, len(i.configs))
	for name, cfg := range i.configs {
		if cfg.Ejectable {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var conflicts []*EjectConflictError
	for _, name := range names {
		cfg := i.configs[name]
		if !i.applies(cfg) || !i.isToolSelected(cfg) || (cfg.Scope == config.ScopeGitRoot && i.cwdPath != i.rootPath) {
			continue
		}
		conflicts = append(conflicts, i.ejectConflicts(name, cfg)...)
	}
	return conflicts
}

// applies reports whether reconciliation handles an entry at this location.
// An internal entry's repository copy is removed whatever the types: init
// writes its .datamitsu/configs/ copy regardless, and a stale file left beside
// it would be one the preflight check reports and nothing else ever removes.
func (i *Installer) applies(cfg config.ManagedConfig) bool {
	if i.isApplicable(cfg) || cfg.Placement == config.PlacementInternal {
		return true
	}
	if !cfg.Ejectable || cfg.Scope != config.ScopeGitRoot {
		return false
	}
	for _, t := range cfg.ProjectTypes {
		if slices.Contains(i.repoProjectTypes, t) {
			return true
		}
	}
	return false
}

// ejectConflicts checks the files an ejectable entry removes: the canonical
// file when the entry is internal, and its alternate names in either placement.
func (i *Installer) ejectConflicts(name string, cfg config.ManagedConfig) []*EjectConflictError {
	pristine := i.pristineContent(name)
	var conflicts []*EjectConflictError
	for _, target := range i.ejectRemovals(name, cfg) {
		if !i.fileExists(target.path) {
			continue
		}
		data, err := os.ReadFile(target.path)
		if err == nil && i.holdsOnlyWhatIsRendered(name, data, pristine, target.alternate) {
			continue
		}
		rel, relErr := filepath.Rel(i.rootPath, target.path)
		if relErr != nil {
			rel = target.path
		}
		conflicts = append(conflicts, &EjectConflictError{
			ConfigName: name,
			Path:       target.path,
			RelPath:    filepath.ToSlash(rel),
			Tools:      cfg.Tools,
			Alternate:  target.alternate,
		})
	}
	return conflicts
}

type ejectRemoval struct {
	path      string
	alternate bool
}

func (i *Installer) ejectRemovals(name string, cfg config.ManagedConfig) []ejectRemoval {
	mainPath := filepath.Join(i.cwdPath, name)
	var removals []ejectRemoval
	if cfg.Placement == config.PlacementInternal {
		removals = append(removals, ejectRemoval{path: mainPath})
	}
	for _, alt := range cfg.OtherFileNameList {
		altPath := filepath.Join(i.cwdPath, alt)
		if altPath != mainPath {
			removals = append(removals, ejectRemoval{path: altPath, alternate: true})
		}
	}
	return removals
}

// holdsOnlyWhatIsRendered reports whether deleting a repository file loses
// nothing the configuration would not render again.
//
// Byte equality with the pristine render is rarely available: the fix that
// follows reconciliation runs the formatters over the files it just wrote, so
// .gitleaks.toml comes back with different quotes. The test that survives
// formatting is the one reconciliation itself applies: re-rendering the file
// from its own content — what reconcile would write for it — gives the same
// result as rendering from nothing. The generator's own parse and stringify
// decide what counts as content, so a key the project added makes the renders
// differ, and a changed quote style does not.
//
// Only the canonical file has that re-render (the loader reads it as
// originalContent); an alternate name must match byte for byte.
func (i *Installer) holdsOnlyWhatIsRendered(name string, data []byte, pristine *string, alternate bool) bool {
	if pristine == nil {
		return false
	}
	if string(data) == *pristine {
		return true
	}
	if alternate || i.layerMap == nil {
		return false
	}
	history := (*i.layerMap)[name]
	if history == nil || history.RenderFailed || history.OriginalContent == nil || *history.OriginalContent != string(data) {
		return false
	}
	rerendered := config.GetLastGeneratedContent(history)
	return rerendered != nil && *rerendered == *pristine
}

func (i *Installer) pristineContent(name string) *string {
	if i.layerMap == nil {
		return nil
	}
	history, ok := (*i.layerMap)[name]
	if !ok || history == nil {
		return nil
	}
	return history.PristineContent
}

// internalize removes the repository copies of an entry that lives in
// .datamitsu/configs/. Writing that copy is init's job; the caller has already
// established that every file removed here is a pristine render.
func (i *Installer) internalize(name string, cfg config.ManagedConfig, dryRun bool, result InstallResult) InstallResult {
	result.Action = "internal"
	result.FilePath = filepath.Join(i.rootPath, filepath.FromSlash(config.InternalConfigRelPath(name)))
	for _, target := range i.ejectRemovals(name, cfg) {
		if !i.fileExists(target.path) {
			continue
		}
		if !dryRun {
			if err := os.Remove(target.path); err != nil {
				result.Error = fmt.Errorf("failed to remove %s: %w", target.path, err)
				return result
			}
		}
		result.DeletedFiles = append(result.DeletedFiles, target.path)
	}
	return result
}
