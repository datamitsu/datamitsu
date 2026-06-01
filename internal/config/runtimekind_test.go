package config

import (
	"sort"
	"strings"
	"testing"
)

func TestLookupRuntimeKind_KnownKinds(t *testing.T) {
	tests := []struct {
		kind          RuntimeKind
		wantName      string
		wantSystemCmd string
	}{
		{RuntimeKindUV, "uv", "uv"},
		{RuntimeKindNode, "node", "node"},
		{RuntimeKindJVM, "jvm", "java"},
		{RuntimeKindGo, "go", "go"},
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			info, ok := LookupRuntimeKind(tt.kind)
			if !ok {
				t.Fatalf("LookupRuntimeKind(%q) ok = false, want true", tt.kind)
			}
			if info.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", info.Name, tt.wantName)
			}
			if info.SystemCommand != tt.wantSystemCmd {
				t.Errorf("SystemCommand = %q, want %q", info.SystemCommand, tt.wantSystemCmd)
			}
			if info.HashFields == nil {
				t.Errorf("HashFields is nil for kind %q", tt.kind)
			}
		})
	}
}

func TestLookupRuntimeKind_UnknownKind(t *testing.T) {
	for _, kind := range []RuntimeKind{"", "legacy-removed", "python"} {
		if _, ok := LookupRuntimeKind(kind); ok {
			t.Errorf("LookupRuntimeKind(%q) ok = true, want false", kind)
		}
	}
}

func TestRuntimeKindHashFields(t *testing.T) {
	t.Run("returns kind version fields in order", func(t *testing.T) {
		cases := []struct {
			kind RuntimeKind
			rc   RuntimeConfig
			want []string
		}{
			{
				kind: RuntimeKindUV,
				rc:   RuntimeConfig{Kind: RuntimeKindUV, UV: &RuntimeConfigUV{PythonVersion: "3.12"}},
				want: []string{"3.12"},
			},
			{
				kind: RuntimeKindNode,
				rc:   RuntimeConfig{Kind: RuntimeKindNode, Node: &RuntimeConfigNode{NodeVersion: "22.0.0", PNPMVersion: "10.7.0", PNPMHash: "abc"}},
				want: []string{"22.0.0", "10.7.0", "abc"},
			},
			{
				kind: RuntimeKindJVM,
				rc:   RuntimeConfig{Kind: RuntimeKindJVM, JVM: &RuntimeConfigJVM{JavaVersion: "21"}},
				want: []string{"21"},
			},
			{
				kind: RuntimeKindGo,
				rc:   RuntimeConfig{Kind: RuntimeKindGo, Go: &RuntimeConfigGo{GoVersion: "1.26.3"}},
				want: []string{"1.26.3"},
			},
		}
		for _, c := range cases {
			info, ok := LookupRuntimeKind(c.kind)
			if !ok {
				t.Fatalf("LookupRuntimeKind(%q) ok = false", c.kind)
			}
			got := info.HashFields(c.rc)
			if !equalStrings(got, c.want) {
				t.Errorf("kind %q HashFields = %v, want %v", c.kind, got, c.want)
			}
		}
	})

	t.Run("returns nil when sub-config absent", func(t *testing.T) {
		for _, kind := range AllRuntimeKinds() {
			info, _ := LookupRuntimeKind(kind)
			// An empty RuntimeConfig has no typed sub-config for any kind.
			if got := info.HashFields(RuntimeConfig{Kind: kind}); got != nil {
				t.Errorf("kind %q HashFields with absent sub-config = %v, want nil", kind, got)
			}
		}
	})
}

func TestRuntimeKindValidate(t *testing.T) {
	t.Run("uv has no required sub-config validation", func(t *testing.T) {
		info, _ := LookupRuntimeKind(RuntimeKindUV)
		if info.Validate != nil {
			t.Error("uv Validate should be nil (pythonVersion is optional)")
		}
	})

	t.Run("node missing config errors", func(t *testing.T) {
		info, _ := LookupRuntimeKind(RuntimeKindNode)
		errs := info.Validate("rt", RuntimeConfig{Kind: RuntimeKindNode})
		if len(errs) == 0 {
			t.Fatal("expected node validation error when node config is nil")
		}
		if !strings.Contains(errs[0], "Node runtime requires node config") {
			t.Errorf("unexpected error: %q", errs[0])
		}
	})

	t.Run("go system mode tolerates missing goVersion", func(t *testing.T) {
		info, _ := LookupRuntimeKind(RuntimeKindGo)
		errs := info.Validate("rt", RuntimeConfig{Kind: RuntimeKindGo, Mode: RuntimeModeSystem})
		if len(errs) != 0 {
			t.Errorf("go system mode should not require goVersion, got: %v", errs)
		}
	})

	t.Run("go managed mode requires goVersion", func(t *testing.T) {
		info, _ := LookupRuntimeKind(RuntimeKindGo)
		errs := info.Validate("rt", RuntimeConfig{Kind: RuntimeKindGo, Mode: RuntimeModeManaged})
		if len(errs) == 0 {
			t.Fatal("expected go validation error in managed mode without go config")
		}
	})
}

func TestAllRuntimeKinds(t *testing.T) {
	got := AllRuntimeKinds()
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	want := []RuntimeKind{RuntimeKindGo, RuntimeKindJVM, RuntimeKindNode, RuntimeKindUV}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(got) != len(want) {
		t.Fatalf("AllRuntimeKinds() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllRuntimeKinds()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
