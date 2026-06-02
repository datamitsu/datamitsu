package runtimemanager

import (
	"runtime"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
)

var testLibc = string(target.DetectHost().Libc)

func makeTestManagedRuntime(url, hash string) config.RuntimeConfig {
	return config.RuntimeConfig{
		Kind: config.RuntimeKindUV,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: binmanager.MapOfBinaries{
				syslist.OsTypeDarwin: {
					syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{
						URL:         url,
						Hash:        hash,
						ContentType: binmanager.BinContentTypeTarGz,
					}},
					syslist.ArchTypeArm64: {"unknown": binmanager.BinaryOsArchInfo{
						URL:         url + "-arm64",
						Hash:        hash,
						ContentType: binmanager.BinContentTypeTarGz,
					}},
				},
				syslist.OsTypeLinux: {
					syslist.ArchTypeAmd64: {testLibc: binmanager.BinaryOsArchInfo{
						URL:         url + "-linux",
						Hash:        hash,
						ContentType: binmanager.BinContentTypeTarGz,
					}},
				},
			},
		},
	}
}

func TestCalculateRuntimeHash(t *testing.T) {
	t.Run("basic hash", func(t *testing.T) {
		rc := makeTestManagedRuntime("https://example.com/uv.tar.gz", "abc123")
		hash, err := calculateRuntimeHash(rc, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		if err != nil {
			t.Fatalf("calculateRuntimeHash() error = %v", err)
		}
		if hash == "" {
			t.Error("hash is empty")
		}
		if len(hash) != 32 {
			t.Errorf("hash length = %d, want 32 (xxh3-128)", len(hash))
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		rc := makeTestManagedRuntime("https://example.com/uv.tar.gz", "abc123")
		hash1, err := calculateRuntimeHash(rc, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		hash2, err := calculateRuntimeHash(rc, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}
		if hash1 != hash2 {
			t.Errorf("hash not deterministic: %q != %q", hash1, hash2)
		}
	})

	t.Run("different urls produce different hashes", func(t *testing.T) {
		rc1 := makeTestManagedRuntime("https://example.com/uv-v1.tar.gz", "abc123")
		rc2 := makeTestManagedRuntime("https://example.com/uv-v2.tar.gz", "abc123")

		hash1, _ := calculateRuntimeHash(rc1, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		hash2, _ := calculateRuntimeHash(rc2, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")

		if hash1 == hash2 {
			t.Error("different URLs produced same hash")
		}
	})

	t.Run("different os produce different hashes", func(t *testing.T) {
		rc := makeTestManagedRuntime("https://example.com/uv.tar.gz", "abc123")
		hash1, _ := calculateRuntimeHash(rc, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		hash2, _ := calculateRuntimeHash(rc, syslist.OsTypeLinux, syslist.ArchTypeAmd64, testLibc)

		if hash1 == hash2 {
			t.Error("different OS produced same hash")
		}
	})

	t.Run("non-managed runtime returns error", func(t *testing.T) {
		rc := config.RuntimeConfig{
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{
				Command: "uv",
			},
		}
		_, err := calculateRuntimeHash(rc, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		if err == nil {
			t.Error("expected error for non-managed runtime, got nil")
		}
	})

	t.Run("missing os returns error", func(t *testing.T) {
		rc := makeTestManagedRuntime("https://example.com/uv.tar.gz", "abc123")
		_, err := calculateRuntimeHash(rc, syslist.OsTypeWindows, syslist.ArchTypeAmd64, "unknown")
		if err == nil {
			t.Error("expected error for missing OS, got nil")
		}
	})

	t.Run("missing arch returns error", func(t *testing.T) {
		rc := makeTestManagedRuntime("https://example.com/uv.tar.gz", "abc123")
		_, err := calculateRuntimeHash(rc, syslist.OsTypeDarwin, syslist.ArchTypeRiscv64, "unknown")
		if err == nil {
			t.Error("expected error for missing arch, got nil")
		}
	})

	t.Run("with binary path", func(t *testing.T) {
		binaryPath := "uv"
		rc := config.RuntimeConfig{
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeManaged,
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					syslist.OsTypeDarwin: {
						syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/uv.tar.gz",
							Hash:        "abc123",
							ContentType: binmanager.BinContentTypeTarGz,
							BinaryPath:  &binaryPath,
						}},
					},
				},
			},
		}
		rcWithout := makeTestManagedRuntime("https://example.com/uv.tar.gz", "abc123")

		hashWith, err := calculateRuntimeHash(rc, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		if err != nil {
			t.Fatalf("hash with binaryPath error = %v", err)
		}
		hashWithout, err := calculateRuntimeHash(rcWithout, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		if err != nil {
			t.Fatalf("hash without binaryPath error = %v", err)
		}

		if hashWith == hashWithout {
			t.Error("binaryPath should affect hash")
		}
	})

	t.Run("Node node version affects runtime hash", func(t *testing.T) {
		rc1 := makeTestManagedRuntime("https://example.com/node.tar.gz", "node123")
		rc1.Kind = config.RuntimeKindNode
		rc1.Node = &config.RuntimeConfigNode{NodeVersion: "22.14.0", PNPMVersion: "10.7.0", PNPMHash: "pnpmhash1"}

		rc2 := makeTestManagedRuntime("https://example.com/node.tar.gz", "node123")
		rc2.Kind = config.RuntimeKindNode
		rc2.Node = &config.RuntimeConfigNode{NodeVersion: "20.11.1", PNPMVersion: "10.7.0", PNPMHash: "pnpmhash1"}

		hash1, _ := calculateRuntimeHash(rc1, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		hash2, _ := calculateRuntimeHash(rc2, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")

		if hash1 == hash2 {
			t.Error("different node versions should produce different runtime hashes")
		}
	})

	t.Run("Node pnpm version affects runtime hash", func(t *testing.T) {
		rc1 := makeTestManagedRuntime("https://example.com/node.tar.gz", "node123")
		rc1.Kind = config.RuntimeKindNode
		rc1.Node = &config.RuntimeConfigNode{NodeVersion: "22.14.0", PNPMVersion: "10.7.0", PNPMHash: "pnpmhash1"}

		rc2 := makeTestManagedRuntime("https://example.com/node.tar.gz", "node123")
		rc2.Kind = config.RuntimeKindNode
		rc2.Node = &config.RuntimeConfigNode{NodeVersion: "22.14.0", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash1"}

		hash1, _ := calculateRuntimeHash(rc1, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		hash2, _ := calculateRuntimeHash(rc2, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")

		if hash1 == hash2 {
			t.Error("different pnpm versions should produce different runtime hashes")
		}
	})

	t.Run("Node pnpm hash affects runtime hash", func(t *testing.T) {
		rc1 := makeTestManagedRuntime("https://example.com/node.tar.gz", "node123")
		rc1.Kind = config.RuntimeKindNode
		rc1.Node = &config.RuntimeConfigNode{NodeVersion: "22.14.0", PNPMVersion: "10.7.0", PNPMHash: "pnpmhash1"}

		rc2 := makeTestManagedRuntime("https://example.com/node.tar.gz", "node123")
		rc2.Kind = config.RuntimeKindNode
		rc2.Node = &config.RuntimeConfigNode{NodeVersion: "22.14.0", PNPMVersion: "10.7.0", PNPMHash: "pnpmhash2"}

		hash1, _ := calculateRuntimeHash(rc1, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		hash2, _ := calculateRuntimeHash(rc2, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")

		if hash1 == hash2 {
			t.Error("different pnpm hashes should produce different runtime hashes")
		}
	})

	t.Run("UV python version affects runtime hash", func(t *testing.T) {
		rc1 := makeTestManagedRuntime("https://example.com/uv.tar.gz", "abc123")
		rc1.Kind = config.RuntimeKindUV
		rc1.UV = &config.RuntimeConfigUV{PythonVersion: "3.12"}

		rc2 := makeTestManagedRuntime("https://example.com/uv.tar.gz", "abc123")
		rc2.Kind = config.RuntimeKindUV
		rc2.UV = &config.RuntimeConfigUV{PythonVersion: "3.11"}

		hash1, _ := calculateRuntimeHash(rc1, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		hash2, _ := calculateRuntimeHash(rc2, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")

		if hash1 == hash2 {
			t.Error("different python versions should produce different runtime hashes")
		}
	})
}

// TestRuntimeHashFoldsSameFieldsForEveryKind guards the lock-step contract the
// RuntimeKind registry exists to enforce: for every kind, each cache-affecting
// version field must be folded into BOTH the managed-mode hash
// (calculateRuntimeHash) and the system-mode hash (calculateSystemRuntimeHash).
// If a future field is added to one hasher but not the other (the historical
// drift risk), the corresponding sub-test changes only one hash and fails.
func TestRuntimeHashFoldsSameFieldsForEveryKind(t *testing.T) {
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("detect os type: %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("detect arch type: %v", err)
	}

	// Key the binaries by the host os/arch so calculateRuntimeHash resolves on any
	// platform the suite runs on (makeTestManagedRuntime is hard-keyed to
	// darwin/linux-amd64 and would miss e.g. linux/arm64).
	sharedBin := binmanager.BinaryOsArchInfo{
		URL:         "https://example.com/runtime.tar.gz",
		Hash:        "rt123",
		ContentType: binmanager.BinContentTypeTarGz,
	}
	managedBase := func(kind config.RuntimeKind) config.RuntimeConfig {
		return config.RuntimeConfig{
			Kind: kind,
			Mode: config.RuntimeModeManaged,
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{osType: {archType: {testLibc: sharedBin}}},
			},
		}
	}
	systemBase := func(kind config.RuntimeKind) config.RuntimeConfig {
		return config.RuntimeConfig{
			Kind:   kind,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/" + string(kind)},
		}
	}

	// Each field case applies the "a"/"b" variant to a runtime config so we can
	// assert the change propagates into both hash functions. The setter is kind-aware and
	// initializes the typed sub-config.
	type fieldCase struct {
		field string
		set   func(rc *config.RuntimeConfig, v string)
	}
	kindCases := []struct {
		kind   config.RuntimeKind
		fields []fieldCase
	}{
		{
			kind: config.RuntimeKindUV,
			fields: []fieldCase{
				{"pythonVersion", func(rc *config.RuntimeConfig, v string) { rc.UV = &config.RuntimeConfigUV{PythonVersion: v} }},
			},
		},
		{
			kind: config.RuntimeKindNode,
			fields: []fieldCase{
				{"nodeVersion", func(rc *config.RuntimeConfig, v string) {
					rc.Node = &config.RuntimeConfigNode{NodeVersion: v, PNPMVersion: "10.0.0", PNPMHash: "h"}
				}},
				{"pnpmVersion", func(rc *config.RuntimeConfig, v string) {
					rc.Node = &config.RuntimeConfigNode{NodeVersion: "22.0.0", PNPMVersion: v, PNPMHash: "h"}
				}},
				{"pnpmHash", func(rc *config.RuntimeConfig, v string) {
					rc.Node = &config.RuntimeConfigNode{NodeVersion: "22.0.0", PNPMVersion: "10.0.0", PNPMHash: v}
				}},
			},
		},
		{
			kind: config.RuntimeKindJVM,
			fields: []fieldCase{
				{"javaVersion", func(rc *config.RuntimeConfig, v string) { rc.JVM = &config.RuntimeConfigJVM{JavaVersion: v} }},
			},
		},
		{
			kind: config.RuntimeKindGo,
			fields: []fieldCase{
				{"goVersion", func(rc *config.RuntimeConfig, v string) { rc.Go = &config.RuntimeConfigGo{GoVersion: v} }},
			},
		},
	}

	// Cross-check: the fields enumerated here must match the registry's HashFields
	// count for each kind, so this test can't silently fall out of sync if a new
	// field is added to a kind's HashFields.
	for _, kc := range kindCases {
		info, ok := config.LookupRuntimeKind(kc.kind)
		if !ok {
			t.Fatalf("kind %q missing from registry", kc.kind)
		}
		rc := managedBase(kc.kind)
		for _, fc := range kc.fields {
			fc.set(&rc, "x")
		}
		if got := len(info.HashFields(rc)); got != len(kc.fields) {
			t.Fatalf("kind %q: registry folds %d fields but test enumerates %d; update the test", kc.kind, got, len(kc.fields))
		}
	}

	for _, kc := range kindCases {
		for _, fc := range kc.fields {
			t.Run(string(kc.kind)+"/"+fc.field, func(t *testing.T) {
				mA := managedBase(kc.kind)
				mB := managedBase(kc.kind)
				fc.set(&mA, "version-a")
				fc.set(&mB, "version-b")
				mHashA, err := calculateRuntimeHash(mA, osType, archType, testLibc)
				if err != nil {
					t.Fatalf("managed hash A: %v", err)
				}
				mHashB, err := calculateRuntimeHash(mB, osType, archType, testLibc)
				if err != nil {
					t.Fatalf("managed hash B: %v", err)
				}
				if mHashA == mHashB {
					t.Errorf("managed hash does not fold %s for kind %s", fc.field, kc.kind)
				}

				sA := systemBase(kc.kind)
				sB := systemBase(kc.kind)
				fc.set(&sA, "version-a")
				fc.set(&sB, "version-b")
				sHashA := calculateSystemRuntimeHash(sA)
				sHashB := calculateSystemRuntimeHash(sB)
				if sHashA == sHashB {
					t.Errorf("system hash does not fold %s for kind %s", fc.field, kc.kind)
				}
			})
		}
	}
}

func TestCalculateAppHash(t *testing.T) {
	t.Run("basic hash", func(t *testing.T) {
		hash := calculateAppHash("yamllint", "1.37.0", nil, "runtimehash123", "", "")
		if hash == "" {
			t.Error("hash is empty")
		}
		if len(hash) != 32 {
			t.Errorf("hash length = %d, want 32 (xxh3-128)", len(hash))
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		hash1 := calculateAppHash("yamllint", "1.37.0", nil, "runtimehash123", "", "")
		hash2 := calculateAppHash("yamllint", "1.37.0", nil, "runtimehash123", "", "")

		if hash1 != hash2 {
			t.Errorf("hash not deterministic: %q != %q", hash1, hash2)
		}
	})

	t.Run("different app names produce different hashes", func(t *testing.T) {
		hash1 := calculateAppHash("yamllint", "1.0.0", nil, "rt1", "", "")
		hash2 := calculateAppHash("ruff", "1.0.0", nil, "rt1", "", "")

		if hash1 == hash2 {
			t.Error("different app names produced same hash")
		}
	})

	t.Run("different versions produce different hashes", func(t *testing.T) {
		hash1 := calculateAppHash("yamllint", "1.37.0", nil, "rt1", "", "")
		hash2 := calculateAppHash("yamllint", "1.38.0", nil, "rt1", "", "")

		if hash1 == hash2 {
			t.Error("different versions produced same hash")
		}
	})

	t.Run("different runtime hashes produce different hashes", func(t *testing.T) {
		hash1 := calculateAppHash("yamllint", "1.37.0", nil, "rt1", "", "")
		hash2 := calculateAppHash("yamllint", "1.37.0", nil, "rt2", "", "")

		if hash1 == hash2 {
			t.Error("different runtime hashes produced same hash")
		}
	})

	t.Run("dependencies affect hash", func(t *testing.T) {
		deps := map[string]string{"plugin-a": "1.0.0"}
		hash1 := calculateAppHash("eslint", "9.0.0", nil, "rt1", "", "")
		hash2 := calculateAppHash("eslint", "9.0.0", deps, "rt1", "", "")

		if hash1 == hash2 {
			t.Error("deps should affect hash")
		}
	})

	t.Run("dependency order does not affect hash", func(t *testing.T) {
		deps1 := map[string]string{"a": "1.0", "b": "2.0", "c": "3.0"}
		deps2 := map[string]string{"c": "3.0", "a": "1.0", "b": "2.0"}

		hash1 := calculateAppHash("eslint", "9.0.0", deps1, "rt1", "", "")
		hash2 := calculateAppHash("eslint", "9.0.0", deps2, "rt1", "", "")

		if hash1 != hash2 {
			t.Error("dependency order should not affect hash")
		}
	})

	t.Run("different dependency versions produce different hashes", func(t *testing.T) {
		deps1 := map[string]string{"plugin-a": "1.0.0"}
		deps2 := map[string]string{"plugin-a": "2.0.0"}

		hash1 := calculateAppHash("eslint", "9.0.0", deps1, "rt1", "", "")
		hash2 := calculateAppHash("eslint", "9.0.0", deps2, "rt1", "", "")

		if hash1 == hash2 {
			t.Error("different dep versions produced same hash")
		}
	})

	t.Run("lock file hash affects hash", func(t *testing.T) {
		hash1 := calculateAppHash("yamllint", "1.37.0", nil, "rt1", "", "")
		hash2 := calculateAppHash("yamllint", "1.37.0", nil, "rt1", "abc123lockhash", "")

		if hash1 == hash2 {
			t.Error("lock file hash should affect hash")
		}
	})

	t.Run("different lock file hash values produce different hashes", func(t *testing.T) {
		hash1 := calculateAppHash("yamllint", "1.37.0", nil, "rt1", "lockhash1", "")
		hash2 := calculateAppHash("yamllint", "1.37.0", nil, "rt1", "lockhash2", "")

		if hash1 == hash2 {
			t.Error("different lock file hash values should produce different hashes")
		}
	})
}

func TestCalculateNodeAppHash(t *testing.T) {
	t.Run("basic hash has xxh3-128 length", func(t *testing.T) {
		hash := calculateNodeAppHash("eslint", "eslint", "9.0.0", "node_modules/.bin/eslint", nil, "rthash", "", "")
		if hash == "" {
			t.Error("hash is empty")
		}
		if len(hash) != 32 {
			t.Errorf("hash length = %d, want 32 (xxh3-128)", len(hash))
		}
	})

	t.Run("stable / deterministic", func(t *testing.T) {
		hash1 := calculateNodeAppHash("eslint", "eslint", "9.0.0", "node_modules/.bin/eslint", nil, "rthash", "", "")
		hash2 := calculateNodeAppHash("eslint", "eslint", "9.0.0", "node_modules/.bin/eslint", nil, "rthash", "", "")
		if hash1 != hash2 {
			t.Errorf("hash not deterministic: %q != %q", hash1, hash2)
		}
	})

	t.Run("package name affects hash", func(t *testing.T) {
		hash1 := calculateNodeAppHash("myapp", "pkg-a", "1.0.0", "node_modules/.bin/myapp", nil, "rthash", "", "")
		hash2 := calculateNodeAppHash("myapp", "pkg-b", "1.0.0", "node_modules/.bin/myapp", nil, "rthash", "", "")
		if hash1 == hash2 {
			t.Error("different package names produced same hash")
		}
	})

	t.Run("runtime hash affects hash", func(t *testing.T) {
		hash1 := calculateNodeAppHash("eslint", "eslint", "9.0.0", "node_modules/.bin/eslint", nil, "rthash1", "", "")
		hash2 := calculateNodeAppHash("eslint", "eslint", "9.0.0", "node_modules/.bin/eslint", nil, "rthash2", "", "")
		if hash1 == hash2 {
			t.Error("different runtime hashes produced same hash")
		}
	})

	t.Run("differs from calculateAppHash (the uv/jvm app hasher) with same base inputs", func(t *testing.T) {
		nodeHash := calculateNodeAppHash("eslint", "eslint", "9.0.0", "node_modules/.bin/eslint", nil, "rthash", "", "")
		appHash := calculateAppHash("eslint", "9.0.0", nil, "rthash", "", "")
		if nodeHash == appHash {
			t.Error("node app hash should differ from plain app hash due to packageName and binPath inputs")
		}
	})
}

// TestCalculateRuntimeHashNodeDistinct verifies the managed node runtime hash is
// stable, sensitive to the node config fields (nodeVersion/pnpmVersion/pnpmHash),
// and distinct from jvm/uv runtimes that share the same binaries entry.
func TestCalculateRuntimeHashNodeDistinct(t *testing.T) {
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("detect os type: %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("detect arch type: %v", err)
	}

	sharedBin := binmanager.BinaryOsArchInfo{
		URL:         "https://example.com/runtime.tar.xz",
		Hash:        "abc123",
		ContentType: binmanager.BinContentTypeTarXz,
	}
	managed := func(kind config.RuntimeKind, mutate func(*config.RuntimeConfig)) config.RuntimeConfig {
		rc := config.RuntimeConfig{
			Kind: kind,
			Mode: config.RuntimeModeManaged,
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{osType: {archType: {testLibc: sharedBin}}},
			},
		}
		mutate(&rc)
		return rc
	}

	nodeRC := managed(config.RuntimeKindNode, func(rc *config.RuntimeConfig) {
		rc.Node = &config.RuntimeConfigNode{NodeVersion: "26.2.0", PNPMVersion: "11.0.0", PNPMHash: "pnpmhash"}
	})
	jvmRC := managed(config.RuntimeKindJVM, func(rc *config.RuntimeConfig) {
		rc.JVM = &config.RuntimeConfigJVM{JavaVersion: "21"}
	})
	uvRC := managed(config.RuntimeKindUV, func(rc *config.RuntimeConfig) {
		rc.UV = &config.RuntimeConfigUV{PythonVersion: "3.12"}
	})

	nodeHash, err := calculateRuntimeHash(nodeRC, osType, archType, testLibc)
	if err != nil {
		t.Fatalf("node runtime hash error: %v", err)
	}

	t.Run("stable", func(t *testing.T) {
		again, err := calculateRuntimeHash(nodeRC, osType, archType, testLibc)
		if err != nil {
			t.Fatalf("node runtime hash error: %v", err)
		}
		if nodeHash != again {
			t.Errorf("node runtime hash not stable: %q != %q", nodeHash, again)
		}
	})

	t.Run("distinct from jvm", func(t *testing.T) {
		jvmHash, err := calculateRuntimeHash(jvmRC, osType, archType, testLibc)
		if err != nil {
			t.Fatalf("jvm runtime hash error: %v", err)
		}
		if nodeHash == jvmHash {
			t.Error("node runtime hash should differ from jvm")
		}
	})

	t.Run("distinct from uv", func(t *testing.T) {
		uvHash, err := calculateRuntimeHash(uvRC, osType, archType, testLibc)
		if err != nil {
			t.Fatalf("uv runtime hash error: %v", err)
		}
		if nodeHash == uvHash {
			t.Error("node runtime hash should differ from uv")
		}
	})

	t.Run("node version affects hash", func(t *testing.T) {
		other := managed(config.RuntimeKindNode, func(rc *config.RuntimeConfig) {
			rc.Node = &config.RuntimeConfigNode{NodeVersion: "24.0.0", PNPMVersion: "11.0.0", PNPMHash: "pnpmhash"}
		})
		otherHash, err := calculateRuntimeHash(other, osType, archType, testLibc)
		if err != nil {
			t.Fatalf("node runtime hash error: %v", err)
		}
		if nodeHash == otherHash {
			t.Error("different node version should produce a different runtime hash")
		}
	})
}

func TestCalculateSystemRuntimeHash(t *testing.T) {
	t.Run("different commands produce different hashes", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/uv"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/local/bin/uv"},
		}

		hash1 := calculateSystemRuntimeHash(rc1)
		hash2 := calculateSystemRuntimeHash(rc2)

		if hash1 == hash2 {
			t.Error("different system commands should produce different hashes")
		}
	})

	t.Run("same command produces same hash", func(t *testing.T) {
		rc := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/uv"},
		}

		hash1 := calculateSystemRuntimeHash(rc)
		hash2 := calculateSystemRuntimeHash(rc)

		if hash1 != hash2 {
			t.Errorf("same command should produce same hash: %q != %q", hash1, hash2)
		}
	})

	t.Run("nil system config produces valid hash", func(t *testing.T) {
		rc := config.RuntimeConfig{
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeSystem,
		}

		hash := calculateSystemRuntimeHash(rc)
		if hash == "" {
			t.Error("hash should not be empty")
		}
		if len(hash) != 32 {
			t.Errorf("hash length = %d, want 32 (xxh3-128)", len(hash))
		}
	})

	t.Run("different Node nodeVersion produces different hash", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindNode,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/node"},
			Node:   &config.RuntimeConfigNode{NodeVersion: "20.11.1", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash1"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindNode,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/node"},
			Node:   &config.RuntimeConfigNode{NodeVersion: "22.0.0", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash1"},
		}

		hash1 := calculateSystemRuntimeHash(rc1)
		hash2 := calculateSystemRuntimeHash(rc2)
		if hash1 == hash2 {
			t.Error("different nodeVersion should produce different hashes")
		}
	})

	t.Run("different Node pnpmVersion produces different hash", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindNode,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/node"},
			Node:   &config.RuntimeConfigNode{NodeVersion: "20.11.1", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash1"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindNode,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/node"},
			Node:   &config.RuntimeConfigNode{NodeVersion: "20.11.1", PNPMVersion: "10.0.0", PNPMHash: "pnpmhash1"},
		}

		hash1 := calculateSystemRuntimeHash(rc1)
		hash2 := calculateSystemRuntimeHash(rc2)
		if hash1 == hash2 {
			t.Error("different pnpmVersion should produce different hashes")
		}
	})

	t.Run("different Node pnpmHash produces different hash", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindNode,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/node"},
			Node:   &config.RuntimeConfigNode{NodeVersion: "20.11.1", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash1"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindNode,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/node"},
			Node:   &config.RuntimeConfigNode{NodeVersion: "20.11.1", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash2"},
		}

		hash1 := calculateSystemRuntimeHash(rc1)
		hash2 := calculateSystemRuntimeHash(rc2)
		if hash1 == hash2 {
			t.Error("different pnpmHash should produce different hashes")
		}
	})

	t.Run("different UV pythonVersion produces different hash", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/uv"},
			UV:     &config.RuntimeConfigUV{PythonVersion: "3.12"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/uv"},
			UV:     &config.RuntimeConfigUV{PythonVersion: "3.13"},
		}

		hash1 := calculateSystemRuntimeHash(rc1)
		hash2 := calculateSystemRuntimeHash(rc2)
		if hash1 == hash2 {
			t.Error("different pythonVersion should produce different hashes")
		}
	})
}

func TestCalculateRuntimeHash_JVM(t *testing.T) {
	t.Run("JVM javaVersion affects runtime hash", func(t *testing.T) {
		rc1 := makeTestManagedRuntime("https://example.com/jdk.tar.gz", "jdk123")
		rc1.Kind = config.RuntimeKindJVM
		rc1.JVM = &config.RuntimeConfigJVM{JavaVersion: "21"}

		rc2 := makeTestManagedRuntime("https://example.com/jdk.tar.gz", "jdk123")
		rc2.Kind = config.RuntimeKindJVM
		rc2.JVM = &config.RuntimeConfigJVM{JavaVersion: "17"}

		hash1, _ := calculateRuntimeHash(rc1, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		hash2, _ := calculateRuntimeHash(rc2, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")

		if hash1 == hash2 {
			t.Error("different java versions should produce different runtime hashes")
		}
	})

	t.Run("JVM runtime hash is deterministic", func(t *testing.T) {
		rc := makeTestManagedRuntime("https://example.com/jdk.tar.gz", "jdk123")
		rc.Kind = config.RuntimeKindJVM
		rc.JVM = &config.RuntimeConfigJVM{JavaVersion: "21"}

		hash1, err := calculateRuntimeHash(rc, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		hash2, err := calculateRuntimeHash(rc, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}
		if hash1 != hash2 {
			t.Errorf("hash not deterministic: %q != %q", hash1, hash2)
		}
	})
}

func TestCalculateSystemRuntimeHash_JVM(t *testing.T) {
	t.Run("different JVM javaVersion produces different hash", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindJVM,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/java"},
			JVM:    &config.RuntimeConfigJVM{JavaVersion: "21"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindJVM,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/java"},
			JVM:    &config.RuntimeConfigJVM{JavaVersion: "17"},
		}

		hash1 := calculateSystemRuntimeHash(rc1)
		hash2 := calculateSystemRuntimeHash(rc2)
		if hash1 == hash2 {
			t.Error("different javaVersion should produce different hashes")
		}
	})
}

func TestCalculateRuntimeHash_Go(t *testing.T) {
	t.Run("Go goVersion affects runtime hash", func(t *testing.T) {
		rc1 := makeTestManagedRuntime("https://example.com/go.tar.gz", "go123")
		rc1.Kind = config.RuntimeKindGo
		rc1.Go = &config.RuntimeConfigGo{GoVersion: "1.22.0"}

		rc2 := makeTestManagedRuntime("https://example.com/go.tar.gz", "go123")
		rc2.Kind = config.RuntimeKindGo
		rc2.Go = &config.RuntimeConfigGo{GoVersion: "1.21.0"}

		hash1, _ := calculateRuntimeHash(rc1, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		hash2, _ := calculateRuntimeHash(rc2, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")

		if hash1 == hash2 {
			t.Error("different go versions should produce different runtime hashes")
		}
	})

	t.Run("Go runtime hash is deterministic", func(t *testing.T) {
		rc := makeTestManagedRuntime("https://example.com/go.tar.gz", "go123")
		rc.Kind = config.RuntimeKindGo
		rc.Go = &config.RuntimeConfigGo{GoVersion: "1.22.0"}

		hash1, err := calculateRuntimeHash(rc, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		hash2, err := calculateRuntimeHash(rc, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}
		if hash1 != hash2 {
			t.Errorf("hash not deterministic: %q != %q", hash1, hash2)
		}
	})
}

func TestCalculateSystemRuntimeHash_Go(t *testing.T) {
	t.Run("different Go goVersion produces different hash", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindGo,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/go"},
			Go:     &config.RuntimeConfigGo{GoVersion: "1.22.0"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindGo,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/go"},
			Go:     &config.RuntimeConfigGo{GoVersion: "1.21.0"},
		}

		hash1 := calculateSystemRuntimeHash(rc1)
		hash2 := calculateSystemRuntimeHash(rc2)
		if hash1 == hash2 {
			t.Error("different goVersion should produce different hashes")
		}
	})

	t.Run("same Go goVersion produces same hash", func(t *testing.T) {
		rc := config.RuntimeConfig{
			Kind:   config.RuntimeKindGo,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/go"},
			Go:     &config.RuntimeConfigGo{GoVersion: "1.22.0"},
		}

		hash1 := calculateSystemRuntimeHash(rc)
		hash2 := calculateSystemRuntimeHash(rc)
		if hash1 != hash2 {
			t.Errorf("same goVersion should produce same hash: %q != %q", hash1, hash2)
		}
	})
}

func TestCalculateAppHash_Go(t *testing.T) {
	t.Run("Go app package params produce valid hash", func(t *testing.T) {
		hash := calculateAppHash("govulncheck", "v1.1.4", nil, "rthash", "lockhash", "")
		if hash == "" {
			t.Error("hash is empty")
		}
		if len(hash) != 32 {
			t.Errorf("hash length = %d, want 32 (xxh3-128)", len(hash))
		}
	})

	t.Run("Go lockfile hash affects app hash", func(t *testing.T) {
		hash1 := calculateAppHash("govulncheck", "v1.1.4", nil, "rthash", "lockhash1", "")
		hash2 := calculateAppHash("govulncheck", "v1.1.4", nil, "rthash", "lockhash2", "")
		if hash1 == hash2 {
			t.Error("different lockfile hashes should produce different app hashes")
		}
	})

	t.Run("Go version affects app hash", func(t *testing.T) {
		hash1 := calculateAppHash("govulncheck", "v1.1.4", nil, "rthash", "lockhash", "")
		hash2 := calculateAppHash("govulncheck", "v1.1.3", nil, "rthash", "lockhash", "")
		if hash1 == hash2 {
			t.Error("different versions should produce different app hashes")
		}
	})
}

func TestCalculateSystemRuntimeHash_SystemVersion(t *testing.T) {
	t.Run("different systemVersion produces different hash", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/uv", SystemVersion: "1.0"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/uv", SystemVersion: "2.0"},
		}

		hash1 := calculateSystemRuntimeHash(rc1)
		hash2 := calculateSystemRuntimeHash(rc2)
		if hash1 == hash2 {
			t.Error("different systemVersion should produce different hashes")
		}
	})

	t.Run("same systemVersion produces same hash", func(t *testing.T) {
		rc := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/uv", SystemVersion: "1.0"},
		}

		hash1 := calculateSystemRuntimeHash(rc)
		hash2 := calculateSystemRuntimeHash(rc)
		if hash1 != hash2 {
			t.Errorf("same systemVersion should produce same hash: %q != %q", hash1, hash2)
		}
	})

	t.Run("empty vs non-empty systemVersion produces different hash", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/uv"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/uv", SystemVersion: "3.12.0"},
		}

		hash1 := calculateSystemRuntimeHash(rc1)
		hash2 := calculateSystemRuntimeHash(rc2)
		if hash1 == hash2 {
			t.Error("empty vs non-empty systemVersion should produce different hashes")
		}
	})
}

func TestHashFilesAndArchives(t *testing.T) {
	t.Run("empty returns empty string", func(t *testing.T) {
		result := binmanager.HashFilesAndArchives(nil, nil)
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("files only", func(t *testing.T) {
		files := map[string]string{"config.js": "content"}
		result := binmanager.HashFilesAndArchives(files, nil)
		if result == "" {
			t.Error("expected non-empty hash")
		}
		if len(result) != 32 {
			t.Errorf("hash length = %d, want 32", len(result))
		}
	})

	t.Run("archives only - inline", func(t *testing.T) {
		archives := map[string]*binmanager.ArchiveSpec{
			"dist": {Inline: "tar.br:somedata"},
		}
		result := binmanager.HashFilesAndArchives(nil, archives)
		if result == "" {
			t.Error("expected non-empty hash")
		}
	})

	t.Run("archives only - external", func(t *testing.T) {
		archives := map[string]*binmanager.ArchiveSpec{
			"dist": {
				URL:    "https://example.com/archive.tar.gz",
				Hash:   "abc123",
				Format: binmanager.BinContentTypeTarGz,
			},
		}
		result := binmanager.HashFilesAndArchives(nil, archives)
		if result == "" {
			t.Error("expected non-empty hash")
		}
	})

	t.Run("different inline content produces different hash", func(t *testing.T) {
		archives1 := map[string]*binmanager.ArchiveSpec{
			"dist": {Inline: "tar.br:version1"},
		}
		archives2 := map[string]*binmanager.ArchiveSpec{
			"dist": {Inline: "tar.br:version2"},
		}
		hash1 := binmanager.HashFilesAndArchives(nil, archives1)
		hash2 := binmanager.HashFilesAndArchives(nil, archives2)
		if hash1 == hash2 {
			t.Error("different inline content should produce different hashes")
		}
	})

	t.Run("different external hash produces different hash", func(t *testing.T) {
		archives1 := map[string]*binmanager.ArchiveSpec{
			"dist": {URL: "https://example.com/a.tar.gz", Hash: "hash1", Format: binmanager.BinContentTypeTarGz},
		}
		archives2 := map[string]*binmanager.ArchiveSpec{
			"dist": {URL: "https://example.com/a.tar.gz", Hash: "hash2", Format: binmanager.BinContentTypeTarGz},
		}
		hash1 := binmanager.HashFilesAndArchives(nil, archives1)
		hash2 := binmanager.HashFilesAndArchives(nil, archives2)
		if hash1 == hash2 {
			t.Error("different external hashes should produce different cache keys")
		}
	})

	t.Run("files plus archives differs from files alone", func(t *testing.T) {
		files := map[string]string{"config.js": "content"}
		archives := map[string]*binmanager.ArchiveSpec{
			"dist": {Inline: "tar.br:somedata"},
		}
		hashFilesOnly := binmanager.HashFilesAndArchives(files, nil)
		hashBoth := binmanager.HashFilesAndArchives(files, archives)
		if hashFilesOnly == hashBoth {
			t.Error("adding archives should change the hash")
		}
	})

	t.Run("deterministic - archive order does not matter", func(t *testing.T) {
		archives := map[string]*binmanager.ArchiveSpec{
			"alpha": {Inline: "tar.br:aaa"},
			"beta":  {Inline: "tar.br:bbb"},
			"gamma": {Inline: "tar.br:ccc"},
		}
		hash1 := binmanager.HashFilesAndArchives(nil, archives)
		hash2 := binmanager.HashFilesAndArchives(nil, archives)
		if hash1 != hash2 {
			t.Errorf("hash not deterministic: %q != %q", hash1, hash2)
		}
	})

	t.Run("different archive names produce different hash", func(t *testing.T) {
		archives1 := map[string]*binmanager.ArchiveSpec{
			"config": {Inline: "tar.br:data"},
		}
		archives2 := map[string]*binmanager.ArchiveSpec{
			"dist": {Inline: "tar.br:data"},
		}
		hash1 := binmanager.HashFilesAndArchives(nil, archives1)
		hash2 := binmanager.HashFilesAndArchives(nil, archives2)
		if hash1 == hash2 {
			t.Error("different archive names should produce different hashes")
		}
	})
}

func TestLockFileHash(t *testing.T) {
	t.Run("returns empty when lockFile is empty", func(t *testing.T) {
		result := lockFileHash("")
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("returns xxh3-128 of lockFile content", func(t *testing.T) {
		lockContent := "lockfile: content here"
		expected := hashutil.XXH3Hex([]byte(lockContent))

		result := lockFileHash(lockContent)
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("different lockFile contents produce different hashes", func(t *testing.T) {
		result1 := lockFileHash("content-v1")
		result2 := lockFileHash("content-v2")
		if result1 == result2 {
			t.Error("different lockFile contents should produce different hashes")
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		result1 := lockFileHash("same content")
		result2 := lockFileHash("same content")
		if result1 != result2 {
			t.Errorf("not deterministic: %q != %q", result1, result2)
		}
	})
}
