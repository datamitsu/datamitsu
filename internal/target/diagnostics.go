package target

import "fmt"

// DiagnosticInfo captures the full resolution chain for debugging and user feedback.
type DiagnosticInfo struct {
	HostTarget     Target
	RequestedTarget Target
	ResolvedTarget  ResolvedTarget
	CachePath       string
}

// String returns a human-readable summary of the resolution chain.
func (d DiagnosticInfo) String() string {
	source := "exact"
	if d.ResolvedTarget.Source == ResolutionFallback {
		source = "fallback"
	}

	s := fmt.Sprintf("host=%s requested=%s resolved=%s (%s)",
		d.HostTarget.String(),
		d.RequestedTarget.String(),
		d.ResolvedTarget.Target.String(),
		source,
	)

	if d.CachePath != "" {
		s += fmt.Sprintf(" cache=%s", d.CachePath)
	}

	if d.ResolvedTarget.Source == ResolutionFallback && d.ResolvedTarget.FallbackInfo != nil {
		s += fmt.Sprintf(" reason=%q", d.ResolvedTarget.FallbackInfo.Reason)
	}

	return s
}

// FallbackWarning returns a user-friendly warning message when resolution
// fell back to a different libc variant. Returns empty string for exact matches.
func FallbackWarning(name string, resolved ResolvedTarget) string {
	if resolved.Source != ResolutionFallback || resolved.FallbackInfo == nil {
		return ""
	}

	requested := resolved.FallbackInfo.RequestedTarget
	if requested.Libc != LibcUnknown && resolved.Target.Libc != requested.Libc {
		return fmt.Sprintf("%s: %s binary not found, falling back to %s variant",
			name, string(requested.Libc), string(resolved.Target.Libc))
	}

	return fmt.Sprintf("%s: exact binary not found for %s, using %s",
		name, requested.String(), resolved.Target.String())
}
