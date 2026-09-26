package runtimemanager

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPNPMEnvSettingName(t *testing.T) {
	tests := map[string]string{
		"minimumReleaseAge":         "minimum_release_age",
		"dangerouslyAllowAllBuilds": "dangerously_allow_all_builds",
		"storeDir":                  "store_dir",
		"lockfile":                  "lockfile",
		"allow-builds":              "allow_builds",
	}
	for key, want := range tests {
		if got := pnpmEnvSettingName(key); got != want {
			t.Errorf("pnpmEnvSettingName(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestPNPMWorkspaceEnvOverride(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	merged, err := buildPNPMWorkspaceForApp(map[string]string{
		"pnpm-workspace.yaml": "allowBuilds:\n  esbuild: true\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	overrides, err := pnpmWorkspaceEnvOverride(merged)
	if err != nil {
		t.Fatal(err)
	}

	base := []string{
		"PATH=/usr/bin",
		"pnpm_config_minimum_release_age=0",
		"PNPM_CONFIG_MINIMUM_RELEASE_AGE=0",
		`pnpm_config_minimum_release_age_exclude=["@types/node"]`,
		"pnpm_config_minimum_release_age_strict=false",
		"pnpm_config_dangerously_allow_all_builds=true",
		"pnpm_config_strict_dep_builds=false",
		"pnpm_config_store_dir=/tmp/elsewhere",
		"pnpm_config_allow_builds={}",
		"pnpm_config_registry=https://registry.example/",
		"npm_config_minimum_release_age=0",
		"minimumReleaseAge=0",
	}
	got := withoutEnv(base, overrides)
	want := []string{
		"PATH=/usr/bin",
		"pnpm_config_registry=https://registry.example/",
		"npm_config_minimum_release_age=0",
		"minimumReleaseAge=0",
	}
	if !slices.Equal(got, want) {
		t.Errorf("withoutEnv() = %v, want %v", got, want)
	}
}

func TestPNPMWorkspaceEnvOverrideRejectsInvalidYAML(t *testing.T) {
	if _, err := pnpmWorkspaceEnvOverride("a: [\n"); err == nil {
		t.Error("expected an error for invalid YAML")
	}
}

func TestCheckGoModuleAge(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	const week = 10080
	list := `{
	"Path": "datamitsu-shfmt",
	"Main": true,
	"GoVersion": "1.27.0"
}
{
	"Path": "mvdan.cc/sh/v3",
	"Version": "v3.12.0",
	"Time": "2025-07-06T17:25:03Z"
}
{
	"Path": "golang.org/x/sys",
	"Version": "v0.40.0",
	"Time": "2026-09-24T09:00:00Z"
}
{
	"Path": "example.com/timeless",
	"Version": "v1.0.0"
}
`

	err := checkGoModuleAge("shfmt", strings.NewReader(list), now, week)
	if err == nil {
		t.Fatal("expected modules younger than the window to fail")
	}
	msg := err.Error()
	for _, want := range []string{
		"10080 minutes",
		"golang.org/x/sys v0.40.0 (2026-09-24T09:00:00Z)",
		"example.com/timeless v1.0.0 (no release time)",
		"DATAMITSU_MIN_RELEASE_AGE=0",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
	for _, unwanted := range []string{"mvdan.cc/sh/v3", "datamitsu-shfmt"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("error %q names %q, which is old enough or the main module", msg, unwanted)
		}
	}

	oldOnly := `{"Path": "datamitsu-shfmt", "Main": true}
{"Path": "mvdan.cc/sh/v3", "Version": "v3.12.0", "Time": "2025-07-06T17:25:03Z"}`
	if err := checkGoModuleAge("shfmt", strings.NewReader(oldOnly), now, week); err != nil {
		t.Errorf("modules older than the window failed: %v", err)
	}

	if err := checkGoModuleAge("shfmt", strings.NewReader("{not json"), now, week); err == nil || !strings.Contains(err.Error(), "failed to parse") {
		t.Errorf("malformed output error = %v, want a parse error", err)
	}
}
