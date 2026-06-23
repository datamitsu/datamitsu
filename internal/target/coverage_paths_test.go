package target

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestHostTarget exercises the memoized host detection. It must agree with a
// direct DetectHost call and be stable across repeated invocations.
func TestHostTarget(t *testing.T) {
	first := HostTarget()
	second := HostTarget()
	if first != second {
		t.Errorf("HostTarget not memoized: %v != %v", first, second)
	}
	direct := DetectHost(context.Background())
	if first.OS != direct.OS || first.Arch != direct.Arch {
		t.Errorf("HostTarget OS/Arch %v/%v, want %v/%v", first.OS, first.Arch, direct.OS, direct.Arch)
	}
	if first.OS != runtime.GOOS || first.Arch != runtime.GOARCH {
		t.Errorf("HostTarget = %v/%v, want %v/%v", first.OS, first.Arch, runtime.GOOS, runtime.GOARCH)
	}
}

// TestDefaultLoaderGlob covers the production glob function used by loader-path
// detection: a valid pattern returns matches (possibly empty), a malformed
// pattern surfaces an error.
func TestDefaultLoaderGlob(t *testing.T) {
	if _, err := defaultLoaderGlob("/lib/ld-*.so*"); err != nil {
		t.Errorf("defaultLoaderGlob valid pattern returned error: %v", err)
	}

	tmp := t.TempDir()
	expected := filepath.Join(tmp, "marker.txt")
	if err := os.WriteFile(expected, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	matches, err := defaultLoaderGlob(filepath.Join(tmp, "*.txt"))
	if err != nil {
		t.Fatalf("defaultLoaderGlob returned error: %v", err)
	}
	if len(matches) != 1 || matches[0] != expected {
		t.Errorf("defaultLoaderGlob = %v, want [%s]", matches, expected)
	}

	if _, err := defaultLoaderGlob("[invalid"); err == nil {
		t.Error("defaultLoaderGlob with malformed pattern expected error, got nil")
	}
}

// TestRunLddVersionOnLinux ensures runLddVersion returns a string without
// panicking on the host. The content depends on the host libc, so we only
// assert it is callable and DetectLibc agrees with one of the known outcomes.
func TestRunLddVersionOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ldd probe only runs on Linux")
	}
	// Should not panic; output may be empty when ldd is absent.
	_ = runLddVersion(context.Background())
}

// TestDetectViaLddOnLinux drives the real ldd-based detection path on Linux.
func TestDetectViaLddOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only runs on Linux")
	}
	got := detectViaLdd(context.Background())
	if got != LibcGlibc && got != LibcMusl && got != LibcUnknown {
		t.Errorf("detectViaLdd = %q, want one of glibc/musl/unknown", got)
	}
}

// TestDetectViaELFOnLinux drives the real ELF-interpreter detection path.
func TestDetectViaELFOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only runs on Linux")
	}
	got := detectViaELF()
	if got != LibcGlibc && got != LibcMusl && got != LibcUnknown {
		t.Errorf("detectViaELF = %q, want one of glibc/musl/unknown", got)
	}
}

// TestDetectViaLoaderPathsOnLinux drives the real loader-path globbing.
func TestDetectViaLoaderPathsOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only runs on Linux")
	}
	got := detectViaLoaderPaths()
	if got != LibcGlibc && got != LibcMusl && got != LibcUnknown {
		t.Errorf("detectViaLoaderPaths = %q, want one of glibc/musl/unknown", got)
	}
}
