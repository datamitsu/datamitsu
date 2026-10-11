package config

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func yes() *bool { b := true; return &b }

func no() *bool { b := false; return &b }

func binaryApp() *binmanager.AppConfigBinary {
	return &binmanager.AppConfigBinary{Binaries: binmanager.MapOfBinaries{
		"linux": {"amd64": {"glibc": {URL: "https://example.invalid/x", Hash: "sha256:" + strings.Repeat("0", 64), ContentType: binmanager.BinContentTypeBinary}}},
	}}
}

func TestValidateAppPrepare(t *testing.T) {
	for _, tt := range []struct {
		name    string
		app     binmanager.App
		wantErr string
	}{
		{
			name:    "shell app rejected",
			app:     binmanager.App{Shell: &binmanager.AppConfigShell{Name: "echo"}, Prepare: &binmanager.AppPrepare{Args: []string{}, Network: yes()}},
			wantErr: "prepare is not supported on shell apps",
		},
		{
			name:    "network required",
			app:     binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{Args: []string{}}},
			wantErr: "network: required",
		},
		{
			name:    "args required",
			app:     binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{Network: no()}},
			wantErr: "args: required",
		},
		{
			name:    "empty args and false network accepted",
			app:     binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{Args: []string{}, Network: no()}},
			wantErr: "",
		},
		{
			name: "store placeholder rejected",
			app: binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{
				Args: []string{"--dir", "${STORE}/x"}, Network: no(),
			}},
			wantErr: "unknown placeholder",
		},
		{
			name: "tool placeholder rejected",
			app: binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{
				Args: []string{"{root}/x"}, Network: no(),
			}},
			wantErr: "tool placeholders",
		},
		{
			name: "allowed placeholders accepted",
			app: binmanager.App{Binary: binaryApp(), DependsOn: edges("dep"), Prepare: &binmanager.AppPrepare{
				Args:    []string{"--dir", "${APP_PREPARED}/sub", "--bin", "${APP_BIN:dep}", "--app", "${APP_DIR}"},
				Env:     map[string]string{"CACHE": "${APP_PREPARED}"},
				Network: no(),
			}},
			wantErr: "",
		},
		{
			name: "path value validated",
			app: binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{
				Args: []string{}, Network: no(), Path: "wide",
			}},
			wantErr: `must be "declared" or "inherit"`,
		},
		{
			name: "prepare env rejects PATH",
			app: binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{
				Args: []string{}, Network: no(), Env: map[string]string{"PATH": "/bin"},
			}},
			wantErr: "PATH cannot be set",
		},
		{
			name: "prepare env rejects isolation keys",
			app: binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{
				Args: []string{}, Network: no(), Env: map[string]string{"TMPDIR": "/tmp"},
			}},
			wantErr: "owned by datamitsu during prepare",
		},
		{
			name: "prepare env keeps XDG_CONFIG_HOME",
			app: binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{
				Args: []string{}, Network: no(), Env: map[string]string{"XDG_CONFIG_HOME": "/cfg"},
			}},
			wantErr: "",
		},
		{
			name: "binPath absolute rejected",
			app: binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{
				Args: []string{}, Network: no(), BinPath: "/bin/sh",
			}},
			wantErr: "must be relative",
		},
		{
			name: "binPath NUL rejected",
			app: binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{
				Args: []string{}, Network: no(), BinPath: "x\x00args:2\x00y",
			}},
			wantErr: "NUL bytes are not allowed in binPath",
		},
		{
			name: "binPath escaping rejected",
			app: binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{
				Args: []string{}, Network: no(), BinPath: "../bin/sh",
			}},
			wantErr: "must stay inside",
		},
		{
			name: "binPath on single-file binary rejected",
			app: binmanager.App{Binary: binaryApp(), Prepare: &binmanager.AppPrepare{
				Args: []string{}, Network: no(), BinPath: "bin/other",
			}},
			wantErr: "no other executable",
		},
		{
			name: "binPath on node accepted",
			app: binmanager.App{Node: &binmanager.AppConfigNode{PackageName: "x", Version: "1", BinPath: "x", LockFile: "br:x"}, Prepare: &binmanager.AppPrepare{
				Args: []string{}, Network: no(), BinPath: "node_modules/.bin/cli",
			}},
			wantErr: "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			apps := binmanager.MapOfApps{"app": tt.app, "dep": {Binary: binaryApp()}}
			errs := validateAppPrepare(apps, []string{"app"})
			if tt.wantErr == "" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e, tt.wantErr) {
					found = true
				}
			}
			if !found {
				t.Fatalf("errors %v, want one containing %q", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateAppPrepareEnvRules(t *testing.T) {
	// ${APP_PREPARED} in app env is rejected: env reaches installers.
	apps := binmanager.MapOfApps{
		"app": {Binary: binaryApp(), Env: map[string]string{"CACHE": "${APP_PREPARED}"}},
	}
	errs := validateAppRuntimeEnv(apps, []string{"app"})
	if len(errs) == 0 || !strings.Contains(errs[0], "${APP_PREPARED} is not allowed in env") {
		t.Fatalf("errs = %v", errs)
	}

	// In runtimeEnv it requires a declared prepare step.
	apps = binmanager.MapOfApps{
		"app": {Binary: binaryApp(), RuntimeEnv: map[string]string{"CACHE": "${APP_PREPARED}"}},
	}
	errs = validateAppRuntimeEnv(apps, []string{"app"})
	if len(errs) == 0 || !strings.Contains(errs[0], "requires apps.app.prepare") {
		t.Fatalf("errs = %v", errs)
	}

	// With prepare declared it passes.
	app := apps["app"]
	app.Prepare = &binmanager.AppPrepare{Args: []string{}, Network: no()}
	apps["app"] = app
	if errs = validateAppRuntimeEnv(apps, []string{"app"}); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
}

func TestValidateDependencyEdges(t *testing.T) {
	for _, tt := range []struct {
		name    string
		edges   []binmanager.AppDependency
		wantErr string
	}{
		{name: "plain names", edges: edges("a", "b")},
		{
			name:    "alias duplicate case-insensitive",
			edges:   []binmanager.AppDependency{{App: "a", Alias: "validator"}, {App: "b", Alias: "Validator"}},
			wantErr: "already used",
		},
		{
			name:    "same target twice",
			edges:   []binmanager.AppDependency{{App: "a", Alias: "one"}, {App: "a", Alias: "two"}},
			wantErr: "listed more than once",
		},
		{
			name:    "invalid alias characters",
			edges:   []binmanager.AppDependency{{App: "a", Alias: "not/ok"}},
			wantErr: "as",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateDependencyEdges(tt.edges, "root")
			if tt.wantErr == "" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			if len(errs) == 0 || !strings.Contains(errs[0], tt.wantErr) {
				t.Fatalf("errs = %v, want %q", errs, tt.wantErr)
			}
		})
	}
}
