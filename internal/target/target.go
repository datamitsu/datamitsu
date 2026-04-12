package target

import "runtime"

// LibcType represents the libc implementation on the host system.
type LibcType string

const (
	LibcGlibc   LibcType = "glibc"
	LibcMusl    LibcType = "musl"
	LibcUnknown LibcType = "unknown"
)

// ResolutionSource indicates how a target was resolved.
type ResolutionSource int

const (
	ResolutionExact    ResolutionSource = iota
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
func DetectHost() Target {
	libc := LibcUnknown
	if runtime.GOOS == "linux" {
		libc = DetectLibc()
	}
	return Target{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
		Libc: libc,
	}
}
