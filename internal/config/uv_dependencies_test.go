package config

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func TestValidateUVDependencies(t *testing.T) {
	base := binmanager.AppConfigUV{PackageName: "check-jsonschema", Version: "0.38.2", LockFile: "br:lock"}
	for _, tc := range []struct {
		name string
		deps map[string]string
		want string
	}{
		{name: "pinned dependencies", deps: map[string]string{"json5": "0.17.3", "ruamel-yaml": "0.19.1"}},
		{name: "invalid package name", deps: map[string]string{"json5;bad": "0.17.3"}, want: "package name"},
		{name: "floating version", deps: map[string]string{"json5": "latest"}, want: "must be pinned"},
		{name: "invalid version", deps: map[string]string{"json5": "0.17.3;bad"}, want: "invalid characters"},
		{name: "duplicate normalized names", deps: map[string]string{"foo_bar": "1.0.0", "foo-bar": "1.0.1"}, want: "normalize to the same name"},
		{name: "duplicate app package", deps: map[string]string{"check_jsonschema": "0.38.2"}, want: "duplicates the app package"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := base
			app.Dependencies = tc.deps
			_, err := ValidateApps(binmanager.MapOfApps{"frontend": {Uv: &app}}, nil)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("ValidateApps: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateApps error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
