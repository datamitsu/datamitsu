package runtimemanager

import (
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/pelletier/go-toml/v2"
)

func TestUVManifestIncludesPinnedAdditionalRequirements(t *testing.T) {
	manifest := buildPyprojectTOML("check-jsonschema", "0.38.2", ">=3.14", map[string]string{
		"ruamel-yaml": "0.19.1",
		"json5":       "0.17.3",
	})
	var project struct {
		Project struct {
			Dependencies   []string `toml:"dependencies"`
			RequiresPython string   `toml:"requires-python"`
		} `toml:"project"`
	}
	if err := toml.Unmarshal([]byte(manifest), &project); err != nil {
		t.Fatal(err)
	}
	want := []string{"check-jsonschema==0.38.2", "json5==0.17.3", "ruamel-yaml==0.19.1"}
	if !slices.Equal(project.Project.Dependencies, want) || project.Project.RequiresPython != ">=3.14" {
		t.Fatalf("generated project does not preserve the locked requirement set and Python constraint: %+v", project.Project)
	}
}

func TestUVRequirementIdentityMatchesCommandResolution(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(makeTestRuntimes())
	app := binmanager.AppConfigUV{PackageName: "check-jsonschema", Version: "0.38.2", Runtime: "uv", LockFile: "version = 1\n"}
	pathFor := func(definition binmanager.AppConfigUV) string {
		t.Helper()
		path, err := rm.ComputeAppPath("checker", binmanager.App{Uv: &definition})
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	base := pathFor(app)
	app.Dependencies = map[string]string{}
	if pathFor(app) != base {
		t.Fatal("empty requirements changed the identity of an existing UV app")
	}
	app.Dependencies = map[string]string{"json5": "0.17.3", "referencing": "0.37.0"}
	withRequirements := pathFor(app)
	if withRequirements == base {
		t.Fatal("additional requirements reused an environment without those packages")
	}
	info, err := rm.GetUVCommandInfo("checker", &app, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Command != getUVBinaryPath(withRequirements, app.PackageName) {
		t.Fatalf("command resolution and install identity disagree: %q versus %q", info.Command, withRequirements)
	}
	app.Dependencies = map[string]string{"referencing": "0.37.0", "json5": "0.17.3"}
	if pathFor(app) != withRequirements {
		t.Fatal("requirement map order changed the UV environment identity")
	}
	app.Dependencies["json5"] = "0.17.4"
	if pathFor(app) == withRequirements {
		t.Fatal("changed requirement pin reused the old UV environment")
	}
}
