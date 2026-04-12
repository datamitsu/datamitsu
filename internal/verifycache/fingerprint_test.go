package verifycache

import (
	"testing"
)

func TestFingerprintBinary(t *testing.T) {
	t.Run("same inputs produce same output", func(t *testing.T) {
		fp1 := FingerprintBinary("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		fp2 := FingerprintBinary("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		if fp1 != fp2 {
			t.Errorf("same inputs produced different fingerprints: %q != %q", fp1, fp2)
		}
	})

	t.Run("changed URL produces different output", func(t *testing.T) {
		fp1 := FingerprintBinary("https://example.com/bin-v1", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		fp2 := FingerprintBinary("https://example.com/bin-v2", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		if fp1 == fp2 {
			t.Error("different URLs should produce different fingerprints")
		}
	})

	t.Run("changed hash produces different output", func(t *testing.T) {
		fp1 := FingerprintBinary("https://example.com/bin", "hash1", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		fp2 := FingerprintBinary("https://example.com/bin", "hash2", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		if fp1 == fp2 {
			t.Error("different hashes should produce different fingerprints")
		}
	})

	t.Run("changed hashType produces different output", func(t *testing.T) {
		fp1 := FingerprintBinary("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		fp2 := FingerprintBinary("https://example.com/bin", "sha256hash", "sha512", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		if fp1 == fp2 {
			t.Error("different hashTypes should produce different fingerprints")
		}
	})

	t.Run("changed extractDir produces different output", func(t *testing.T) {
		fp1 := FingerprintBinary("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		fp2 := FingerprintBinary("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", true, "linux", "amd64", "glibc")
		if fp1 == fp2 {
			t.Error("different extractDir should produce different fingerprints")
		}
	})

	t.Run("changed os produces different output", func(t *testing.T) {
		fp1 := FingerprintBinary("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		fp2 := FingerprintBinary("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", false, "darwin", "amd64", "unknown")
		if fp1 == fp2 {
			t.Error("different os should produce different fingerprints")
		}
	})

	t.Run("changed arch produces different output", func(t *testing.T) {
		fp1 := FingerprintBinary("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		fp2 := FingerprintBinary("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "arm64", "glibc")
		if fp1 == fp2 {
			t.Error("different arch should produce different fingerprints")
		}
	})

	t.Run("output is 32 hex chars (xxh3-128)", func(t *testing.T) {
		fp := FingerprintBinary("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		if len(fp) != 32 {
			t.Errorf("fingerprint length = %d, want 32", len(fp))
		}
	})
}

func TestFingerprintRuntime(t *testing.T) {
	t.Run("same inputs produce same output", func(t *testing.T) {
		fp1 := FingerprintRuntime("https://example.com/runtime", "sha256hash", "sha256", "application/gzip", "runtime", true, "linux", "amd64", "glibc")
		fp2 := FingerprintRuntime("https://example.com/runtime", "sha256hash", "sha256", "application/gzip", "runtime", true, "linux", "amd64", "glibc")
		if fp1 != fp2 {
			t.Errorf("same inputs produced different fingerprints: %q != %q", fp1, fp2)
		}
	})

	t.Run("changed URL produces different output", func(t *testing.T) {
		fp1 := FingerprintRuntime("https://example.com/runtime-v1", "sha256hash", "sha256", "application/gzip", "runtime", true, "linux", "amd64", "glibc")
		fp2 := FingerprintRuntime("https://example.com/runtime-v2", "sha256hash", "sha256", "application/gzip", "runtime", true, "linux", "amd64", "glibc")
		if fp1 == fp2 {
			t.Error("different URLs should produce different fingerprints")
		}
	})

	t.Run("different from binary fingerprint with same inputs", func(t *testing.T) {
		fpBin := FingerprintBinary("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		fpRt := FingerprintRuntime("https://example.com/bin", "sha256hash", "sha256", "application/gzip", "bin", false, "linux", "amd64", "glibc")
		if fpBin == fpRt {
			t.Error("binary and runtime fingerprints with same inputs should differ (different prefix)")
		}
	})

	t.Run("output is 32 hex chars (xxh3-128)", func(t *testing.T) {
		fp := FingerprintRuntime("https://example.com/runtime", "sha256hash", "sha256", "application/gzip", "runtime", true, "linux", "amd64", "glibc")
		if len(fp) != 32 {
			t.Errorf("fingerprint length = %d, want 32", len(fp))
		}
	})
}

func TestFingerprintRuntimeApp(t *testing.T) {
	t.Run("same inputs produce same output", func(t *testing.T) {
		fp1 := FingerprintRuntimeApp(`{"name":"yamllint","version":"1.0"}`, `{"pythonVersion":"3.12"}`, "", "", "linux", "amd64")
		fp2 := FingerprintRuntimeApp(`{"name":"yamllint","version":"1.0"}`, `{"pythonVersion":"3.12"}`, "", "", "linux", "amd64")
		if fp1 != fp2 {
			t.Errorf("same inputs produced different fingerprints: %q != %q", fp1, fp2)
		}
	})

	t.Run("changed app config produces different output", func(t *testing.T) {
		fp1 := FingerprintRuntimeApp(`{"name":"yamllint","version":"1.0"}`, `{"pythonVersion":"3.12"}`, "", "", "linux", "amd64")
		fp2 := FingerprintRuntimeApp(`{"name":"yamllint","version":"2.0"}`, `{"pythonVersion":"3.12"}`, "", "", "linux", "amd64")
		if fp1 == fp2 {
			t.Error("different app configs should produce different fingerprints")
		}
	})

	t.Run("changed runtime config produces different output", func(t *testing.T) {
		fp1 := FingerprintRuntimeApp(`{"name":"yamllint","version":"1.0"}`, `{"pythonVersion":"3.12"}`, "", "", "linux", "amd64")
		fp2 := FingerprintRuntimeApp(`{"name":"yamllint","version":"1.0"}`, `{"pythonVersion":"3.13"}`, "", "", "linux", "amd64")
		if fp1 == fp2 {
			t.Error("different runtime configs should produce different fingerprints")
		}
	})

	t.Run("changed files produces different output", func(t *testing.T) {
		fp1 := FingerprintRuntimeApp(`{"name":"app"}`, `{"rt":"1"}`, "", "", "linux", "amd64")
		fp2 := FingerprintRuntimeApp(`{"name":"app"}`, `{"rt":"1"}`, `{"config.js":"content"}`, "", "linux", "amd64")
		if fp1 == fp2 {
			t.Error("different files should produce different fingerprints")
		}
	})

	t.Run("changed archives produces different output", func(t *testing.T) {
		fp1 := FingerprintRuntimeApp(`{"name":"app"}`, `{"rt":"1"}`, "", "", "linux", "amd64")
		fp2 := FingerprintRuntimeApp(`{"name":"app"}`, `{"rt":"1"}`, "", `{"dist":{"inline":"tar.br:data"}}`, "linux", "amd64")
		if fp1 == fp2 {
			t.Error("different archives should produce different fingerprints")
		}
	})

	t.Run("changed os produces different output", func(t *testing.T) {
		fp1 := FingerprintRuntimeApp(`{"name":"app"}`, `{"rt":"1"}`, "", "", "linux", "amd64")
		fp2 := FingerprintRuntimeApp(`{"name":"app"}`, `{"rt":"1"}`, "", "", "darwin", "amd64")
		if fp1 == fp2 {
			t.Error("different os should produce different fingerprints")
		}
	})

	t.Run("changed arch produces different output", func(t *testing.T) {
		fp1 := FingerprintRuntimeApp(`{"name":"app"}`, `{"rt":"1"}`, "", "", "linux", "amd64")
		fp2 := FingerprintRuntimeApp(`{"name":"app"}`, `{"rt":"1"}`, "", "", "linux", "arm64")
		if fp1 == fp2 {
			t.Error("different arch should produce different fingerprints")
		}
	})

	t.Run("output is 32 hex chars (xxh3-128)", func(t *testing.T) {
		fp := FingerprintRuntimeApp(`{"name":"yamllint"}`, `{"pythonVersion":"3.12"}`, "", "", "linux", "amd64")
		if len(fp) != 32 {
			t.Errorf("fingerprint length = %d, want 32", len(fp))
		}
	})
}

func TestFingerprintVersionCheck(t *testing.T) {
	t.Run("same inputs produce same output", func(t *testing.T) {
		fp1 := FingerprintVersionCheck("1.0.0", "1.0.0", "linux", "amd64", "glibc")
		fp2 := FingerprintVersionCheck("1.0.0", "1.0.0", "linux", "amd64", "glibc")
		if fp1 != fp2 {
			t.Errorf("same inputs produced different fingerprints: %q != %q", fp1, fp2)
		}
	})

	t.Run("changed appVersion produces different output", func(t *testing.T) {
		fp1 := FingerprintVersionCheck("1.0.0", "1.0.0", "linux", "amd64", "glibc")
		fp2 := FingerprintVersionCheck("2.0.0", "1.0.0", "linux", "amd64", "glibc")
		if fp1 == fp2 {
			t.Error("different appVersions should produce different fingerprints")
		}
	})

	t.Run("changed expectedVersion produces different output", func(t *testing.T) {
		fp1 := FingerprintVersionCheck("1.0.0", "1.0.0", "linux", "amd64", "glibc")
		fp2 := FingerprintVersionCheck("1.0.0", "2.0.0", "linux", "amd64", "glibc")
		if fp1 == fp2 {
			t.Error("different expectedVersions should produce different fingerprints")
		}
	})

	t.Run("changed os produces different output", func(t *testing.T) {
		fp1 := FingerprintVersionCheck("1.0.0", "1.0.0", "linux", "amd64", "glibc")
		fp2 := FingerprintVersionCheck("1.0.0", "1.0.0", "darwin", "amd64", "unknown")
		if fp1 == fp2 {
			t.Error("different os should produce different fingerprints")
		}
	})

	t.Run("output is 32 hex chars (xxh3-128)", func(t *testing.T) {
		fp := FingerprintVersionCheck("1.0.0", "1.0.0", "linux", "amd64", "glibc")
		if len(fp) != 32 {
			t.Errorf("fingerprint length = %d, want 32", len(fp))
		}
	})
}

func TestBinaryEntryKey(t *testing.T) {
	t.Run("formats correctly", func(t *testing.T) {
		key := BinaryEntryKey("lefthook", "darwin", "arm64", "unknown")
		expected := "binary:lefthook:darwin:arm64:unknown"
		if key != expected {
			t.Errorf("BinaryEntryKey() = %q, want %q", key, expected)
		}
	})

	t.Run("different libc produces different key", func(t *testing.T) {
		k1 := BinaryEntryKey("lefthook", "linux", "amd64", "glibc")
		k2 := BinaryEntryKey("lefthook", "linux", "amd64", "musl")
		if k1 == k2 {
			t.Error("different libc should produce different entry keys")
		}
	})
}

func TestRuntimeEntryKey(t *testing.T) {
	t.Run("formats correctly", func(t *testing.T) {
		key := RuntimeEntryKey("uv", "linux", "amd64", "glibc")
		expected := "runtime:uv:linux:amd64:glibc"
		if key != expected {
			t.Errorf("RuntimeEntryKey() = %q, want %q", key, expected)
		}
	})
}

func TestRuntimeAppEntryKey(t *testing.T) {
	t.Run("formats correctly", func(t *testing.T) {
		key := RuntimeAppEntryKey("yamllint", "linux", "amd64")
		expected := "runtime-app:yamllint:linux:amd64"
		if key != expected {
			t.Errorf("RuntimeAppEntryKey() = %q, want %q", key, expected)
		}
	})
}

func TestVersionCheckEntryKey(t *testing.T) {
	t.Run("formats correctly", func(t *testing.T) {
		key := VersionCheckEntryKey("lefthook", "linux", "amd64")
		expected := "version-check:lefthook:linux:amd64"
		if key != expected {
			t.Errorf("VersionCheckEntryKey() = %q, want %q", key, expected)
		}
	})
}

func TestFingerprintBundle(t *testing.T) {
	t.Run("same inputs produce same output", func(t *testing.T) {
		fp1 := FingerprintBundle("1.0.0", `{"config.js":"content"}`, "")
		fp2 := FingerprintBundle("1.0.0", `{"config.js":"content"}`, "")
		if fp1 != fp2 {
			t.Errorf("same inputs produced different fingerprints: %q != %q", fp1, fp2)
		}
	})

	t.Run("changed version produces different output", func(t *testing.T) {
		fp1 := FingerprintBundle("1.0.0", `{"config.js":"content"}`, "")
		fp2 := FingerprintBundle("2.0.0", `{"config.js":"content"}`, "")
		if fp1 == fp2 {
			t.Error("different versions should produce different fingerprints")
		}
	})

	t.Run("output is 32 hex chars (xxh3-128)", func(t *testing.T) {
		fp := FingerprintBundle("1.0.0", `{"config.js":"content"}`, "")
		if len(fp) != 32 {
			t.Errorf("fingerprint length = %d, want 32", len(fp))
		}
	})
}

func TestBundleEntryKey(t *testing.T) {
	t.Run("formats correctly", func(t *testing.T) {
		key := BundleEntryKey("my-bundle")
		expected := "bundle:my-bundle"
		if key != expected {
			t.Errorf("BundleEntryKey() = %q, want %q", key, expected)
		}
	})
}
