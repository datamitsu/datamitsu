package target

import (
	"fmt"
	"sort"
)

// Candidate pairs a Target with arbitrary binary info for resolution.
type Candidate struct {
	Target Target
	Info   interface{}
}

// Resolver selects the best candidate for a host target using scoring.
type Resolver struct {
	host Target
}

// NewResolver creates a Resolver for the given host target.
func NewResolver(host Target) *Resolver {
	return &Resolver{host: host}
}

// Host returns the resolver's host target.
func (r *Resolver) Host() Target {
	return r.host
}

// Resolve selects the best candidate for the given app name.
// Returns nil ResolvedTarget and nil info if no candidates match OS+Arch.
func (r *Resolver) Resolve(name string, candidates []Candidate) (*ResolvedTarget, interface{}) {
	if len(candidates) == 0 {
		return nil, nil
	}

	type scored struct {
		candidate Candidate
		score     int
	}

	var scoredCandidates []scored
	for _, c := range candidates {
		s := r.scoreCandidate(c.Target)
		if s == 0 {
			continue
		}
		scoredCandidates = append(scoredCandidates, scored{candidate: c, score: s})
	}

	if len(scoredCandidates) == 0 {
		return nil, nil
	}

	sort.SliceStable(scoredCandidates, func(i, j int) bool {
		if scoredCandidates[i].score != scoredCandidates[j].score {
			return scoredCandidates[i].score > scoredCandidates[j].score
		}
		return scoredCandidates[i].candidate.Target.String() < scoredCandidates[j].candidate.Target.String()
	})

	best := scoredCandidates[0]
	resolved := &ResolvedTarget{
		Target: best.candidate.Target,
	}

	if r.isExactMatch(best.candidate.Target) {
		resolved.Source = ResolutionExact
	} else {
		resolved.Source = ResolutionFallback
		resolved.FallbackInfo = &FallbackInfo{
			RequestedTarget: r.host,
			Reason:          r.fallbackReason(best.candidate.Target),
		}
	}

	return resolved, best.candidate.Info
}

// scoreCandidate returns a score for how well a candidate matches the host.
// Returns 0 if OS or Arch doesn't match (disqualified).
func (r *Resolver) scoreCandidate(t Target) int {
	if t.OS != r.host.OS {
		return 0
	}
	if t.Arch != r.host.Arch {
		return 0
	}

	score := 1000 + 100 // OS + Arch match

	switch {
	case t.Libc == r.host.Libc:
		score += 10
	case t.Libc == LibcUnknown || r.host.Libc == LibcUnknown:
		score += 5
	default:
		score += 1
	}

	return score
}

// isExactMatch returns true if the candidate matches OS, Arch, and Libc exactly.
func (r *Resolver) isExactMatch(t Target) bool {
	return t.OS == r.host.OS && t.Arch == r.host.Arch && t.Libc == r.host.Libc
}

// fallbackReason generates a human-readable reason for the fallback.
func (r *Resolver) fallbackReason(resolved Target) string {
	if resolved.Libc == LibcUnknown && r.host.Libc != LibcUnknown {
		return fmt.Sprintf("no %s-specific binary for %s, using generic variant",
			string(r.host.Libc), r.host.String())
	}
	if r.host.Libc != LibcUnknown && resolved.Libc != r.host.Libc {
		return fmt.Sprintf("%s binary not available for %s, using %s variant",
			string(r.host.Libc), r.host.String(), string(resolved.Libc))
	}
	return fmt.Sprintf("exact match not available for %s, using %s", r.host.String(), resolved.String())
}
