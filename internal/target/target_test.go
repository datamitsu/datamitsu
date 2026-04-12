package target

import (
	"runtime"
	"testing"
)

func TestTargetString(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		want   string
	}{
		{
			name:   "linux amd64 glibc",
			target: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc},
			want:   "linux/amd64/glibc",
		},
		{
			name:   "linux amd64 musl",
			target: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
			want:   "linux/amd64/musl",
		},
		{
			name:   "linux arm64 unknown",
			target: Target{OS: "linux", Arch: "arm64", Libc: LibcUnknown},
			want:   "linux/arm64/unknown",
		},
		{
			name:   "darwin arm64 unknown",
			target: Target{OS: "darwin", Arch: "arm64", Libc: LibcUnknown},
			want:   "darwin/arm64/unknown",
		},
		{
			name:   "windows amd64 unknown",
			target: Target{OS: "windows", Arch: "amd64", Libc: LibcUnknown},
			want:   "windows/amd64/unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.target.String()
			if got != tt.want {
				t.Errorf("Target.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolvedTargetExact(t *testing.T) {
	target := Target{OS: "linux", Arch: "amd64", Libc: LibcMusl}
	resolved := ResolvedTarget{
		Target:       target,
		Source:       ResolutionExact,
		FallbackInfo: nil,
	}

	if resolved.Source != ResolutionExact {
		t.Errorf("expected ResolutionExact, got %d", resolved.Source)
	}
	if resolved.FallbackInfo != nil {
		t.Error("expected nil FallbackInfo for exact match")
	}
	if resolved.Target.String() != "linux/amd64/musl" {
		t.Errorf("unexpected target string: %s", resolved.Target.String())
	}
}

func TestResolvedTargetFallback(t *testing.T) {
	requested := Target{OS: "linux", Arch: "amd64", Libc: LibcMusl}
	resolved := ResolvedTarget{
		Target: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc},
		Source: ResolutionFallback,
		FallbackInfo: &FallbackInfo{
			RequestedTarget: requested,
			Reason:          "musl binary not available, using glibc variant",
		},
	}

	if resolved.Source != ResolutionFallback {
		t.Errorf("expected ResolutionFallback, got %d", resolved.Source)
	}
	if resolved.FallbackInfo == nil {
		t.Fatal("expected non-nil FallbackInfo for fallback")
	}
	if resolved.FallbackInfo.RequestedTarget.String() != "linux/amd64/musl" {
		t.Errorf("unexpected requested target: %s", resolved.FallbackInfo.RequestedTarget.String())
	}
	if resolved.FallbackInfo.Reason == "" {
		t.Error("expected non-empty fallback reason")
	}
	if resolved.Target.String() != "linux/amd64/glibc" {
		t.Errorf("unexpected resolved target: %s", resolved.Target.String())
	}
}

func TestDetectHost(t *testing.T) {
	host := DetectHost()

	if host.OS != runtime.GOOS {
		t.Errorf("DetectHost().OS = %q, want %q", host.OS, runtime.GOOS)
	}
	if host.Arch != runtime.GOARCH {
		t.Errorf("DetectHost().Arch = %q, want %q", host.Arch, runtime.GOARCH)
	}

	if runtime.GOOS != "linux" {
		if host.Libc != LibcUnknown {
			t.Errorf("non-Linux DetectHost().Libc = %q, want %q", host.Libc, LibcUnknown)
		}
	}
}

func TestResolutionSourceValues(t *testing.T) {
	if ResolutionExact != 0 {
		t.Errorf("ResolutionExact = %d, want 0", ResolutionExact)
	}
	if ResolutionFallback != 1 {
		t.Errorf("ResolutionFallback = %d, want 1", ResolutionFallback)
	}
}

func TestLibcTypeConstants(t *testing.T) {
	if LibcGlibc != "glibc" {
		t.Errorf("LibcGlibc = %q, want %q", LibcGlibc, "glibc")
	}
	if LibcMusl != "musl" {
		t.Errorf("LibcMusl = %q, want %q", LibcMusl, "musl")
	}
	if LibcUnknown != "unknown" {
		t.Errorf("LibcUnknown = %q, want %q", LibcUnknown, "unknown")
	}
}
