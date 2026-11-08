package config

import (
	"slices"
	"strings"
	"testing"
)

func TestLookupRuntimeKind_KnownKinds(t *testing.T) {
	tests := []struct {
		kind          RuntimeKind
		wantName      string
		wantSystemCmd string
	}{
		{RuntimeKindBun, "bun", "bun"},
		{RuntimeKindUV, "uv", "uv"},
		{RuntimeKindNode, "node", "node"},
		{RuntimeKindJVM, "jvm", "java"},
		{RuntimeKindGo, "go", "go"},
		{RuntimeKindPNPM, "pnpm", "pnpm"},
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
				kind: RuntimeKindBun,
				// The pnpm runtime folds into the app hash, not the Bun
				// runtime's, so a pnpm bump never re-downloads Bun.
				rc: RuntimeConfig{Kind: RuntimeKindBun, Bun: &RuntimeConfigBun{
					BunVersion:  "1.4.1",
					PNPMRuntime: "pnpm",
				}},
				want: []string{"1.4.1"},
			},
			{
				kind: RuntimeKindUV,
				rc:   RuntimeConfig{Kind: RuntimeKindUV, UV: &RuntimeConfigUV{PythonVersion: "3.12"}},
				want: []string{"3.12"},
			},
			{
				kind: RuntimeKindNode,
				rc:   RuntimeConfig{Kind: RuntimeKindNode, Node: &RuntimeConfigNode{NodeVersion: "22.0.0", PNPMRuntime: "pnpm"}},
				want: []string{"22.0.0"},
			},
			{
				kind: RuntimeKindPNPM,
				rc:   RuntimeConfig{Kind: RuntimeKindPNPM, PNPM: &RuntimeConfigPNPM{PNPMVersion: "12.4.1"}},
				want: []string{"12.4.1"},
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
	t.Run("uv hashes complete catalogs in sorted key order", func(t *testing.T) {
		entry := func(url string) PythonDownload {
			return PythonDownload{
				Name: "cpython", Arch: PythonDownloadArch{Family: "x86_64"}, OS: "linux", LibC: "gnu",
				Major: 3, Minor: 14, Patch: 6, URL: url, Hash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			}
		}
		info, _ := LookupRuntimeKind(RuntimeKindUV)
		rc := RuntimeConfig{Kind: RuntimeKindUV, UV: &RuntimeConfigUV{PythonVersion: "3.14.6", PythonDownloads: map[string]PythonDownload{
			"z-key": entry("https://example.com/z"), "a-key": entry("https://example.com/a"),
		}}}
		got := info.HashFields(rc)
		want := []string{"3.14.6", "a-key", "cpython", "x86_64", "", "linux", "gnu", "3", "14", "6", "", "https://example.com/a", "", "", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "z-key", "cpython", "x86_64", "", "linux", "gnu", "3", "14", "6", "", "https://example.com/z", "", "", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		if !slices.Equal(got, want) {
			t.Fatalf("UV HashFields = %v, want %v", got, want)
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
	t.Run("uv allows an omitted optional Python version and catalog", func(t *testing.T) {
		info, _ := LookupRuntimeKind(RuntimeKindUV)
		if errs := info.Validate("rt", RuntimeConfig{Kind: RuntimeKindUV, UV: &RuntimeConfigUV{}}); len(errs) != 0 {
			t.Errorf("empty optional uv config errors = %v", errs)
		}
	})

	t.Run("validates pinned stable default-GIL catalog entries", func(t *testing.T) {
		info, _ := LookupRuntimeKind(RuntimeKindUV)
		entry := PythonDownload{
			Name: "cpython", Arch: PythonDownloadArch{Family: "x86_64"}, OS: "linux", LibC: "gnu",
			Major: 3, Minor: 14, Patch: 6, URL: "https://example.com/python.tar.zst",
			Hash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}
		rc := RuntimeConfig{Kind: RuntimeKindUV, UV: &RuntimeConfigUV{
			PythonVersion: "3.14.6", PythonDownloads: map[string]PythonDownload{"catalog-key": entry},
		}}
		if errs := info.Validate("uv-main", rc); len(errs) != 0 {
			t.Fatalf("valid Python download rejected: %v", errs)
		}
		rc.UV.PythonVersion = ""
		if errs := info.Validate("uv-main", rc); !strings.Contains(strings.Join(errs, "\n"), "pythonVersion is required") {
			t.Fatalf("catalog without Python version errors = %v, want version requirement", errs)
		}
		rc.UV.PythonVersion = "3.14.6"

		entry.Prerelease = "rc1"
		rc.UV.PythonDownloads["catalog-key"] = entry
		if errs := info.Validate("uv-main", rc); !strings.Contains(strings.Join(errs, "\n"), "stable default-GIL") {
			t.Fatalf("unstable Python download errors = %v, want stable/default-GIL error", errs)
		}
	})

	t.Run("rejects version mismatch and noncanonical hash", func(t *testing.T) {
		info, _ := LookupRuntimeKind(RuntimeKindUV)
		entry := PythonDownload{
			Name: "cpython", Arch: PythonDownloadArch{Family: "x86_64"}, OS: "linux", LibC: "gnu",
			Major: 3, Minor: 14, Patch: 5, URL: "https://example.com/python.tar.zst", Hash: strings.Repeat("a", 64),
		}
		rc := RuntimeConfig{Kind: RuntimeKindUV, UV: &RuntimeConfigUV{
			PythonVersion: "3.14.6", PythonDownloads: map[string]PythonDownload{"catalog-key": entry},
		}}
		errs := strings.Join(info.Validate("uv-main", rc), "\n")
		for _, want := range []string{"does not match uv.pythonVersion", "canonical SHA-256"} {
			if !strings.Contains(errs, want) {
				t.Errorf("validation errors %q do not contain %q", errs, want)
			}
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

	t.Run("bun missing config errors", func(t *testing.T) {
		info, _ := LookupRuntimeKind(RuntimeKindBun)
		errs := info.Validate("rt", RuntimeConfig{Kind: RuntimeKindBun})
		if len(errs) == 0 {
			t.Fatal("expected Bun validation error when bun config is nil")
		}
		if !strings.Contains(errs[0], "Bun runtime requires bun config") {
			t.Errorf("unexpected error: %q", errs[0])
		}
	})

	t.Run("bun requires a pnpm runtime", func(t *testing.T) {
		info, _ := LookupRuntimeKind(RuntimeKindBun)
		errs := info.Validate("rt", RuntimeConfig{
			Kind: RuntimeKindBun,
			Bun:  &RuntimeConfigBun{BunVersion: "1.4.1"},
		})
		if joined := strings.Join(errs, "\n"); !strings.Contains(joined, "bun.pnpmRuntime is required") {
			t.Errorf("validation errors %q do not contain bun.pnpmRuntime is required", joined)
		}
	})

	t.Run("node requires a pnpm runtime", func(t *testing.T) {
		info, _ := LookupRuntimeKind(RuntimeKindNode)
		errs := info.Validate("rt", RuntimeConfig{
			Kind: RuntimeKindNode,
			Node: &RuntimeConfigNode{NodeVersion: "26.2.0"},
		})
		if joined := strings.Join(errs, "\n"); !strings.Contains(joined, "node.pnpmRuntime is required") {
			t.Errorf("validation errors %q do not contain node.pnpmRuntime is required", joined)
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
	slices.Sort(got)
	want := []RuntimeKind{RuntimeKindBun, RuntimeKindGo, RuntimeKindJVM, RuntimeKindNode, RuntimeKindPNPM, RuntimeKindUV}
	slices.Sort(want)
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
