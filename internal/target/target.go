package target

import (
	"context"
	"runtime"
	"sync"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/logger"

	"go.uber.org/zap"
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

// DetectHost returns the Target for the current system. On Linux the
// DATAMITSU_LIBC override, when set to a valid value, takes precedence over
// probing — it both fixes fragile detection on distroless hosts (no
// ldd/loader) and keeps store paths and OCI bundle selection consistent.
func DetectHost(ctx context.Context) Target {
	libc := LibcUnknown
	if runtime.GOOS == "linux" {
		if override, ok := libcFromEnv(); ok {
			libc = override
		} else {
			libc = DetectLibc(ctx)
		}
	}
	return Target{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
		Libc: libc,
	}
}

// libcFromEnv validates the DATAMITSU_LIBC override. Only glibc and musl are
// accepted; anything else is ignored with a warning so a typo degrades to
// regular detection instead of poisoning store paths.
func libcFromEnv() (LibcType, bool) {
	switch value := env.LibcOverride(); value {
	case "":
		return LibcUnknown, false
	case string(LibcGlibc):
		return LibcGlibc, true
	case string(LibcMusl):
		return LibcMusl, true
	default:
		logger.Logger.Warn("ignoring invalid libc override (must be glibc or musl)",
			zap.String("value", value))
		return LibcUnknown, false
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
