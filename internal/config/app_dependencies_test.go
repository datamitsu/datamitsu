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
		{shell: "", empty: "", name: "missing", edges: map[string][]string{"a": {"missing"}}, wantErr: `app "a": dependency "missing" not found in apps`},
		{shell: "", empty: "", name: "self", edges: map[string][]string{"a": {"a"}}, wantErr: `app "a": cannot depend on itself`},
		{shell: "", empty: "", name: "duplicate", edges: map[string][]string{"a": {"b", "b"}, "b": nil}, wantErr: `app "a": duplicate dependency "b"`},
		{shell: "", empty: "", name: "two node cycle", edges: map[string][]string{"b": {"a"}, "a": {"b"}}, wantErr: "a -> b -> a"},
		{shell: "", empty: "", name: "three node cycle", edges: map[string][]string{"c": {"a"}, "a": {"b"}, "b": {"c"}}, wantErr: "a -> b -> c -> a"},
		{shell: "", empty: "", wantErr: "", name: "diamond", edges: map[string][]string{"a": {"c", "b"}, "b": {"d"}, "c": {"d"}, "d": nil}},
		{empty: "", wantErr: "", name: "shell dependent", edges: map[string][]string{"a": {"b"}, "b": nil}, shell: "a"},
		{empty: "", name: "shell dependency", edges: map[string][]string{"a": {"b"}, "b": nil}, shell: "b", wantErr: `dependency "b" is a shell app and cannot be provisioned`},
		{shell: "", name: "empty dependency", edges: map[string][]string{"a": {"b"}, "b": nil}, empty: "b", wantErr: `dependency "b" has no installable configuration`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			apps := binmanager.MapOfApps{}
			for name, deps := range tt.edges {
				app := binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}, DependsOn: edges(deps...)}
				if name == tt.shell {
					app.Binary = nil
					app.Shell = &binmanager.AppConfigShell{Args: nil, Name: "echo"}
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

// edges builds a dependsOn edge list from plain names, mirroring the
// normalized string form of the config.
func edges(names ...string) []binmanager.AppDependency {
	out := make([]binmanager.AppDependency, 0, len(names))
	for _, n := range names {
		out = append(out, binmanager.AppDependency{App: n, Alias: n})
	}
	return out
}
