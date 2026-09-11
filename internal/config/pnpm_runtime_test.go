package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

// testPNPMBinaries returns managed binaries shaped like the pnpm GitHub release
// entries in config/src/runtimes.json. Each call returns a fresh map so tests
// can mutate it.
func testPNPMBinaries() binmanager.MapOfBinaries {
	entry := func(asset, binaryPath string, contentType binmanager.BinContentType, hashChar string) binmanager.BinaryOsArchInfo {
		return binmanager.BinaryOsArchInfo{
			URL:         "https://github.com/pnpm/pnpm/releases/download/v12.4.1/" + asset,
			Hash:        strings.Repeat(hashChar, 64),
			ContentType: contentType,
			BinaryPath:  &binaryPath,
			ExtractDir:  true,
		}
	}
	return binmanager.MapOfBinaries{
		syslist.OsTypeLinux: {
			syslist.ArchTypeAmd64: {
				"glibc": entry("pnpm-linux-x64.tar.gz", "pnpm", binmanager.BinContentTypeTarGz, "1"),
				"musl":  entry("pnpm-linux-x64-musl.tar.gz", "pnpm", binmanager.BinContentTypeTarGz, "2"),
			},
		},
		syslist.OsTypeWindows: {
			syslist.ArchTypeAmd64: {
				"unknown": entry("pnpm-win32-x64.zip", "pnpm.exe", binmanager.BinContentTypeZip, "3"),
			},
		},
	}
}

// testPNPMRuntime returns a valid managed runtime of kind pnpm.
func testPNPMRuntime() RuntimeConfig {
	return RuntimeConfig{
		Kind:    RuntimeKindPNPM,
		Mode:    RuntimeModeManaged,
		Managed: &RuntimeConfigManaged{Binaries: testPNPMBinaries()},
		PNPM:    &RuntimeConfigPNPM{PNPMVersion: "12.4.1"},
	}
}

func TestValidateRuntimes_PNPMKind(t *testing.T) {
	missingHash := testPNPMRuntime()
	glibc := missingHash.Managed.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]["glibc"]
	glibc.Hash = ""
	missingHash.Managed.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]["glibc"] = glibc

	withVersion := func(version string) RuntimeConfig {
		rc := testPNPMRuntime()
		rc.PNPM.PNPMVersion = version
		return rc
	}
	system := func(pnpm *RuntimeConfigPNPM) RuntimeConfig {
		return RuntimeConfig{Kind: RuntimeKindPNPM, Mode: RuntimeModeSystem, System: &RuntimeConfigSystem{Command: "pnpm"}, PNPM: pnpm}
	}

	// want is a substring of the error; empty means the runtime is valid.
	tests := []struct {
		name string
		rc   RuntimeConfig
		want string
	}{
		{"managed valid", testPNPMRuntime(), ""},
		{
			"managed without pnpm config",
			RuntimeConfig{Kind: RuntimeKindPNPM, Mode: RuntimeModeManaged, Managed: &RuntimeConfigManaged{Binaries: testPNPMBinaries()}},
			`runtime "pnpm": pnpm runtime requires pnpm config with pnpmVersion`,
		},
		{"managed without pnpmVersion", withVersion(""), `runtime "pnpm": pnpm.pnpmVersion is required`},
		{"managed with invalid pnpmVersion", withVersion("../12"), `runtime "pnpm": pnpm.pnpmVersion "../12" contains invalid characters`},
		// A system pnpm is whatever the host provides, so its version is optional.
		{"system without pnpm config", system(nil), ""},
		{"system without pnpmVersion", system(&RuntimeConfigPNPM{}), ""},
		{"system with invalid pnpmVersion", system(&RuntimeConfigPNPM{PNPMVersion: "12 && x"}), `pnpm.pnpmVersion "12 && x" contains invalid characters`},
		// The archives go through the managed-runtime checks, messages unchanged.
		{"managed entry without hash", missingHash, `runtime "pnpm" (linux/amd64/glibc): hash is required`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRuntimes(MapOfRuntimes{"pnpm": tt.rc})
			if tt.want == "" {
				if err != nil {
					t.Fatalf("ValidateRuntimes() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateRuntimes() error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestValidateRuntimes_PNPMRuntimeRef(t *testing.T) {
	withRef := func(kind RuntimeKind, ref string) RuntimeConfig {
		rc := RuntimeConfig{Kind: kind, Mode: RuntimeModeSystem, System: &RuntimeConfigSystem{Command: string(kind)}}
		if kind == RuntimeKindBun {
			rc.Bun = &RuntimeConfigBun{BunVersion: "1.4.1", PNPMRuntime: ref}
		} else {
			rc.Node = &RuntimeConfigNode{NodeVersion: "26.2.0", PNPMRuntime: ref}
		}
		return rc
	}

	// want takes the field name ("node.pnpmRuntime" or "bun.pnpmRuntime");
	// empty means the runtime is valid.
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{"names a pnpm runtime", "pnpm", ""},
		{"missing", "", `runtime "rt": %s is required`},
		{"not found", "pnpm-11", `runtime "rt": %s "pnpm-11" not found`},
		{"wrong kind", "uv", `runtime "rt": %s "uv" is kind "uv", expected "pnpm"`},
	}

	for _, kind := range []RuntimeKind{RuntimeKindNode, RuntimeKindBun} {
		field := string(kind) + ".pnpmRuntime"
		for _, tt := range tests {
			t.Run(string(kind)+"/"+tt.name, func(t *testing.T) {
				runtimes := MapOfRuntimes{
					"rt":   withRef(kind, tt.ref),
					"pnpm": testPNPMRuntime(),
					"uv":   {Kind: RuntimeKindUV, Mode: RuntimeModeSystem, System: &RuntimeConfigSystem{Command: "uv"}},
				}
				err := ValidateRuntimes(runtimes)
				if tt.want == "" {
					if err != nil {
						t.Fatalf("ValidateRuntimes() unexpected error: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatal("ValidateRuntimes() error = nil, want an error")
				}
				if want := strings.Replace(tt.want, "%s", field, 1); !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			})
		}
	}
}

// No app runs on pnpm: the app-side kind check keeps it from being selected.
func TestValidateApps_NodeAppCannotRunOnPNPMRuntime(t *testing.T) {
	apps := binmanager.MapOfApps{
		"prettier": {
			Node: &binmanager.AppConfigNode{
				PackageName: "prettier",
				Version:     "3.8.3",
				BinPath:     "node_modules/.bin/prettier",
				LockFile:    "lock",
				Runtime:     "pnpm",
			},
		},
	}
	_, err := ValidateApps(apps, MapOfRuntimes{"pnpm": testPNPMRuntime()})
	if err == nil || !strings.Contains(err.Error(), `runtime "pnpm" is kind "pnpm", expected "node"`) {
		t.Fatalf("ValidateApps() error = %v, want a kind mismatch", err)
	}
}

func TestRuntimeConfigPNPMRuntimeRef(t *testing.T) {
	tests := []struct {
		name string
		rc   RuntimeConfig
		want string
	}{
		{"node", RuntimeConfig{Kind: RuntimeKindNode, Node: &RuntimeConfigNode{PNPMRuntime: "pnpm-node"}}, "pnpm-node"},
		{"bun", RuntimeConfig{Kind: RuntimeKindBun, Bun: &RuntimeConfigBun{PNPMRuntime: "pnpm-bun"}}, "pnpm-bun"},
		{"node without node config", RuntimeConfig{Kind: RuntimeKindNode}, ""},
		{"bun without bun config", RuntimeConfig{Kind: RuntimeKindBun}, ""},
		{"pnpm", testPNPMRuntime(), ""},
		// The accessor follows Kind, not whichever sub-config happens to be set.
		{"jvm with a stray node config", RuntimeConfig{Kind: RuntimeKindJVM, Node: &RuntimeConfigNode{PNPMRuntime: "pnpm"}}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rc.PNPMRuntimeRef(); got != tt.want {
				t.Errorf("PNPMRuntimeRef() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMapOfRuntimesPNPMRuntimeName(t *testing.T) {
	node := func(ref string) RuntimeConfig {
		return RuntimeConfig{Kind: RuntimeKindNode, Node: &RuntimeConfigNode{NodeVersion: "26.2.0", PNPMRuntime: ref}}
	}
	runtimes := MapOfRuntimes{
		"pnpm-b": testPNPMRuntime(),
		"pnpm-a": testPNPMRuntime(),
		"uv":     {Kind: RuntimeKindUV},
	}

	tests := []struct {
		name   string
		m      MapOfRuntimes
		rc     RuntimeConfig
		want   string
		wantOK bool
	}{
		{"explicit reference", runtimes, node("pnpm-b"), "pnpm-b", true},
		{"bun explicit reference", runtimes, RuntimeConfig{Kind: RuntimeKindBun, Bun: &RuntimeConfigBun{PNPMRuntime: "pnpm-a"}}, "pnpm-a", true},
		// A dangling reference never falls back to another pnpm runtime.
		{"dangling reference", runtimes, node("pnpm-c"), "", false},
		{"reference to another kind", runtimes, node("uv"), "", false},
		{"empty reference takes the first pnpm runtime by name", runtimes, node(""), "pnpm-a", true},
		{"empty reference with no pnpm runtime", MapOfRuntimes{"uv": {Kind: RuntimeKindUV}}, node(""), "", false},
		{"uv runtime", runtimes, RuntimeConfig{Kind: RuntimeKindUV}, "", false},
		{"pnpm runtime itself", runtimes, testPNPMRuntime(), "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.m.PNPMRuntimeName(tt.rc)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("PNPMRuntimeName() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestDefaultConfigPNPMRuntime checks the embedded default config the way it is
// evaluated at runtime: one pnpm runtime, hash-pinned per platform, that the
// node and bun runtimes both name.
func TestDefaultConfigPNPMRuntime(t *testing.T) {
	cfg := runDefaultConfigForTest(t)

	rt, ok := cfg.Runtimes["pnpm"]
	if !ok {
		t.Fatal(`runtimes["pnpm"] missing from default config`)
	}
	if rt.Kind != RuntimeKindPNPM || rt.Mode != RuntimeModeManaged {
		t.Errorf("pnpm runtime = kind %q mode %q, want managed pnpm", rt.Kind, rt.Mode)
	}
	if rt.PNPM == nil || rt.PNPM.PNPMVersion == "" {
		t.Fatal("pnpm runtime missing pnpmVersion")
	}
	if rt.Managed == nil {
		t.Fatal("pnpm runtime missing managed binaries")
	}
	entries := 0
	for osType, byArch := range rt.Managed.Binaries {
		for arch, byLibc := range byArch {
			for libc, info := range byLibc {
				entries++
				if !isValidSHA256Hex(info.Hash) {
					t.Errorf("%s/%s/%s: hash %q is not a SHA-256", osType, arch, libc, info.Hash)
				}
				if !strings.HasPrefix(info.URL, "https://github.com/pnpm/pnpm/releases/download/v"+rt.PNPM.PNPMVersion+"/") {
					t.Errorf("%s/%s/%s: url %q is not the pinned pnpm release", osType, arch, libc, info.URL)
				}
			}
		}
	}
	if entries != 8 {
		t.Errorf("pnpm runtime has %d platform entries, want 8", entries)
	}

	for _, name := range []string{"node", "bun"} {
		if ref := cfg.Runtimes[name].PNPMRuntimeRef(); ref != "pnpm" {
			t.Errorf("%s runtime pnpmRuntime = %q, want %q", name, ref, "pnpm")
		}
	}
	if err := ValidateRuntimes(cfg.Runtimes); err != nil {
		t.Errorf("default runtimes fail validation: %v", err)
	}
}

func TestRuntimeConfig_PNPMField_JSONRoundTrip(t *testing.T) {
	original := testPNPMRuntime()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	if !strings.Contains(string(data), `"kind":"pnpm"`) || !strings.Contains(string(data), `"pnpm":{"pnpmVersion":"12.4.1"}`) {
		t.Errorf("JSON should carry kind and pnpm.pnpmVersion, got: %s", data)
	}

	var decoded RuntimeConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if decoded.Kind != RuntimeKindPNPM || decoded.PNPM == nil || decoded.PNPM.PNPMVersion != "12.4.1" {
		t.Fatalf("decoded runtime = %+v, want pnpm 12.4.1", decoded)
	}
	entry, ok := decoded.Managed.Binaries[syslist.OsTypeWindows][syslist.ArchTypeAmd64]["unknown"]
	if !ok || entry.BinaryPath == nil || *entry.BinaryPath != "pnpm.exe" || !entry.ExtractDir {
		t.Errorf("decoded windows entry = %+v, want pnpm.exe extracted whole", entry)
	}
	if err := ValidateRuntimes(MapOfRuntimes{"pnpm": decoded}); err != nil {
		t.Errorf("round-tripped pnpm runtime fails validation: %v", err)
	}
}
