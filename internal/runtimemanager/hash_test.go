package runtimemanager

import (
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

	t.Run("FNM node version affects runtime hash", func(t *testing.T) {
		rc1 := makeTestManagedRuntime("https://example.com/fnm.tar.gz", "fnm123")
		rc1.Kind = config.RuntimeKindFNM
		rc1.FNM = &config.RuntimeConfigFNM{NodeVersion: "22.14.0", PNPMVersion: "10.7.0", PNPMHash: "pnpmhash1"}

		rc2 := makeTestManagedRuntime("https://example.com/fnm.tar.gz", "fnm123")
		rc2.Kind = config.RuntimeKindFNM
		rc2.FNM = &config.RuntimeConfigFNM{NodeVersion: "20.11.1", PNPMVersion: "10.7.0", PNPMHash: "pnpmhash1"}

		hash1, _ := calculateRuntimeHash(rc1, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		hash2, _ := calculateRuntimeHash(rc2, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")

		if hash1 == hash2 {
			t.Error("different node versions should produce different runtime hashes")
		}
	})

	t.Run("FNM pnpm version affects runtime hash", func(t *testing.T) {
		rc1 := makeTestManagedRuntime("https://example.com/fnm.tar.gz", "fnm123")
		rc1.Kind = config.RuntimeKindFNM
		rc1.FNM = &config.RuntimeConfigFNM{NodeVersion: "22.14.0", PNPMVersion: "10.7.0", PNPMHash: "pnpmhash1"}

		rc2 := makeTestManagedRuntime("https://example.com/fnm.tar.gz", "fnm123")
		rc2.Kind = config.RuntimeKindFNM
		rc2.FNM = &config.RuntimeConfigFNM{NodeVersion: "22.14.0", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash1"}

		hash1, _ := calculateRuntimeHash(rc1, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
		hash2, _ := calculateRuntimeHash(rc2, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")

		if hash1 == hash2 {
			t.Error("different pnpm versions should produce different runtime hashes")
		}
	})

	t.Run("FNM pnpm hash affects runtime hash", func(t *testing.T) {
		rc1 := makeTestManagedRuntime("https://example.com/fnm.tar.gz", "fnm123")
		rc1.Kind = config.RuntimeKindFNM
		rc1.FNM = &config.RuntimeConfigFNM{NodeVersion: "22.14.0", PNPMVersion: "10.7.0", PNPMHash: "pnpmhash1"}

		rc2 := makeTestManagedRuntime("https://example.com/fnm.tar.gz", "fnm123")
		rc2.Kind = config.RuntimeKindFNM
		rc2.FNM = &config.RuntimeConfigFNM{NodeVersion: "22.14.0", PNPMVersion: "10.7.0", PNPMHash: "pnpmhash2"}

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

func TestCalculateFNMAppHash(t *testing.T) {
	t.Run("basic hash", func(t *testing.T) {
		hash := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", nil, "rthash", "", "")
		if hash == "" {
			t.Error("hash is empty")
		}
		if len(hash) != 32 {
			t.Errorf("hash length = %d, want 32 (xxh3-128)", len(hash))
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		hash1 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", nil, "rthash", "", "")
		hash2 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", nil, "rthash", "", "")

		if hash1 != hash2 {
			t.Errorf("hash not deterministic: %q != %q", hash1, hash2)
		}
	})

	t.Run("different app names produce different hashes", func(t *testing.T) {
		hash1 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "1.0.0", "node_modules/.bin/mmdc", nil, "rthash", "", "")
		hash2 := calculateFNMAppHash("slidev", "@slidev/cli", "1.0.0", "node_modules/.bin/slidev", nil, "rthash", "", "")

		if hash1 == hash2 {
			t.Error("different app names produced same hash")
		}
	})

	t.Run("different package names produce different hashes", func(t *testing.T) {
		hash1 := calculateFNMAppHash("myapp", "pkg-a", "1.0.0", "node_modules/.bin/myapp", nil, "rthash", "", "")
		hash2 := calculateFNMAppHash("myapp", "pkg-b", "1.0.0", "node_modules/.bin/myapp", nil, "rthash", "", "")

		if hash1 == hash2 {
			t.Error("different package names produced same hash")
		}
	})

	t.Run("different bin paths produce different hashes", func(t *testing.T) {
		hash1 := calculateFNMAppHash("myapp", "pkg-a", "1.0.0", "node_modules/.bin/myapp", nil, "rthash", "", "")
		hash2 := calculateFNMAppHash("myapp", "pkg-a", "1.0.0", "node_modules/.bin/other", nil, "rthash", "", "")

		if hash1 == hash2 {
			t.Error("different bin paths produced same hash")
		}
	})

	t.Run("different pkg versions produce different hashes", func(t *testing.T) {
		hash1 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.0", "node_modules/.bin/mmdc", nil, "rthash", "", "")
		hash2 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", nil, "rthash", "", "")

		if hash1 == hash2 {
			t.Error("different pkg versions produced same hash")
		}
	})

	t.Run("different runtime hashes produce different hashes", func(t *testing.T) {
		hash1 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", nil, "rthash1", "", "")
		hash2 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", nil, "rthash2", "", "")

		if hash1 == hash2 {
			t.Error("different runtime hashes produced same hash")
		}
	})

	t.Run("dependencies affect hash", func(t *testing.T) {
		deps := map[string]string{"@mermaid-js/mermaid-cli": "11.4.1"}
		hash1 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", nil, "rthash", "", "")
		hash2 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", deps, "rthash", "", "")

		if hash1 == hash2 {
			t.Error("deps should affect hash")
		}
	})

	t.Run("dependency order does not affect hash", func(t *testing.T) {
		deps1 := map[string]string{"a": "1.0", "b": "2.0", "c": "3.0"}
		deps2 := map[string]string{"c": "3.0", "a": "1.0", "b": "2.0"}

		hash1 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", deps1, "rthash", "", "")
		hash2 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", deps2, "rthash", "", "")

		if hash1 != hash2 {
			t.Error("dependency order should not affect hash")
		}
	})

	t.Run("lock file hash affects hash", func(t *testing.T) {
		hash1 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", nil, "rthash", "", "")
		hash2 := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", nil, "rthash", "lockhash123", "")

		if hash1 == hash2 {
			t.Error("lock file hash should affect hash")
		}
	})

	t.Run("differs from calculateAppHash with same base inputs", func(t *testing.T) {
		fnmHash := calculateFNMAppHash("mmdc", "@mermaid-js/mermaid-cli", "11.4.1", "node_modules/.bin/mmdc", nil, "rthash", "", "")
		appHash := calculateAppHash("mmdc", "11.4.1", nil, "rthash", "", "")

		if fnmHash == appHash {
			t.Error("FNM hash should differ from app hash due to packageName and binPath inputs")
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

	t.Run("different FNM nodeVersion produces different hash", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindFNM,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/fnm"},
			FNM:    &config.RuntimeConfigFNM{NodeVersion: "20.11.1", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash1"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindFNM,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/fnm"},
			FNM:    &config.RuntimeConfigFNM{NodeVersion: "22.0.0", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash1"},
		}

		hash1 := calculateSystemRuntimeHash(rc1)
		hash2 := calculateSystemRuntimeHash(rc2)
		if hash1 == hash2 {
			t.Error("different nodeVersion should produce different hashes")
		}
	})

	t.Run("different FNM pnpmVersion produces different hash", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindFNM,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/fnm"},
			FNM:    &config.RuntimeConfigFNM{NodeVersion: "20.11.1", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash1"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindFNM,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/fnm"},
			FNM:    &config.RuntimeConfigFNM{NodeVersion: "20.11.1", PNPMVersion: "10.0.0", PNPMHash: "pnpmhash1"},
		}

		hash1 := calculateSystemRuntimeHash(rc1)
		hash2 := calculateSystemRuntimeHash(rc2)
		if hash1 == hash2 {
			t.Error("different pnpmVersion should produce different hashes")
		}
	})

	t.Run("different FNM pnpmHash produces different hash", func(t *testing.T) {
		rc1 := config.RuntimeConfig{
			Kind:   config.RuntimeKindFNM,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/fnm"},
			FNM:    &config.RuntimeConfigFNM{NodeVersion: "20.11.1", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash1"},
		}
		rc2 := config.RuntimeConfig{
			Kind:   config.RuntimeKindFNM,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/fnm"},
			FNM:    &config.RuntimeConfigFNM{NodeVersion: "20.11.1", PNPMVersion: "9.15.0", PNPMHash: "pnpmhash2"},
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
