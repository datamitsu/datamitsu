package config

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func validBunRuntime() RuntimeConfig {
	return RuntimeConfig{
		Managed: nil,
		Node:    nil,
		UV:      nil,
		JVM:     nil,
		Go:      nil,
		PNPM:    nil,
		Kind:    RuntimeKindBun,
		Mode:    RuntimeModeSystem,
		System:  &RuntimeConfigSystem{SystemVersion: "", Command: "bun"},
		Bun: &RuntimeConfigBun{
			BunVersion:  "1.4.1",
			PNPMRuntime: "pnpm",
		},
	}
}

func TestValidateBunApp(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Binary:             nil,
			Uv:                 nil,
			Node:               nil,
			Jvm:                nil,
			Go:                 nil,
			Shell:              nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Bun: &binmanager.AppConfigBun{
				Dependencies: nil,
				PackageName:  "eslint",
				Version:      "10.9.0",
				BinPath:      "node_modules/eslint/bin/eslint.js",
				LockFile:     "lock",
				Runtime:      "bun",
			},
		},
	}
	if _, err := ValidateApps(apps, MapOfRuntimes{"bun": validBunRuntime()}); err != nil {
		t.Fatalf("ValidateApps() error = %v", err)
	}
}

func TestValidateBunAppRequiresLockfileAndSafeEntrypoint(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Binary:             nil,
			Uv:                 nil,
			Node:               nil,
			Jvm:                nil,
			Go:                 nil,
			Shell:              nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Bun: &binmanager.AppConfigBun{
				LockFile:     "",
				Dependencies: nil,
				PackageName:  "eslint",
				Version:      "10.9.0",
				BinPath:      "../eslint.js",
				Runtime:      "bun",
			},
		},
	}
	_, err := ValidateApps(apps, MapOfRuntimes{"bun": validBunRuntime()})
	if err == nil {
		t.Fatal("ValidateApps() error = nil")
	}
	for _, want := range []string{"escapes parent directory", "lockFile is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestValidateBunAppRejectsPackageBinShim(t *testing.T) {
	apps := binmanager.MapOfApps{
		"cowsay": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Binary:             nil,
			Uv:                 nil,
			Node:               nil,
			Jvm:                nil,
			Go:                 nil,
			Shell:              nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Bun: &binmanager.AppConfigBun{
				Dependencies: nil,
				PackageName:  "cowsay",
				Version:      "1.6.0",
				BinPath:      "node_modules/.bin/cowsay",
				LockFile:     "lock",
				Runtime:      "bun",
			},
		},
	}
	_, err := ValidateApps(apps, MapOfRuntimes{"bun": validBunRuntime()})
	if err == nil || !strings.Contains(err.Error(), "JavaScript entrypoint") {
		t.Fatalf("ValidateApps() error = %v, want node_modules/.bin rejection", err)
	}
}

func TestValidateBunAppRejectsRuntimeKindMismatch(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required:           false,
			Lazy:               false,
			Description:        "",
			VersionCheck:       nil,
			DependsOn:          nil,
			OfficialURL:        "",
			OfficialURLDerived: false,
			Binary:             nil,
			Uv:                 nil,
			Node:               nil,
			Jvm:                nil,
			Go:                 nil,
			Shell:              nil,
			Env:                nil,
			RuntimeEnv:         nil,
			Caches:             nil,
			Files:              nil,
			Links:              nil,
			Archives:           nil,
			Prepare:            nil,
			Bun: &binmanager.AppConfigBun{
				Dependencies: nil,
				PackageName:  "eslint",
				Version:      "10.9.0",
				BinPath:      "node_modules/eslint/bin/eslint.js",
				LockFile:     "lock",
				Runtime:      "node",
			},
		},
	}
	runtimes := MapOfRuntimes{
		"node": {Managed: nil, Bun: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: RuntimeKindNode, Mode: RuntimeModeSystem, System: &RuntimeConfigSystem{SystemVersion: "", Command: "node"}, Node: &RuntimeConfigNode{NodeVersion: "26", PNPMRuntime: "pnpm"}},
	}
	_, err := ValidateApps(apps, runtimes)
	if err == nil || !strings.Contains(err.Error(), `expected "bun"`) {
		t.Fatalf("ValidateApps() error = %v", err)
	}
}
