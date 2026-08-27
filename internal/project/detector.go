// Package project detects project types within a repository by matching
// marker files against configured project-type patterns.
package project

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/globmatch"
	"github.com/datamitsu/datamitsu/internal/traverser"
)

// Detector handles project type detection based on marker files
type Detector struct {
	types    config.MapOfProjectTypes
	rootPath string
}

// NewDetector creates a new project type detector
func NewDetector(rootPath string, types config.MapOfProjectTypes) *Detector {
	return &Detector{
		types:    types,
		rootPath: rootPath,
	}
}

// DetectAll detects all matching project types in the repository
// Returns a slice of project type names that match based on marker files
//
// The tree is walked once and every type is matched against that one file list.
// It used to call matchesType per type, and each of those walked the whole
// repository again — one gitignore-aware traversal per configured project type,
// of which the shared config declares dozens, all producing the identical list.
func (d *Detector) DetectAll(ctx context.Context) ([]string, error) {
	files, err := traverser.FindFiles(ctx, d.rootPath)
	if err != nil {
		// Matching used to swallow a failed walk and report "no marker found",
		// so a broken traversal looked like a repository with no project types.
		// Keeping that shape here would hide it just as well; the caller decides.
		return nil, fmt.Errorf("failed to traverse files: %w", err)
	}

	var detected []string
	for name, ptype := range d.types {
		if matchesTypeInFiles(d, globmatch.New(ptype.Markers), files) {
			detected = append(detected, name)
		}
	}

	return detected, nil
}

// matchesTypeInFiles reports whether any file matches one of the prepared marker
// patterns.
func matchesTypeInFiles(d *Detector, markers globmatch.Set, files []string) bool {
	if markers.Len() == 0 {
		return false
	}
	for _, file := range files {
		relPath, ok := d.relToRoot(file)
		if !ok {
			continue
		}
		if markers.Match(relPath) {
			return true
		}
	}
	return false
}

// IsType checks if a specific project type is detected
func (d *Detector) IsType(ctx context.Context, typeName string) (bool, error) {
	ptype, exists := d.types[typeName]
	if !exists {
		return false, nil
	}
	return d.matchesType(ctx, ptype), nil
}

// ProjectLocation represents a detected project with its type and path
type ProjectLocation struct { //nolint:revive // exported: name kept explicit; project.ProjectLocation reads clearer than project.Location at its cross-package call sites
	Type string // Project type name (e.g., "npm-package", "golang-package")
	Path string // Absolute path to the project directory
}

// DetectAllWithLocations detects all matching project types and returns their locations
// For each marker file found, it returns the directory containing that marker
// Respects .gitignore rules - directories and files matching .gitignore patterns are excluded
func (d *Detector) DetectAllWithLocations(ctx context.Context) ([]ProjectLocation, error) {
	// Get all files respecting .gitignore
	files, err := traverser.FindFiles(ctx, d.rootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to traverse files: %w", err)
	}

	return d.DetectAllWithLocationsFromFiles(files), nil
}

// DetectAllWithLocationsFromFiles matches detected project locations against a
// precomputed list of absolute file paths under rootPath. It lets callers that
// already hold the file set (e.g. config loading, which traverses once and runs
// detection for several config layers) skip a repeat filesystem walk.
func (d *Detector) DetectAllWithLocationsFromFiles(files []string) []ProjectLocation {
	var locations []ProjectLocation
	seen := make(map[string]bool) // To avoid duplicates

	// Prepare each type's markers once. This loop is files x types x markers —
	// on a large monorepo, hundreds of thousands of matches — so the patterns are
	// compiled outside it and most reduce to a suffix or final-segment test
	// rather than a full glob walk (`**/package.json`, `**/*.csproj`).
	type typeMarkers struct {
		name string
		set  globmatch.Set
	}
	typesList := make([]typeMarkers, 0, len(d.types))
	for typeName, ptype := range d.types {
		typesList = append(typesList, typeMarkers{
			name: typeName,
			set:  globmatch.New(ptype.Markers),
		})
	}

	// Check each file against all patterns
	for _, file := range files {
		relPath, ok := d.relToRoot(file)
		if !ok {
			continue
		}

		// Check against all project type markers
		for _, tm := range typesList {
			if !tm.set.Match(relPath) {
				continue
			}

			// Get the directory containing the marker file
			dir := filepath.Dir(file)

			// Create unique key for this location
			key := tm.name + ":" + dir
			if seen[key] {
				continue
			}
			seen[key] = true

			locations = append(locations, ProjectLocation{
				Type: tm.name,
				Path: dir,
			})
		}
	}

	return locations
}

// relToRoot returns file's path relative to the detector's root. Files come from
// a walk rooted there, so they are absolute, cleaned and under it — a slice of
// the existing string, rather than filepath.Rel's split-and-rejoin and the fresh
// string it allocates for every file for every project type.
//
// It reports false for a path outside the root, which is the case the Rel-based
// version signalled with an error and skipped.
func (d *Detector) relToRoot(file string) (string, bool) {
	root := d.rootPath
	if root != "" && len(file) > len(root)+1 &&
		strings.HasPrefix(file, root) && file[len(root)] == filepath.Separator {
		return file[len(root)+1:], true
	}
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return "", false
	}
	return rel, true
}

// matchesType checks if any marker file exists for the given project type.
// Single-type callers (IsType) walk once here; DetectAll walks once for all
// types and matches through matchesTypeInFiles instead.
func (d *Detector) matchesType(ctx context.Context, ptype config.ProjectType) bool {
	// Get all files respecting .gitignore
	files, err := traverser.FindFiles(ctx, d.rootPath)
	if err != nil {
		return false
	}
	return matchesTypeInFiles(d, globmatch.New(ptype.Markers), files)
}
