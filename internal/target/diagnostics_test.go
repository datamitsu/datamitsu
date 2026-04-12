package target

import (
	"strings"
	"testing"
)

func TestDiagnosticInfoStringExactMatch(t *testing.T) {
	d := DiagnosticInfo{
		HostTarget:      Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
		RequestedTarget: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
		ResolvedTarget: ResolvedTarget{
			Target: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
			Source: ResolutionExact,
		},
		CachePath: "/cache/bin/app/abc123",
	}

	s := d.String()
	if !strings.Contains(s, "host=linux/amd64/musl") {
		t.Errorf("expected host target in output, got: %s", s)
	}
	if !strings.Contains(s, "resolved=linux/amd64/musl") {
		t.Errorf("expected resolved target in output, got: %s", s)
	}
	if !strings.Contains(s, "(exact)") {
		t.Errorf("expected (exact) in output, got: %s", s)
	}
	if !strings.Contains(s, "cache=/cache/bin/app/abc123") {
		t.Errorf("expected cache path in output, got: %s", s)
	}
	if strings.Contains(s, "reason=") {
		t.Errorf("expected no reason for exact match, got: %s", s)
	}
}

func TestDiagnosticInfoStringFallback(t *testing.T) {
	d := DiagnosticInfo{
		HostTarget:      Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
		RequestedTarget: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
		ResolvedTarget: ResolvedTarget{
			Target: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc},
			Source: ResolutionFallback,
			FallbackInfo: &FallbackInfo{
				RequestedTarget: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
				Reason:          "musl binary not available for linux/amd64/musl, using glibc variant",
			},
		},
	}

	s := d.String()
	if !strings.Contains(s, "(fallback)") {
		t.Errorf("expected (fallback) in output, got: %s", s)
	}
	if !strings.Contains(s, "resolved=linux/amd64/glibc") {
		t.Errorf("expected resolved glibc target, got: %s", s)
	}
	if !strings.Contains(s, "reason=") {
		t.Errorf("expected reason in output, got: %s", s)
	}
	if !strings.Contains(s, "musl binary not available") {
		t.Errorf("expected fallback reason content, got: %s", s)
	}
}

func TestDiagnosticInfoStringNoCachePath(t *testing.T) {
	d := DiagnosticInfo{
		HostTarget:      Target{OS: "darwin", Arch: "arm64", Libc: LibcUnknown},
		RequestedTarget: Target{OS: "darwin", Arch: "arm64", Libc: LibcUnknown},
		ResolvedTarget: ResolvedTarget{
			Target: Target{OS: "darwin", Arch: "arm64", Libc: LibcUnknown},
			Source: ResolutionExact,
		},
	}

	s := d.String()
	if strings.Contains(s, "cache=") {
		t.Errorf("expected no cache path when empty, got: %s", s)
	}
}

func TestFallbackWarningMuslToGlibc(t *testing.T) {
	resolved := ResolvedTarget{
		Target: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc},
		Source: ResolutionFallback,
		FallbackInfo: &FallbackInfo{
			RequestedTarget: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
			Reason:          "musl binary not available",
		},
	}

	msg := FallbackWarning("typst", resolved)
	if msg == "" {
		t.Fatal("expected non-empty warning for fallback")
	}
	if !strings.Contains(msg, "typst") {
		t.Errorf("expected app name in warning, got: %s", msg)
	}
	if !strings.Contains(msg, "musl binary not found") {
		t.Errorf("expected 'musl binary not found' in warning, got: %s", msg)
	}
	if !strings.Contains(msg, "falling back to glibc variant") {
		t.Errorf("expected 'falling back to glibc variant' in warning, got: %s", msg)
	}
}

func TestFallbackWarningGlibcToMusl(t *testing.T) {
	resolved := ResolvedTarget{
		Target: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
		Source: ResolutionFallback,
		FallbackInfo: &FallbackInfo{
			RequestedTarget: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc},
			Reason:          "glibc binary not available",
		},
	}

	msg := FallbackWarning("shellcheck", resolved)
	if !strings.Contains(msg, "glibc binary not found") {
		t.Errorf("expected 'glibc binary not found' in warning, got: %s", msg)
	}
	if !strings.Contains(msg, "falling back to musl variant") {
		t.Errorf("expected 'falling back to musl variant' in warning, got: %s", msg)
	}
}

func TestFallbackWarningUnknownLibc(t *testing.T) {
	resolved := ResolvedTarget{
		Target: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc},
		Source: ResolutionFallback,
		FallbackInfo: &FallbackInfo{
			RequestedTarget: Target{OS: "linux", Arch: "amd64", Libc: LibcUnknown},
			Reason:          "no exact match",
		},
	}

	msg := FallbackWarning("hadolint", resolved)
	if msg == "" {
		t.Fatal("expected non-empty warning")
	}
	if !strings.Contains(msg, "hadolint") {
		t.Errorf("expected app name in warning, got: %s", msg)
	}
	if !strings.Contains(msg, "exact binary not found") {
		t.Errorf("expected generic fallback message for unknown libc, got: %s", msg)
	}
}

func TestFallbackWarningExactMatchReturnsEmpty(t *testing.T) {
	resolved := ResolvedTarget{
		Target: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
		Source: ResolutionExact,
	}

	msg := FallbackWarning("typst", resolved)
	if msg != "" {
		t.Errorf("expected empty warning for exact match, got: %s", msg)
	}
}

func TestFallbackWarningNilFallbackInfoReturnsEmpty(t *testing.T) {
	resolved := ResolvedTarget{
		Target: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
		Source: ResolutionFallback,
	}

	msg := FallbackWarning("typst", resolved)
	if msg != "" {
		t.Errorf("expected empty warning when FallbackInfo is nil, got: %s", msg)
	}
}
