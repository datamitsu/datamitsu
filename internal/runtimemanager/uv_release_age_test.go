package runtimemanager

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

func TestUVSpan(t *testing.T) {
	tests := []struct {
		minutes int
		want    string
	}{
		{10080, "P7D"},
		{1440, "P1D"},
		{90, "PT90M"},
		{1500, "PT1500M"},
		{0, ""},
		{-5, ""},
	}
	for _, tt := range tests {
		if got := uvSpan(tt.minutes); got != tt.want {
			t.Errorf("uvSpan(%d) = %q, want %q", tt.minutes, got, tt.want)
		}
	}
}

func TestUVDefaultExcludeNewerIgnoresEnvironment(t *testing.T) {
	t.Setenv("DATAMITSU_MIN_RELEASE_AGE", "20160")
	want := uvSpan(runtimeconfig.MinimumReleaseAgeMinutes)
	if got := uvDefaultExcludeNewer(); got != want {
		t.Errorf("uvDefaultExcludeNewer() = %q, want %q from the compile-time constant", got, want)
	}
	if want != "P7D" {
		t.Errorf("default window = %q, want P7D", want)
	}
}

func TestUVLockWindow(t *testing.T) {
	tests := []struct {
		name         string
		lock         string
		wantExclude  string
		wantPackages []string
		wantErr      string
	}{
		{
			name: "span",
			lock: `version = 1
revision = 3
requires-python = ">=3.12"

[options]
exclude-newer = "0001-01-01T00:00:00Z" # This has no effect and is included for backwards compatibility when using relative exclude-newer values.
exclude-newer-span = "P7D"

[[package]]
name = "certifi"
version = "2026.7.22"
`,
			wantExclude: "P7D",
		},
		{
			name: "span in minutes keeps its unit",
			lock: `version = 1

[options]
exclude-newer = "0001-01-01T00:00:00Z"
exclude-newer-span = "PT10080M"
`,
			wantExclude: "PT10080M",
		},
		{
			name: "absolute timestamp",
			lock: `version = 1

[options]
exclude-newer = "2026-09-01T00:00:00Z"

[[package]]
name = "idna"
version = "3.10"
`,
			wantExclude: "2026-09-01T00:00:00Z",
		},
		{
			name: "no options",
			lock: `version = 1
requires-python = ">=3.12"

[[package]]
name = "idna"
version = "3.10"
`,
		},
		{
			name: "package cutoffs in every recorded shape",
			lock: `version = 1

[options]
exclude-newer = "0001-01-01T00:00:00Z"
exclude-newer-span = "P7D"

[options.exclude-newer-package]
urllib3 = false
certifi = { timestamp = "0001-01-01T00:00:00Z", span = "P1D" }
idna = "2026-09-24T00:00:00Z"
charset-normalizer = { timestamp = "2026-09-20T00:00:00Z" }
`,
			wantExclude:  "P7D",
			wantPackages: []string{"certifi=P1D", "charset-normalizer=2026-09-20T00:00:00Z", "idna=2026-09-24T00:00:00Z", "urllib3=false"},
		},
		{
			name:    "malformed",
			lock:    "version = 1\n[options\nexclude-newer = \n",
			wantErr: "failed to read [options] from uv.lock",
		},
		{
			name: "unsupported package value",
			lock: `version = 1

[options.exclude-newer-package]
certifi = true
`,
			wantErr: `exclude-newer-package "certifi"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := uvLockWindow(tt.lock)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("uvLockWindow() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("uvLockWindow() error = %v", err)
			}
			if w.excludeNewer != tt.wantExclude {
				t.Errorf("excludeNewer = %q, want %q", w.excludeNewer, tt.wantExclude)
			}
			if !slices.Equal(w.packages, tt.wantPackages) {
				t.Errorf("packages = %v, want %v", w.packages, tt.wantPackages)
			}
		})
	}
}

func TestUVResolveWindow(t *testing.T) {
	writeConfig := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "uv.toml")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("no app config uses the default", func(t *testing.T) {
		w, err := uvResolveWindow("")
		if err != nil {
			t.Fatal(err)
		}
		if w.excludeNewer != "P7D" || len(w.packages) != 0 {
			t.Errorf("window = %+v, want the default P7D only", w)
		}
	})

	t.Run("app config without a window keeps the default", func(t *testing.T) {
		w, err := uvResolveWindow(writeConfig(t, "[exclude-newer-package]\ncertifi = \"P1D\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if w.excludeNewer != "P7D" || len(w.packages) != 0 {
			t.Errorf("window = %+v, want P7D with the package cutoffs left to uv.toml", w)
		}
	})

	t.Run("app config window wins", func(t *testing.T) {
		w, err := uvResolveWindow(writeConfig(t, "exclude-newer = \"30 days\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if len(w.args()) != 0 {
			t.Errorf("args = %v, want none so uv reads the window from uv.toml", w.args())
		}
	})

	t.Run("malformed app config", func(t *testing.T) {
		if _, err := uvResolveWindow(writeConfig(t, "exclude-newer = \n")); err == nil {
			t.Error("expected an error for a malformed uv.toml")
		}
	})
}

func TestUVAppConfigPath(t *testing.T) {
	dir := t.TempDir()
	path, err := uvAppConfigPath(dir)
	if err != nil || path != "" {
		t.Fatalf("uvAppConfigPath() = %q, %v; want no config", path, err)
	}
	want := filepath.Join(dir, "uv.toml")
	if err := os.WriteFile(want, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	path, err = uvAppConfigPath(dir)
	if err != nil || path != want {
		t.Fatalf("uvAppConfigPath() = %q, %v; want %q", path, err, want)
	}
}

func TestUVInheritedOverrideStripsOnlyOwnedVariables(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"UV_EXCLUDE_NEWER=8 days",
		"UV_EXCLUDE_NEWER_PACKAGE=certifi=P1D",
		"UV_CONFIG_FILE=/home/user/uv.toml",
		"uv_exclude_newer=8 days",
		"UV_INDEX_URL=https://mirror.example/simple",
		"UV_EXCLUDE_NEWER_X=kept",
	}
	got := withoutEnv(base, uvInheritedOverride)
	want := []string{"PATH=/usr/bin", "UV_INDEX_URL=https://mirror.example/simple", "UV_EXCLUDE_NEWER_X=kept"}
	if !slices.Equal(got, want) {
		t.Errorf("withoutEnv() = %v, want %v", got, want)
	}
}

func TestUVReleaseAgeHint(t *testing.T) {
	refusal := "hint: `certifi` was filtered by `exclude-newer` to only include packages uploaded before 2026-09-18T00:00:00Z."

	hint := uvReleaseAgeHint(refusal, uvWindow{excludeNewer: uvDefaultExcludeNewer()})
	for _, want := range []string{"10080 minutes", "runtimeconfig.MinimumReleaseAgeMinutes", "--exclude-newer P7D", "exclude-newer-package"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint %q does not mention %q", hint, want)
		}
	}

	if got := uvReleaseAgeHint("error: network unreachable", uvWindow{excludeNewer: "P7D"}); got != "" {
		t.Errorf("unrelated failure got a hint: %q", got)
	}
	if got := uvReleaseAgeHint(refusal, uvWindow{}); got != "" {
		t.Errorf("a window from the app's uv.toml got the default's hint: %q", got)
	}
}
