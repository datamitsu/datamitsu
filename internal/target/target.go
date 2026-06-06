package target

import (
	"context"
	"runtime"
	"sync"
)

// LibcType represents the libc implementation on the host system.
type LibcType string

// Supported libc implementations; LibcUnknown means detection failed or N/A.
const (
	LibcGlibc   LibcType = "glibc"
	LibcMusl    LibcType = "musl"
	LibcUnknown LibcType = "unknown"
)

// ResolutionSource indicates how a target was resolved.
type ResolutionSource int

// Resolution outcomes: ResolutionExact for an exact match, ResolutionFallback
// when a substitute target was used.
const (
	ResolutionExact ResolutionSource = iota
	ResolutionFallback
)

// Target represents a platform target with OS, Arch, and Libc dimensions.
type Target struct {
	OS   string
	Arch string
	Libc LibcType
}

// String returns the canonical string representation: "os/arch/libc".
func (t Target) String() string {
	return t.OS + "/" + t.Arch + "/" + string(t.Libc)
}

// FallbackInfo describes why a fallback was needed during resolution.
type FallbackInfo struct {
	RequestedTarget Target
	Reason          string
}

// ResolvedTarget is the result of target resolution, tracking whether
// an exact match or fallback was used.
type ResolvedTarget struct {
	Target       Target
	Source       ResolutionSource
	FallbackInfo *FallbackInfo
}

// DetectHost returns the Target for the current system.
func DetectHost(ctx context.Context) Target {
	libc := LibcUnknown
	if runtime.GOOS == "linux" {
		libc = DetectLibc(ctx)
	}
	return Target{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
		Libc: libc,
	}
}

// cachedHost memoizes host detection. The host target is machine-static, so the
// underlying `ldd` probe runs at most once per process instead of on every
// BinManager/RuntimeManager construction. The probe is a one-time startup
// detection with no caller context, so a background context is correct.
var cachedHost = sync.OnceValue(func() Target {
	return DetectHost(context.Background())
})

// HostTarget returns the memoized host Target, running detection once on first
// call. Use this from constructors and other contextless call sites; callers
// that already hold a context should call DetectHost/DetectLibc directly.
func HostTarget() Target {
	return cachedHost()
}
