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
		{want: "", mutate: nil, extra: "", name: "valid", value: "${STORE}/${APP_DIR}:${APP_BIN:dep}"},
		{
			want:  "",
			extra: "", name: "valid named cache token", value: "{toolCache:providers}/data", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Caches = []string{"providers"}
				a["root"] = app
			},
		},
		{mutate: nil, extra: "", name: "unknown named cache", value: "{toolCache:unknown}", want: `unknown tool cache "unknown"`},
		{mutate: nil, extra: "", name: "malformed named cache", value: "{toolCache:}", want: "malformed tool cache placeholder"},
		{want: "", mutate: nil, extra: "", name: "valid default cache", value: "{toolCache}/data"},
		{want: "", mutate: nil, extra: "", name: "valid adjacent path and cache", value: "${STORE}{toolCache}/data"},
		{mutate: nil, extra: "", name: "dollar-prefixed cache", value: "${toolCache}/data", want: "malformed nested"},
		{mutate: nil, extra: "", name: "cache inside unknown placeholder", value: "${prefix{toolCache}}", want: "malformed nested"},
		{mutate: nil, extra: "", name: "unterminated nested cache", value: "${prefix{toolCache", want: "malformed nested"},
		{want: "", mutate: nil, extra: "", name: "bare lookalike", value: "{toolCacheEnabled}"},
		{want: "", mutate: nil, extra: "", name: "dollar lookalike", value: "${toolCacheEnabled}"},
		{want: "", mutate: nil, extra: "", name: "nested lookalike without cache", value: "${prefix{toolCacheEnabled}}"},
		{want: "", mutate: nil, extra: "", name: "escaped literal brace", value: `${value#\{}`},
		{want: "", mutate: nil, extra: "", name: "escaped brace before real cache", value: `${value#\{}{toolCache}`},
		{want: "", mutate: nil, extra: "", name: "escaped cache-like text before real cache", value: `${value#\{toolCache}{toolCache}`},
		{want: "", mutate: nil, extra: "", name: "quoted opening brace before cache", value: `${value#'{'}{toolCache}`},
		{want: "", mutate: nil, extra: "", name: "quoted named cache is literal", value: `${value#'{toolCache:providers}'}{toolCache}`},
		{want: "", mutate: nil, extra: "", name: "quoted dollar opener is literal", value: `${value#'${'}{toolCache}`},
		{mutate: nil, extra: "", name: "quoted closing brace before nested cache", value: `${prefix"}"{toolCache}}`, want: "malformed nested"},
		{mutate: nil, name: "independent binding and cache errors", value: "${APP_BIN:missing}|{toolCache:unknown}", want: "unknown tool cache", extra: `APP_BIN target "missing"`},
		{
			extra: "", name: "real cache after nested lookalike", value: "${prefix{toolCacheEnabled}{toolCache:providers}}", want: "malformed nested", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Caches = []string{"providers"}
				a["root"] = app
			},
		},
		{
			extra: "", name: "oversized cache", value: "ok", want: "cache name exceeds 255 bytes", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Caches = []string{strings.Repeat("a", 256)}
				a["root"] = app
			},
		},
		{
			extra: "", name: "invalid cache declaration", value: "ok", want: "invalid cache name", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Caches = []string{"Providers"}
				a["root"] = app
			},
		},
		{
			extra: "", name: "reserved cache declaration", value: "ok", want: "reserved", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Caches = []string{"default"}
				a["root"] = app
			},
		},
		{
			extra: "", name: "duplicate cache declaration", value: "ok", want: "duplicate cache name", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Caches = []string{"providers", "providers"}
				a["root"] = app
			},
		},
		{
			extra: "", name: "shell cache declaration", value: "ok", want: "cache slots require a managed app", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Binary = nil
				app.Shell = &binmanager.AppConfigShell{Args: nil, Name: "echo"}
				app.Caches = []string{"providers"}
				a["root"] = app
			},
		},
		{
			extra: "", name: "shell runtime tool cache token", value: "{toolCache}/data", want: "allowed only for managed apps", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Binary = nil
				app.Shell = &binmanager.AppConfigShell{Args: nil, Name: "echo"}
				a["root"] = app
			},
		},
		{
			extra: "", name: "env tool cache token", value: "ok", want: "only in runtimeEnv", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Env = map[string]string{"CACHE": "{toolCache}/data"}
				a["root"] = app
			},
		},
		{
			extra: "", name: "unmanaged runtime tool cache token", value: "{toolCache}/data", want: "allowed only for managed apps", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Binary = nil
				a["root"] = app
			},
		},
		{want: "", mutate: nil, extra: "", name: "multiple", value: "${APP_BIN:dep} ${APP_BIN:dep}"},
		{mutate: nil, extra: "", name: "missing edge", value: "${APP_BIN:other}", want: "direct dependsOn"},
		{
			extra: "", name: "transitive edge", value: "${APP_BIN:leaf}", want: "direct dependsOn", mutate: func(a binmanager.MapOfApps) {
				a["dep"] = binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}, DependsOn: edges("leaf")}
				a["leaf"] = binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}}
			},
		},
		{extra: "", name: "missing target", value: "${APP_BIN:dep}", want: `APP_BIN target "dep" not found`, mutate: func(a binmanager.MapOfApps) { delete(a, "dep") }},
		{
			extra: "", name: "overlap", value: "ok", want: "key is also defined in env", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Env = map[string]string{"BINDING": "other"}
				a["root"] = app
			},
		},
		{mutate: nil, extra: "", name: "empty", value: "${APP_BIN:}", want: "malformed APP_BIN"},
		{mutate: nil, extra: "", name: "no colon", value: "${APP_BIN}", want: "malformed APP_BIN"},
		{mutate: nil, extra: "", name: "unterminated", value: "${APP_BIN:dep", want: "unterminated placeholder"},
		{mutate: nil, extra: "", name: "nested", value: "${OTHER${APP_BIN:dep}}", want: "malformed nested"},
		{mutate: nil, extra: "", name: "unknown", value: "${FOO:dep}", want: "unknown placeholder"},
		{mutate: nil, extra: "", name: "invalid name", value: "${APP_BIN:dep:more}", want: "malformed APP_BIN"},
		{
			extra: "", name: "env", value: "ok", want: "apps.root.env.BAD: APP_BIN", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Env = map[string]string{"BAD": "${APP_BIN:dep}"}
				a["root"] = app
			},
		},
		{
			extra: "", name: "runtimeEnv PATH", value: "ok", want: "apps.root.runtimeEnv.PATH: PATH cannot be set", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.RuntimeEnv = map[string]string{"PATH": "${STORE}/bin"}
				a["root"] = app
			},
		},
		{
			extra: "", name: "env PATH in any case", value: "ok", want: "apps.root.env.Path: PATH cannot be set", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Env = map[string]string{"Path": "/usr/bin"}
				a["root"] = app
			},
		},
		{
			want:  "",
			extra: "", name: "PATH-like names stay allowed", value: "ok", mutate: func(a binmanager.MapOfApps) {
				app := a["root"]
				app.Env = map[string]string{"PLAYWRIGHT_BROWSERS_PATH": "${STORE}/browsers", "PATHS": "x"}
				a["root"] = app
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			apps := binmanager.MapOfApps{"root": {Required: false, Lazy: false, Description: "", VersionCheck: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}, DependsOn: edges("dep"), RuntimeEnv: map[string]string{"BINDING": tt.value}}, "dep": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}}}
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
		"uv": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Uv: &binmanager.AppConfigUV{Runtime: "", RequiresPython: "", PackageName: "dep", Version: "1", LockFile: "br:x"}},
		"go": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Go: &binmanager.AppConfigGo{Runtime: "", PackageName: "example.com/dep", Version: "v1.0.0", LockFile: "br:x"}},
	}
	for name, app := range supported {
		t.Run(name, func(t *testing.T) {
			apps := binmanager.MapOfApps{"root": {Required: false, Lazy: false, Description: "", VersionCheck: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}, DependsOn: edges("dep"), RuntimeEnv: map[string]string{"BIN": "${APP_BIN:dep}"}}, "dep": app}
			if _, err := ValidateApps(apps, nil); err != nil {
				t.Fatalf("supported target rejected: %v", err)
			}
		})
	}
	for name, app := range map[string]binmanager.App{
		"shell": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Shell: &binmanager.AppConfigShell{Args: nil, Name: "echo"}},
		"bun":   {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Bun: &binmanager.AppConfigBun{PackageName: "", Version: "", BinPath: "", Runtime: "", LockFile: "", Dependencies: nil}}, "node": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Node: &binmanager.AppConfigNode{PackageName: "", Version: "", BinPath: "", Runtime: "", LockFile: "", Dependencies: nil}},
		"jvm": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Node: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Jvm: &binmanager.AppConfigJVM{JarURL: "", JarHash: "", Version: "", Runtime: "", MainClass: ""}},
	} {
		t.Run(name, func(t *testing.T) {
			apps := binmanager.MapOfApps{"root": {Required: false, Lazy: false, Description: "", VersionCheck: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}, DependsOn: edges("dep"), RuntimeEnv: map[string]string{"BIN": "${APP_BIN:dep}"}}, "dep": app}
			_, err := ValidateApps(apps, nil)
			if err == nil || !strings.Contains(err.Error(), "need a launcher (not supported yet)") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
