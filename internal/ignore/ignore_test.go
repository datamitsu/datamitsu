package ignore

import (
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"testing"
)

func TestBuildGroups(t *testing.T) {
	specificGroups := []IgnoreGroup{
		{
			Name:     "test-group",
			Elements: []string{"*.test"},
		},
	}

	result := buildGroups(specificGroups)

	if len(result) != 4 {
		t.Errorf("buildGroups() returned %d groups, want 4", len(result))
	}

	if result[0].Name != ldflags.PackageName+" >>>" {
		t.Errorf("first group name = %q, want %q", result[0].Name, ldflags.PackageName+" >>>")
	}

	if result[len(result)-1].Name != ldflags.PackageName+" <<<" {
		t.Errorf("last group name = %q, want %q", result[len(result)-1].Name, ldflags.PackageName+" <<<")
	}

	if result[2].Name != "test-group" {
		t.Errorf("specific group not found in correct position")
	}
}

func TestGetDockerignoreGroups(t *testing.T) {
	groups := GetDockerignoreGroups()

	if len(groups) == 0 {
		t.Error("GetDockerignoreGroups() returned empty groups")
	}

	if groups[0].Name != ldflags.PackageName+" >>>" {
		t.Errorf("first group name = %q, want %q", groups[0].Name, ldflags.PackageName+" >>>")
	}

	if groups[len(groups)-1].Name != ldflags.PackageName+" <<<" {
		t.Errorf("last group name = %q, want %q", groups[len(groups)-1].Name, ldflags.PackageName+" <<<")
	}

	foundSourceGroup := false
	for _, group := range groups {
		if group.Name == "source" {
			foundSourceGroup = true
			if len(group.Elements) == 0 {
				t.Error("source group has no elements")
			}
		}
	}

	if !foundSourceGroup {
		t.Error("source group not found in dockerignore groups")
	}
}

func TestGetGitignoreGroups(t *testing.T) {
	groups := GetGitignoreGroups()

	if len(groups) == 0 {
		t.Error("GetGitignoreGroups() returned empty groups")
	}

	if groups[0].Name != ldflags.PackageName+" >>>" {
		t.Errorf("first group name = %q, want %q", groups[0].Name, ldflags.PackageName+" >>>")
	}

	if groups[len(groups)-1].Name != ldflags.PackageName+" <<<" {
		t.Errorf("last group name = %q, want %q", groups[len(groups)-1].Name, ldflags.PackageName+" <<<")
	}

	foundLocalEnvGroup := false
	for _, group := range groups {
		if group.Name == "local env files" {
			foundLocalEnvGroup = true
			if len(group.Elements) == 0 {
				t.Error("local env files group has no elements")
			}
		}
	}

	if !foundLocalEnvGroup {
		t.Error("local env files group not found in gitignore groups")
	}
}

func TestGetPatternsByType(t *testing.T) {
	t.Run("git patterns", func(t *testing.T) {
		patterns := GetPatternsByType(IgnoreTypeGit)

		if len(patterns) == 0 {
			t.Error("GetPatternsByType(git) returned no patterns")
		}
	})

	t.Run("docker patterns", func(t *testing.T) {
		patterns := GetPatternsByType(IgnoreTypeDocker)

		if len(patterns) == 0 {
			t.Error("GetPatternsByType(docker) returned no patterns")
		}
	})

	t.Run("unknown type", func(t *testing.T) {
		patterns := GetPatternsByType("unknown")

		if len(patterns) != 0 {
			t.Errorf("GetPatternsByType(unknown) returned %d patterns, want 0", len(patterns))
		}
	})
}

func TestIgnoreGroupMap(t *testing.T) {
	if len(ignoreGroupMap) == 0 {
		t.Error("ignoreGroupMap is empty")
	}

	for groupName, item := range ignoreGroupMap {
		if len(item.Pattern) == 0 {
			t.Errorf("group %q has no patterns", groupName)
		}

		if !item.Git && !item.Docker {
			t.Errorf("group %q has neither Git nor Docker flag set", groupName)
		}
	}
}

func TestDebugCheck(t *testing.T) {
	err := debugCheck()
	if err != nil {
		t.Errorf("debugCheck() found duplicates: %v", err)
	}
}

func TestDebugCheckWithDuplicates(t *testing.T) {
	originalMap := ignoreGroupMap

	duplicateMap := IgnoreGroupMap{
		"group1": {
			Pattern: []string{"**/node_modules", "**/.next"},
			Git:     true,
			Docker:  true,
		},
		"group2": {
			Pattern: []string{"**/node_modules", "**/.cache"},
			Git:     true,
			Docker:  true,
		},
	}

	ignoreGroupMap = duplicateMap

	err := debugCheck()
	if err == nil {
		t.Error("debugCheck() should have detected duplicates but returned nil")
	}

	ignoreGroupMap = originalMap
}

func TestPatternDuplicate(t *testing.T) {
	dup := PatternDuplicate{
		Pattern: "**/test",
		Groups:  []string{"group1", "group2"},
	}

	if dup.Pattern != "**/test" {
		t.Errorf("Pattern = %q, want %q", dup.Pattern, "**/test")
	}

	if len(dup.Groups) != 2 {
		t.Errorf("len(Groups) = %d, want 2", len(dup.Groups))
	}
}
