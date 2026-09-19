package config

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func TestValidateRuntimeEnv(t *testing.T) {
	for _, tt := range []struct {
		name, value, want string
		mutate            func(binmanager.MapOfApps)
	}{
		{name: "valid", value: "${STORE}/${APP_DIR}:${APP_BIN:dep}"},
		{name: "multiple", value: "${APP_BIN:dep} ${APP_BIN:dep}"},
		{name: "missing edge", value: "${APP_BIN:other}", want: "direct dependsOn"},
		{name: "transitive edge", value: "${APP_BIN:leaf}", want: "direct dependsOn", mutate: func(a binmanager.MapOfApps) {
			a["dep"] = binmanager.App{Binary: &binmanager.AppConfigBinary{}, DependsOn: []string{"leaf"}}
			a["leaf"] = binmanager.App{Binary: &binmanager.AppConfigBinary{}}
		}},
		{name: "missing target", value: "${APP_BIN:dep}", want: `APP_BIN target "dep" not found`, mutate: func(a binmanager.MapOfApps) { delete(a, "dep") }},
		{name: "overlap", value: "ok", want: "key is also defined in env", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Env = map[string]string{"BINDING": "other"}
			a["root"] = app
		}},
		{name: "empty", value: "${APP_BIN:}", want: "malformed APP_BIN"},
		{name: "no colon", value: "${APP_BIN}", want: "malformed APP_BIN"},
		{name: "unterminated", value: "${APP_BIN:dep", want: "unterminated placeholder"},
		{name: "nested", value: "${OTHER${APP_BIN:dep}}", want: "malformed nested"},
		{name: "unknown", value: "${FOO:dep}", want: "unknown placeholder"},
		{name: "invalid name", value: "${APP_BIN:dep:more}", want: "malformed APP_BIN"},
		{name: "env", value: "ok", want: "apps.root.env.BAD: APP_BIN", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Env = map[string]string{"BAD": "${APP_BIN:dep}"}
			a["root"] = app
		}},
		{name: "runtimeEnv PATH", value: "ok", want: "apps.root.runtimeEnv.PATH: PATH cannot be set", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.RuntimeEnv = map[string]string{"PATH": "${STORE}/bin"}
			a["root"] = app
		}},
		{name: "env PATH in any case", value: "ok", want: "apps.root.env.Path: PATH cannot be set", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Env = map[string]string{"Path": "/usr/bin"}
			a["root"] = app
		}},
		{name: "PATH-like names stay allowed", value: "ok", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Env = map[string]string{"PLAYWRIGHT_BROWSERS_PATH": "${STORE}/browsers", "PATHS": "x"}
			a["root"] = app
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			apps := binmanager.MapOfApps{"root": {Binary: &binmanager.AppConfigBinary{}, DependsOn: []string{"dep"}, RuntimeEnv: map[string]string{"BINDING": tt.value}}, "dep": {Binary: &binmanager.AppConfigBinary{}}}
			if tt.mutate != nil {
				tt.mutate(apps)
			}
			var previous string
			for range 3 {
				_, err := ValidateApps(apps, nil)
				if tt.want == "" {
					if err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("error = %v, want %q", err, tt.want)
				}
				if previous != "" && previous != err.Error() {
					t.Fatal("nondeterministic errors")
				}
				previous = err.Error()
			}
		})
	}
}

func TestRuntimeEnvBinaryTargetsOnly(t *testing.T) {
	for name, app := range map[string]binmanager.App{
		"shell": {Shell: &binmanager.AppConfigShell{Name: "echo"}},
		"bun":   {Bun: &binmanager.AppConfigBun{}}, "node": {Node: &binmanager.AppConfigNode{}},
		"uv": {Uv: &binmanager.AppConfigUV{}}, "go": {Go: &binmanager.AppConfigGo{}}, "jvm": {Jvm: &binmanager.AppConfigJVM{}},
	} {
		t.Run(name, func(t *testing.T) {
			apps := binmanager.MapOfApps{"root": {Binary: &binmanager.AppConfigBinary{}, DependsOn: []string{"dep"}, RuntimeEnv: map[string]string{"BIN": "${APP_BIN:dep}"}}, "dep": app}
			_, err := ValidateApps(apps, nil)
			if err == nil || !strings.Contains(err.Error(), "only binary targets are supported") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
