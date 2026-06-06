package runtimemanager

import (
	"context"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// TestRuntimeAppRef pins the single dispatch helper that encodes the App.*
// sub-config precedence shared by the three dispatch chains. Every runtime kind
// must map to its config.RuntimeKind and surface its explicit runtime reference;
// non-runtime apps (binary/shell/empty) must report ok=false.
func TestRuntimeAppRef(t *testing.T) {
	tests := []struct {
		name    string
		app     binmanager.App
		wantK   config.RuntimeKind
		wantRef string
		wantOk  bool
	}{
		{
			name:    "uv with explicit ref",
			app:     binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "yamllint", Version: "1", Runtime: "uv-rt"}},
			wantK:   config.RuntimeKindUV,
			wantRef: "uv-rt",
			wantOk:  true,
		},
		{
			name:    "uv without ref",
			app:     binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "yamllint", Version: "1"}},
			wantK:   config.RuntimeKindUV,
			wantRef: "",
			wantOk:  true,
		},
		{
			name:    "node",
			app:     binmanager.App{Node: &binmanager.AppConfigNode{PackageName: "eslint", Version: "9", BinPath: "b", Runtime: "node-rt"}},
			wantK:   config.RuntimeKindNode,
			wantRef: "node-rt",
			wantOk:  true,
		},
		{
			name:    "jvm",
			app:     binmanager.App{Jvm: &binmanager.AppConfigJVM{JarURL: "https://x/x.jar", JarHash: "h", Version: "7", Runtime: "jvm-rt"}},
			wantK:   config.RuntimeKindJVM,
			wantRef: "jvm-rt",
			wantOk:  true,
		},
		{
			name:    "go",
			app:     binmanager.App{Go: &binmanager.AppConfigGo{PackageName: "x", Version: "v1", Runtime: "go-rt"}},
			wantK:   config.RuntimeKindGo,
			wantRef: "go-rt",
			wantOk:  true,
		},
		{
			name:   "binary app is not runtime-managed",
			app:    binmanager.App{Binary: &binmanager.AppConfigBinary{Binaries: binmanager.MapOfBinaries{}}},
			wantOk: false,
		},
		{
			name:   "shell app is not runtime-managed",
			app:    binmanager.App{Shell: &binmanager.AppConfigShell{Name: "echo"}},
			wantOk: false,
		},
		{
			name:   "empty app is not runtime-managed",
			app:    binmanager.App{},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotK, gotRef, gotOk := runtimeAppRef(tt.app)
			if gotOk != tt.wantOk {
				t.Fatalf("runtimeAppRef() ok = %v, want %v", gotOk, tt.wantOk)
			}
			if gotK != tt.wantK {
				t.Errorf("runtimeAppRef() kind = %q, want %q", gotK, tt.wantK)
			}
			if gotRef != tt.wantRef {
				t.Errorf("runtimeAppRef() ref = %q, want %q", gotRef, tt.wantRef)
			}
		})
	}
}

// runtimeKindRoutingApps returns one app per runtime kind, each pointing at a
// runtime reference that does not exist. Routing the app to its kind-specific
// install/path method then fails deterministically inside that method
// ("failed to resolve <kind> runtime ..."), independent of the host environment
// — which is exactly the signal that the dispatch reached the right branch.
func runtimeKindRoutingApps() []struct {
	kind       string
	app        binmanager.App
	wantErrSub string
} {
	return []struct {
		kind       string
		app        binmanager.App
		wantErrSub string
	}{
		{
			kind:       "uv",
			app:        binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "yamllint", Version: "1", Runtime: "nonexistent"}},
			wantErrSub: "resolve UV runtime",
		},
		{
			kind:       "node",
			app:        binmanager.App{Node: &binmanager.AppConfigNode{PackageName: "eslint", Version: "9", BinPath: "b", Runtime: "nonexistent"}},
			wantErrSub: "resolve node runtime",
		},
		{
			kind:       "jvm",
			app:        binmanager.App{Jvm: &binmanager.AppConfigJVM{JarURL: "https://x/x.jar", JarHash: "h", Version: "7", Runtime: "nonexistent"}},
			wantErrSub: "resolve JVM runtime",
		},
		{
			kind:       "go",
			app:        binmanager.App{Go: &binmanager.AppConfigGo{PackageName: "x", Version: "v1", Runtime: "nonexistent", LockFile: "x"}},
			wantErrSub: "resolve Go runtime",
		},
	}
}

func TestComputeAppPath_RoutesEveryKind(t *testing.T) {
	rm := New(config.MapOfRuntimes{})

	for _, tc := range runtimeKindRoutingApps() {
		t.Run(tc.kind, func(t *testing.T) {
			_, err := rm.ComputeAppPath("app", tc.app)
			if err == nil {
				t.Fatalf("ComputeAppPath(%s) expected error for nonexistent runtime, got nil", tc.kind)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("ComputeAppPath(%s) error = %q, want it to contain %q (proves it routed to the %s path)", tc.kind, err.Error(), tc.wantErrSub, tc.kind)
			}
			if strings.Contains(err.Error(), "is not a runtime-managed app") {
				t.Errorf("ComputeAppPath(%s) fell through to the non-runtime error: %q", tc.kind, err.Error())
			}
		})
	}

	t.Run("empty app errors as non-runtime", func(t *testing.T) {
		_, err := rm.ComputeAppPath("app", binmanager.App{})
		if err == nil || !strings.Contains(err.Error(), "is not a runtime-managed app") {
			t.Fatalf("ComputeAppPath(empty) error = %v, want \"is not a runtime-managed app\"", err)
		}
	})

	t.Run("binary app errors as non-runtime", func(t *testing.T) {
		app := binmanager.App{Binary: &binmanager.AppConfigBinary{Binaries: binmanager.MapOfBinaries{}}}
		_, err := rm.ComputeAppPath("app", app)
		if err == nil || !strings.Contains(err.Error(), "is not a runtime-managed app") {
			t.Fatalf("ComputeAppPath(binary) error = %v, want \"is not a runtime-managed app\"", err)
		}
	})
}

func TestGetCommandInfo_RoutesEveryKind(t *testing.T) {
	rm := New(config.MapOfRuntimes{})

	for _, tc := range runtimeKindRoutingApps() {
		t.Run(tc.kind, func(t *testing.T) {
			// The install path fails (unresolvable runtime), but the error must
			// originate from the kind-specific branch, never from the fall-through
			// "not a runtime-managed app".
			_, err := rm.GetCommandInfo(context.Background(), "app", tc.app)
			if err == nil {
				t.Skipf("GetCommandInfo(%s) unexpectedly succeeded in this environment", tc.kind)
			}
			if strings.Contains(err.Error(), "is not a runtime-managed app") {
				t.Errorf("GetCommandInfo(%s) fell through to the non-runtime error: %q", tc.kind, err.Error())
			}
		})
	}

	t.Run("empty app errors as non-runtime", func(t *testing.T) {
		_, err := rm.GetCommandInfo(context.Background(), "app", binmanager.App{})
		if err == nil || !strings.Contains(err.Error(), "is not a runtime-managed app") {
			t.Fatalf("GetCommandInfo(empty) error = %v, want \"is not a runtime-managed app\"", err)
		}
	})

	t.Run("shell app errors as non-runtime", func(t *testing.T) {
		app := binmanager.App{Shell: &binmanager.AppConfigShell{Name: "echo"}}
		_, err := rm.GetCommandInfo(context.Background(), "app", app)
		if err == nil || !strings.Contains(err.Error(), "is not a runtime-managed app") {
			t.Fatalf("GetCommandInfo(shell) error = %v, want \"is not a runtime-managed app\"", err)
		}
	})
}

// TestCollectRequiredRuntimes_EveryKind asserts the consolidated dispatch in
// CollectRequiredRuntimes routes each kind to a runtime of the matching kind,
// both via an explicit reference and via default-by-kind resolution, and that a
// dangling explicit reference contributes nothing.
func TestCollectRequiredRuntimes_EveryKind(t *testing.T) {
	runtimes := config.MapOfRuntimes{
		"uv-rt":   {Kind: config.RuntimeKindUV, Mode: config.RuntimeModeManaged},
		"node-rt": {Kind: config.RuntimeKindNode, Mode: config.RuntimeModeManaged},
		"jvm-rt":  {Kind: config.RuntimeKindJVM, Mode: config.RuntimeModeManaged},
		"go-rt":   {Kind: config.RuntimeKindGo, Mode: config.RuntimeModeManaged},
	}

	t.Run("default-by-kind for every kind", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"a-uv":   {Required: true, Uv: &binmanager.AppConfigUV{PackageName: "x", Version: "1"}},
			"b-node": {Required: true, Node: &binmanager.AppConfigNode{PackageName: "x", Version: "1", BinPath: "b"}},
			"c-jvm":  {Required: true, Jvm: &binmanager.AppConfigJVM{JarURL: "https://x/x.jar", JarHash: "h", Version: "1"}},
			"d-go":   {Required: true, Go: &binmanager.AppConfigGo{PackageName: "x", Version: "1"}},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		want := []string{"go-rt", "jvm-rt", "node-rt", "uv-rt"}
		if !equalStringSlices(result, want) {
			t.Errorf("CollectRequiredRuntimes() = %v, want %v", result, want)
		}
	})

	t.Run("explicit ref for every kind", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"a-uv":   {Required: true, Uv: &binmanager.AppConfigUV{PackageName: "x", Version: "1", Runtime: "uv-rt"}},
			"b-node": {Required: true, Node: &binmanager.AppConfigNode{PackageName: "x", Version: "1", BinPath: "b", Runtime: "node-rt"}},
			"c-jvm":  {Required: true, Jvm: &binmanager.AppConfigJVM{JarURL: "https://x/x.jar", JarHash: "h", Version: "1", Runtime: "jvm-rt"}},
			"d-go":   {Required: true, Go: &binmanager.AppConfigGo{PackageName: "x", Version: "1", Runtime: "go-rt"}},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		want := []string{"go-rt", "jvm-rt", "node-rt", "uv-rt"}
		if !equalStringSlices(result, want) {
			t.Errorf("CollectRequiredRuntimes() = %v, want %v", result, want)
		}
	})

	t.Run("dangling explicit ref contributes nothing", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"a-uv": {Required: true, Uv: &binmanager.AppConfigUV{PackageName: "x", Version: "1", Runtime: "ghost"}},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 0 {
			t.Errorf("CollectRequiredRuntimes() = %v, want empty for dangling ref", result)
		}
	})

	t.Run("non-runtime apps contribute nothing", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"bin":   {Required: true, Binary: &binmanager.AppConfigBinary{Binaries: binmanager.MapOfBinaries{}}},
			"shell": {Required: true, Shell: &binmanager.AppConfigShell{Name: "echo"}},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 0 {
			t.Errorf("CollectRequiredRuntimes() = %v, want empty for non-runtime apps", result)
		}
	})
}
