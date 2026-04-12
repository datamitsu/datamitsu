package ignore

import (
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"errors"
	"fmt"
	"strings"
)

const (
	IgnoreTypeGit    = "git"
	IgnoreTypeDocker = "docker"
)

type IgnoreGroup struct {
	Name     string   `json:"name"`
	Elements []string `json:"elements"`
}

type IgnoreGroupItem struct {
	Pattern []string
	Git     bool
	Docker  bool
}

type IgnoreGroupMap map[string]IgnoreGroupItem

// PatternDuplicate contains information about a duplicate pattern
type PatternDuplicate struct {
	Pattern string
	Groups  []string
}

func buildGroups(specificGroups []IgnoreGroup) []IgnoreGroup {
	groups := []IgnoreGroup{
		{
			Name:     ldflags.PackageName + " >>>",
			Elements: []string{},
		},
	}

	groups = append(groups, IgnoreGroup{
		Name:     ldflags.PackageName + " common <<<",
		Elements: []string{},
	})

	groups = append(groups, specificGroups...)

	groups = append(groups, IgnoreGroup{
		Name:     ldflags.PackageName + " <<<",
		Elements: []string{},
	})

	return groups
}

// GetDockerignoreGroups returns ignore groups for .dockerignore
func GetDockerignoreGroups() []IgnoreGroup {
	specificGroups := []IgnoreGroup{
		{
			Name: "source",
			Elements: []string{
				".dockerignore",
				".gitignore",
				".git",
				"docker-compose*.yml",
				"compose*.yml",
				"Dockerfile*",
			},
		},
	}
	return buildGroups(specificGroups)
}

// GetGitignoreGroups returns ignore groups for .gitignore
func GetGitignoreGroups() []IgnoreGroup {
	specificGroups := []IgnoreGroup{
		{
			Name: "local env files",
			Elements: []string{
				".env.local",
			},
		},
	}
	return buildGroups(specificGroups)
}

var ignoreGroupMap = IgnoreGroupMap{
	"node": {
		Pattern: []string{
			"**/node_modules",
		},
		Git:    true,
		Docker: true,
	},
	"nextjs": {
		Pattern: []string{
			"**/.next/",
		},
		Git:    true,
		Docker: true,
	},
	"testing": {
		Pattern: []string{
			"**/coverage",
			"**/*.cov",
			"**/.cover",
			"**/storybook-static",
		},
		Git:    true,
		Docker: true,
	},
	"misc": {
		Pattern: []string{
			"**/.DS_Store",
			"**/*.pem",
			"**/.idea",
			"**/.fleet",
			"**/*.sublime-workspace",
			"**/*.swp",
			"**/*.iml",
			"**/*.tmp",
		},
		Git:    true,
		Docker: true,
	},
	"turborepo": {
		Pattern: []string{
			"**/.turbo",
			"**/out",
		},
		Git:    true,
		Docker: true,
	},
	"build": {
		Pattern: []string{
			"**/build",
			"**/dist",
			"**/tsconfig.tsbuildinfo",
			"**/.eslintcache",
			"**/.prettiercache",
			"**/.stylelintcache",
		},
		Git:    true,
		Docker: true,
	},
	"test": {
		Pattern: []string{
			"**/playwright-report",
			"**/playwright-results",
			"**/playwright/.cache",
			"**/playwright-report*",
			"**/test-results",
			"**/allure-report",
		},
		Git:    true,
		Docker: true,
	},
	"pnpm": {
		Pattern: []string{
			"**/.pnpm-debug.log*",
			"**/npm-debug.log*",
			"**/yarn-debug.log*",
			"**/yarn-error.log*",
		},
		Git:    true,
		Docker: true,
	},
	"docker-source": {
		Pattern: []string{
			".dockerignore",
			".gitignore",
			".git",
			"docker-compose*.yml",
			"Dockerfile*",
		},
		Git:    false,
		Docker: true,
	},
	"local-env": {
		Pattern: []string{
			".env.local",
			".env.*.local",
		},
		Git:    true,
		Docker: false,
	},
}

func GetPatternsByType(ignoreType string) []string {
	var patterns []string

	for _, item := range ignoreGroupMap {
		switch ignoreType {
		case IgnoreTypeGit:
			if item.Git {
				patterns = append(patterns, item.Pattern...)
			}
		case IgnoreTypeDocker:
			if item.Docker {
				patterns = append(patterns, item.Pattern...)
			}
		}
	}

	return patterns
}

// debugCheck checks for duplicate patterns in ignoreGroupMap
// and returns error with detailed information about found duplicates
func debugCheck() error {
	patternToGroups := make(map[string][]string)

	for groupName, item := range ignoreGroupMap {
		for _, pattern := range item.Pattern {
			patternToGroups[pattern] = append(patternToGroups[pattern], groupName)
		}
	}

	var duplicates []PatternDuplicate
	for pattern, groups := range patternToGroups {
		if len(groups) > 1 {
			duplicates = append(duplicates, PatternDuplicate{
				Pattern: pattern,
				Groups:  groups,
			})
		}
	}

	if len(duplicates) > 0 {
		var errorMessages []string
		errorMessages = append(errorMessages, fmt.Sprintf("Found %d duplicate pattern(s):", len(duplicates)))

		for _, dup := range duplicates {
			errorMessages = append(errorMessages,
				fmt.Sprintf("  - Pattern '%s' appears in groups: [%s]",
					dup.Pattern,
					strings.Join(dup.Groups, ", ")))
		}

		return errors.New(strings.Join(errorMessages, "\n"))
	}

	return nil
}

// init is called automatically during package initialization
// Checks for pattern duplicates and aborts build if found
func init() {
	if err := debugCheck(); err != nil {
		panic(fmt.Sprintf("Pattern validation failed:\n%v", err))
	}
}
