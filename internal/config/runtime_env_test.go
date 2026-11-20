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
		extra             string
	}{
		{name: "valid", value: "${STORE}/${APP_DIR}:${APP_BIN:dep}"},
		{name: "valid named cache token", value: "{toolCache:providers}/data", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Caches = []string{"providers"}
			a["root"] = app
		}},
		{name: "unknown named cache", value: "{toolCache:unknown}", want: `unknown tool cache "unknown"`},
		{name: "malformed named cache", value: "{toolCache:}", want: "malformed tool cache placeholder"},
		{name: "valid default cache", value: "{toolCache}/data"},
		{name: "valid adjacent path and cache", value: "${STORE}{toolCache}/data"},
		{name: "dollar-prefixed cache", value: "${toolCache}/data", want: "malformed nested"},
		{name: "cache inside unknown placeholder", value: "${prefix{toolCache}}", want: "malformed nested"},
		{name: "unterminated nested cache", value: "${prefix{toolCache", want: "malformed nested"},
		{name: "bare lookalike", value: "{toolCacheEnabled}"},
		{name: "dollar lookalike", value: "${toolCacheEnabled}"},
		{name: "nested lookalike without cache", value: "${prefix{toolCacheEnabled}}"},
		{name: "escaped literal brace", value: `${value#\{}`},
		{name: "escaped brace before real cache", value: `${value#\{}{toolCache}`},
		{name: "escaped cache-like text before real cache", value: `${value#\{toolCache}{toolCache}`},
		{name: "quoted opening brace before cache", value: `${value#'{'}{toolCache}`},
		{name: "quoted named cache is literal", value: `${value#'{toolCache:providers}'}{toolCache}`},
		{name: "quoted dollar opener is literal", value: `${value#'${'}{toolCache}`},
		{name: "quoted closing brace before nested cache", value: `${prefix"}"{toolCache}}`, want: "malformed nested"},
		{name: "independent binding and cache errors", value: "${APP_BIN:missing}|{toolCache:unknown}", want: "unknown tool cache", extra: `APP_BIN target "missing"`},
		{name: "real cache after nested lookalike", value: "${prefix{toolCacheEnabled}{toolCache:providers}}", want: "malformed nested", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Caches = []string{"providers"}
			a["root"] = app
		}},
		{name: "oversized cache", value: "ok", want: "cache name exceeds 255 bytes", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Caches = []string{strings.Repeat("a", 256)}
			a["root"] = app
		}},
		{name: "invalid cache declaration", value: "ok", want: "invalid cache name", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Caches = []string{"Providers"}
			a["root"] = app
		}},
		{name: "reserved cache declaration", value: "ok", want: "reserved", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Caches = []string{"default"}
			a["root"] = app
		}},
		{name: "duplicate cache declaration", value: "ok", want: "duplicate cache name", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Caches = []string{"providers", "providers"}
			a["root"] = app
		}},
		{name: "shell cache declaration", value: "ok", want: "cache slots require a managed app", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Binary = nil
			app.Shell = &binmanager.AppConfigShell{Name: "echo"}
			app.Caches = []string{"providers"}
			a["root"] = app
		}},
		{name: "shell runtime tool cache token", value: "{toolCache}/data", want: "allowed only for managed apps", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Binary = nil
			app.Shell = &binmanager.AppConfigShell{Name: "echo"}
			a["root"] = app
		}},
		{name: "env tool cache token", value: "ok", want: "only in runtimeEnv", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Env = map[string]string{"CACHE": "{toolCache}/data"}
			a["root"] = app
		}},
		{name: "unmanaged runtime tool cache token", value: "{toolCache}/data", want: "allowed only for managed apps", mutate: func(a binmanager.MapOfApps) {
			app := a["root"]
			app.Binary = nil
			a["root"] = app
		}},
		{name: "multiple", value: "${APP_BIN:dep} ${APP_BIN:dep}"},
		{name: "missing edge", value: "${APP_BIN:other}", want: "direct dependsOn"},
		{name: "transitive edge", value: "${APP_BIN:leaf}", want: "direct dependsOn", mutate: func(a binmanager.MapOfApps) {
			a["dep"] = binmanager.App{Binary: &binmanager.AppConfigBinary{}, DependsOn: edges("leaf")}
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
			apps := binmanager.MapOfApps{"root": {Binary: &binmanager.AppConfigBinary{}, DependsOn: edges("dep"), RuntimeEnv: map[string]string{"BINDING": tt.value}}, "dep": {Binary: &binmanager.AppConfigBinary{}}}
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
				if strings.Count(err.Error(), tt.want) != 1 || !strings.Contains(err.Error(), tt.extra) {
					t.Fatalf("unexpected diagnostics: %v", err)
				}
				if previous != "" && previous != err.Error() {
					t.Fatal("nondeterministic errors")
				}
				previous = err.Error()
			}
		})
	}
}

func TestRuntimeEnvBindingTargetKinds(t *testing.T) {
	// uv and go apps have a single executable entry point, so bindings can
	// hand out their paths today. Node, bun, jvm and shell need a launcher
	// that does not exist yet.
	supported := map[string]binmanager.App{
		"uv": {Uv: &binmanager.AppConfigUV{PackageName: "dep", Version: "1", LockFile: "br:x"}},
		"go": {Go: &binmanager.AppConfigGo{PackageName: "example.com/dep", Version: "v1.0.0", LockFile: "br:x"}},
	}
	for name, app := range supported {
		t.Run(name, func(t *testing.T) {
			apps := binmanager.MapOfApps{"root": {Binary: &binmanager.AppConfigBinary{}, DependsOn: edges("dep"), RuntimeEnv: map[string]string{"BIN": "${APP_BIN:dep}"}}, "dep": app}
			if _, err := ValidateApps(apps, nil); err != nil {
				t.Fatalf("supported target rejected: %v", err)
			}
		})
	}
	for name, app := range map[string]binmanager.App{
		"shell": {Shell: &binmanager.AppConfigShell{Name: "echo"}},
		"bun":   {Bun: &binmanager.AppConfigBun{}}, "node": {Node: &binmanager.AppConfigNode{}},
		"jvm": {Jvm: &binmanager.AppConfigJVM{}},
	} {
		t.Run(name, func(t *testing.T) {
			apps := binmanager.MapOfApps{"root": {Binary: &binmanager.AppConfigBinary{}, DependsOn: edges("dep"), RuntimeEnv: map[string]string{"BIN": "${APP_BIN:dep}"}}, "dep": app}
			_, err := ValidateApps(apps, nil)
			if err == nil || !strings.Contains(err.Error(), "need a launcher (not supported yet)") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
