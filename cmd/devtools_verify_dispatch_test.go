package cmd

import (
	"encoding/json"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// TestRuntimeAppInfo pins the shared verify-phase dispatch helper: every runtime
// kind must report its canonical kind string, configured version, runtime
// reference, and a non-nil typed sub-config; non-runtime apps must report ok=false.
func TestRuntimeAppInfo(t *testing.T) {
	tests := []struct {
		name    string
		app     binmanager.App
		wantK   string
		wantVer string
		wantRef string
		wantOk  bool
	}{
		{
			name:    "uv",
			app:     binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "yamllint", Version: "1.35.0", Runtime: "uv-rt"}},
			wantK:   "uv",
			wantVer: "1.35.0",
			wantRef: "uv-rt",
			wantOk:  true,
		},
		{
			name:    "node",
			app:     binmanager.App{Node: &binmanager.AppConfigNode{PackageName: "eslint", Version: "9.0.0", BinPath: "b", Runtime: "node-rt"}},
			wantK:   "node",
			wantVer: "9.0.0",
			wantRef: "node-rt",
			wantOk:  true,
		},
		{
			name:    "jvm",
			app:     binmanager.App{Jvm: &binmanager.AppConfigJVM{JarURL: "https://x/x.jar", JarHash: "h", Version: "7.0.0", Runtime: "jvm-rt"}},
			wantK:   "jvm",
			wantVer: "7.0.0",
			wantRef: "jvm-rt",
			wantOk:  true,
		},
		{
			name:    "go",
			app:     binmanager.App{Go: &binmanager.AppConfigGo{PackageName: "x", Version: "v1.3.0", Runtime: "go-rt"}},
			wantK:   "go",
			wantVer: "v1.3.0",
			wantRef: "go-rt",
			wantOk:  true,
		},
		{
			name:   "binary is not a runtime app",
			app:    binmanager.App{Binary: &binmanager.AppConfigBinary{Version: "1.0"}},
			wantOk: false,
		},
		{
			name:   "shell is not a runtime app",
			app:    binmanager.App{Shell: &binmanager.AppConfigShell{Name: "echo"}},
			wantOk: false,
		},
		{
			name:   "empty is not a runtime app",
			app:    binmanager.App{},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotK, gotVer, gotRef, gotCfg, gotOk := runtimeAppInfo(tt.app)
			if gotOk != tt.wantOk {
				t.Fatalf("runtimeAppInfo() ok = %v, want %v", gotOk, tt.wantOk)
			}
			if gotK != tt.wantK {
				t.Errorf("runtimeAppInfo() kind = %q, want %q", gotK, tt.wantK)
			}
			if gotVer != tt.wantVer {
				t.Errorf("runtimeAppInfo() version = %q, want %q", gotVer, tt.wantVer)
			}
			if gotRef != tt.wantRef {
				t.Errorf("runtimeAppInfo() ref = %q, want %q", gotRef, tt.wantRef)
			}
			if tt.wantOk && gotCfg == nil {
				t.Error("runtimeAppInfo() subConfig is nil for a runtime app")
			}
			if !tt.wantOk && gotCfg != nil {
				t.Errorf("runtimeAppInfo() subConfig = %v, want nil for a non-runtime app", gotCfg)
			}
			// runtimeAppVersion is the thin wrapper used by phase 3.
			if got := runtimeAppVersion(tt.app); got != tt.wantVer {
				t.Errorf("runtimeAppVersion() = %q, want %q", got, tt.wantVer)
			}
		})
	}
}

// TestRuntimeAppInfo_SubConfigMarshalsLikeTypedField guards the fingerprint
// path: the sub-config returned as `any` must marshal byte-identically to the
// typed field, so the consolidation does not perturb cached verify fingerprints.
func TestRuntimeAppInfo_SubConfigMarshalsLikeTypedField(t *testing.T) {
	app := binmanager.App{Go: &binmanager.AppConfigGo{PackageName: "x", Version: "v1.3.0", Runtime: "go-rt"}}
	_, _, _, sub, ok := runtimeAppInfo(app)
	if !ok {
		t.Fatal("expected ok for go app")
	}
	viaAny, _ := json.Marshal(sub)
	viaTyped, _ := json.Marshal(app.Go)
	if string(viaAny) != string(viaTyped) {
		t.Errorf("sub-config marshal mismatch:\n any   = %s\n typed = %s", viaAny, viaTyped)
	}
}

// TestRuntimeAppKeyAndFP_EveryKind asserts the consolidated fingerprint bridge
// folds each kind's app config into the fingerprint (changing the version
// changes the fingerprint) for uv, node, jvm and go.
func TestRuntimeAppKeyAndFP_EveryKind(t *testing.T) {
	runtimes := config.MapOfRuntimes{
		"uv-rt":   {Kind: config.RuntimeKindUV},
		"node-rt": {Kind: config.RuntimeKindNode},
		"jvm-rt":  {Kind: config.RuntimeKindJVM},
		"go-rt":   {Kind: config.RuntimeKindGo},
	}

	cases := []struct {
		kind   string
		mkApp  func(version string) binmanager.App
		v1, v2 string
	}{
		{
			kind: "uv",
			mkApp: func(v string) binmanager.App {
				return binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "yamllint", Version: v, Runtime: "uv-rt"}}
			},
			v1: "1.35.0",
			v2: "1.36.0",
		},
		{
			kind: "node",
			mkApp: func(v string) binmanager.App {
				return binmanager.App{Node: &binmanager.AppConfigNode{PackageName: "eslint", Version: v, BinPath: "b", Runtime: "node-rt"}}
			},
			v1: "9.0.0",
			v2: "9.1.0",
		},
		{
			kind: "jvm",
			mkApp: func(v string) binmanager.App {
				return binmanager.App{Jvm: &binmanager.AppConfigJVM{JarURL: "https://x/x.jar", JarHash: "h", Version: v, Runtime: "jvm-rt"}}
			},
			v1: "7.0.0",
			v2: "7.1.0",
		},
		{
			kind: "go",
			mkApp: func(v string) binmanager.App {
				return binmanager.App{Go: &binmanager.AppConfigGo{PackageName: "x", Version: v, Runtime: "go-rt"}}
			},
			v1: "v1.3.0",
			v2: "v1.4.0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			e1 := runtimeAppEntry{name: "app", app: tc.mkApp(tc.v1), kind: tc.kind}
			key, fp := runtimeAppKeyAndFP(e1, runtimes, "linux", "amd64")
			if key == "" {
				t.Fatal("expected non-empty key")
			}
			if fp == "" {
				t.Fatalf("expected non-empty fingerprint for kind %s (app config must be folded in)", tc.kind)
			}

			e2 := runtimeAppEntry{name: "app", app: tc.mkApp(tc.v2), kind: tc.kind}
			_, fp2 := runtimeAppKeyAndFP(e2, runtimes, "linux", "amd64")
			if fp == fp2 {
				t.Errorf("fingerprint unchanged when %s version changed (config not folded in)", tc.kind)
			}
		})
	}
}

// TestRuntimeAppKeyAndFP_DefaultRuntimeFold verifies that, with no explicit
// runtime reference, the default runtime of the app's kind is resolved and its
// config contributes to the fingerprint.
func TestRuntimeAppKeyAndFP_DefaultRuntimeFold(t *testing.T) {
	withRT := config.MapOfRuntimes{"uv-rt": {Kind: config.RuntimeKindUV, Mode: config.RuntimeModeManaged}}
	noRT := config.MapOfRuntimes{}

	entry := runtimeAppEntry{
		name: "yamllint",
		app:  binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "yamllint", Version: "1.35.0"}},
		kind: "uv",
	}
	_, fpWith := runtimeAppKeyAndFP(entry, withRT, "linux", "amd64")
	_, fpNo := runtimeAppKeyAndFP(entry, noRT, "linux", "amd64")
	if fpWith == fpNo {
		t.Error("expected fingerprint to differ when a default runtime is resolved vs absent")
	}
}

// TestResolveDefaultRuntimeName_AllKinds confirms the registry-driven default
// resolution returns a runtime of the matching kind for every kind, and "" for
// an unknown kind.
func TestResolveDefaultRuntimeName_AllKinds(t *testing.T) {
	runtimes := config.MapOfRuntimes{
		"uv-rt":   {Kind: config.RuntimeKindUV},
		"node-rt": {Kind: config.RuntimeKindNode},
		"jvm-rt":  {Kind: config.RuntimeKindJVM},
		"go-rt":   {Kind: config.RuntimeKindGo},
	}

	for kind, want := range map[string]string{
		"uv":   "uv-rt",
		"node": "node-rt",
		"jvm":  "jvm-rt",
		"go":   "go-rt",
	} {
		if got := resolveDefaultRuntimeName(runtimes, kind); got != want {
			t.Errorf("resolveDefaultRuntimeName(%q) = %q, want %q", kind, got, want)
		}
	}

	if got := resolveDefaultRuntimeName(runtimes, "bogus"); got != "" {
		t.Errorf("resolveDefaultRuntimeName(bogus) = %q, want empty for unknown kind", got)
	}
	if got := resolveDefaultRuntimeName(runtimes, ""); got != "" {
		t.Errorf("resolveDefaultRuntimeName(\"\") = %q, want empty for empty kind", got)
	}
}
