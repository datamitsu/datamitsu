package project

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"fmt"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
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
func (d *Detector) DetectAll(ctx context.Context) ([]string, error) {
	var detected []string

	for name, ptype := range d.types {
		if d.matchesType(ctx, ptype) {
			detected = append(detected, name)
		}
	}

	return detected, nil
}

// matchesType checks if any marker file exists for the given project type
func (d *Detector) matchesType(ctx context.Context, ptype config.ProjectType) bool {
	// Get all files respecting .gitignore
	files, err := traverser.FindFiles(ctx, d.rootPath)
	if err != nil {
		return false
	}

	// Check each file against marker patterns
	for _, file := range files {
		relPath, err := filepath.Rel(d.rootPath, file)
		if err != nil {
			continue
		}

		for _, marker := range ptype.Markers {
			matched, err := doublestar.Match(marker, relPath)
			if err != nil {
				continue
			}
			if matched {
				return true
			}
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
type ProjectLocation struct {
	Type string // Project type name (e.g., "npm-package", "golang-package")
	Path string // Absolute path to the project directory
}

// DetectAllWithLocations detects all matching project types and returns their locations
// For each marker file found, it returns the directory containing that marker
// Respects .gitignore rules - directories and files matching .gitignore patterns are excluded
func (d *Detector) DetectAllWithLocations(ctx context.Context) ([]ProjectLocation, error) {
	var locations []ProjectLocation
	seen := make(map[string]bool) // To avoid duplicates

	// Get all files respecting .gitignore
	files, err := traverser.FindFiles(ctx, d.rootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to traverse files: %w", err)
	}

	// Build a map of project types with their compiled patterns
	type typeMarkers struct {
		name    string
		markers []string
	}
	var typesList []typeMarkers
	for typeName, ptype := range d.types {
		typesList = append(typesList, typeMarkers{
			name:    typeName,
			markers: ptype.Markers,
		})
	}

	// Check each file against all patterns
	for _, file := range files {
		// Get relative path from rootPath
		relPath, err := filepath.Rel(d.rootPath, file)
		if err != nil {
			continue
		}

		// Check against all project type markers
		for _, tm := range typesList {
			for _, marker := range tm.markers {
				matched, err := doublestar.Match(marker, relPath)
				if err != nil || !matched {
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
	}

	return locations, nil
}
