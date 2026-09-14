package config

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func TestValidateAppDependencies(t *testing.T) {
	for _, tt := range []struct {
		name    string
		edges   map[string][]string
		shell   string
		empty   string
		wantErr string
	}{
		{name: "missing", edges: map[string][]string{"a": {"missing"}}, wantErr: `app "a": dependency "missing" not found in apps`},
		{name: "self", edges: map[string][]string{"a": {"a"}}, wantErr: `app "a": cannot depend on itself`},
		{name: "duplicate", edges: map[string][]string{"a": {"b", "b"}, "b": nil}, wantErr: `app "a": duplicate dependency "b"`},
		{name: "two node cycle", edges: map[string][]string{"b": {"a"}, "a": {"b"}}, wantErr: "a -> b -> a"},
		{name: "three node cycle", edges: map[string][]string{"c": {"a"}, "a": {"b"}, "b": {"c"}}, wantErr: "a -> b -> c -> a"},
		{name: "diamond", edges: map[string][]string{"a": {"c", "b"}, "b": {"d"}, "c": {"d"}, "d": nil}},
		{name: "shell dependent", edges: map[string][]string{"a": {"b"}, "b": nil}, shell: "a"},
		{name: "shell dependency", edges: map[string][]string{"a": {"b"}, "b": nil}, shell: "b", wantErr: `dependency "b" is a shell app and cannot be provisioned`},
		{name: "empty dependency", edges: map[string][]string{"a": {"b"}, "b": nil}, empty: "b", wantErr: `dependency "b" has no installable configuration`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			apps := binmanager.MapOfApps{}
			for name, deps := range tt.edges {
				app := binmanager.App{Binary: &binmanager.AppConfigBinary{}, DependsOn: deps}
				if name == tt.shell {
					app.Binary = nil
					app.Shell = &binmanager.AppConfigShell{Name: "echo"}
				}
				if name == tt.empty {
					app.Binary = nil
				}
				apps[name] = app
			}
			var previous string
			for range 10 {
				_, err := ValidateApps(apps, nil)
				if tt.wantErr == "" {
					if err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				if previous != "" && previous != err.Error() {
					t.Fatal("nondeterministic validation error")
				}
				previous = err.Error()
			}
		})
	}
}
