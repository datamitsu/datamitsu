package target

import (
	"testing"
)

func TestResolverExactMatch(t *testing.T) {
	host := Target{OS: "linux", Arch: "amd64", Libc: LibcMusl}
	resolver := NewResolver(host)

	candidates := []Candidate{
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc}, Info: "glibc-binary"},
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl}, Info: "musl-binary"},
	}

	resolved, info := resolver.Resolve("test-app", candidates)
	if resolved == nil {
		t.Fatal("expected resolved target, got nil")
	}
	if resolved.Source != ResolutionExact {
		t.Errorf("expected ResolutionExact, got %d", resolved.Source)
	}
	if resolved.Target.Libc != LibcMusl {
		t.Errorf("expected musl libc, got %s", resolved.Target.Libc)
	}
	if info != "musl-binary" {
		t.Errorf("expected musl-binary info, got %v", info)
	}
	if resolved.FallbackInfo != nil {
		t.Error("expected no fallback info for exact match")
	}
}

func TestResolverFallbackToGlibc(t *testing.T) {
	host := Target{OS: "linux", Arch: "amd64", Libc: LibcMusl}
	resolver := NewResolver(host)

	candidates := []Candidate{
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc}, Info: "glibc-binary"},
	}

	resolved, info := resolver.Resolve("test-app", candidates)
	if resolved == nil {
		t.Fatal("expected resolved target, got nil")
	}
	if resolved.Source != ResolutionFallback {
		t.Errorf("expected ResolutionFallback, got %d", resolved.Source)
	}
	if resolved.Target.Libc != LibcGlibc {
		t.Errorf("expected glibc libc, got %s", resolved.Target.Libc)
	}
	if info != "glibc-binary" {
		t.Errorf("expected glibc-binary info, got %v", info)
	}
	if resolved.FallbackInfo == nil {
		t.Fatal("expected fallback info")
	}
	if resolved.FallbackInfo.RequestedTarget.Libc != LibcMusl {
		t.Errorf("expected requested target libc musl, got %s", resolved.FallbackInfo.RequestedTarget.Libc)
	}
	if resolved.FallbackInfo.Reason == "" {
		t.Error("expected non-empty fallback reason")
	}
}

func TestResolverUnknownLibcHost(t *testing.T) {
	host := Target{OS: "linux", Arch: "amd64", Libc: LibcUnknown}
	resolver := NewResolver(host)

	candidates := []Candidate{
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc}, Info: "glibc-binary"},
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl}, Info: "musl-binary"},
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcUnknown}, Info: "unknown-binary"},
	}

	resolved, info := resolver.Resolve("test-app", candidates)
	if resolved == nil {
		t.Fatal("expected resolved target, got nil")
	}
	// Unknown host libc should prefer unknown candidate (exact match score +10)
	if resolved.Source != ResolutionExact {
		t.Errorf("expected ResolutionExact for unknown-unknown match, got %d", resolved.Source)
	}
	if info != "unknown-binary" {
		t.Errorf("expected unknown-binary info, got %v", info)
	}
}

func TestResolverUnknownLibcHostNoUnknownCandidate(t *testing.T) {
	host := Target{OS: "linux", Arch: "amd64", Libc: LibcUnknown}
	resolver := NewResolver(host)

	candidates := []Candidate{
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl}, Info: "musl-binary"},
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc}, Info: "glibc-binary"},
	}

	resolved, info := resolver.Resolve("test-app", candidates)
	if resolved == nil {
		t.Fatal("expected resolved target, got nil")
	}
	// Both get neutral score +5, tiebreak alphabetically: glibc < musl
	if resolved.Source != ResolutionFallback {
		t.Errorf("expected ResolutionFallback, got %d", resolved.Source)
	}
	if info != "glibc-binary" {
		t.Errorf("expected glibc-binary (alphabetical tiebreak), got %v", info)
	}
}

func TestResolverDeterministicTiebreaking(t *testing.T) {
	host := Target{OS: "linux", Arch: "amd64", Libc: LibcUnknown}
	resolver := NewResolver(host)

	// Two candidates with identical scores (both get neutral +5 for unknown host)
	// Should tiebreak alphabetically by Target.String(): glibc < musl
	candidates := []Candidate{
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl}, Info: "musl-binary"},
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc}, Info: "glibc-binary"},
	}

	resolved, info := resolver.Resolve("test-app", candidates)
	if resolved == nil {
		t.Fatal("expected resolved target, got nil")
	}
	if info != "glibc-binary" {
		t.Errorf("expected glibc-binary (alphabetical tiebreak), got %v", info)
	}

	// Run again with reversed input order to verify determinism
	reversedCandidates := []Candidate{
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc}, Info: "glibc-binary"},
		{Target: Target{OS: "linux", Arch: "amd64", Libc: LibcMusl}, Info: "musl-binary"},
	}
	resolved2, info2 := resolver.Resolve("test-app", reversedCandidates)
	if resolved2 == nil {
		t.Fatal("expected resolved target, got nil")
	}
	if info2 != "glibc-binary" {
		t.Errorf("expected glibc-binary regardless of input order, got %v", info2)
	}
	if resolved.Target.String() != resolved2.Target.String() {
		t.Error("resolution is not deterministic across different input orders")
	}
}

func TestResolverWrongOSDisqualified(t *testing.T) {
	host := Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc}
	resolver := NewResolver(host)

	candidates := []Candidate{
		{Target: Target{OS: "darwin", Arch: "amd64", Libc: LibcUnknown}, Info: "darwin-binary"},
	}

	resolved, _ := resolver.Resolve("test-app", candidates)
	if resolved != nil {
		t.Error("expected nil for wrong OS")
	}
}

func TestResolverWrongArchDisqualified(t *testing.T) {
	host := Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc}
	resolver := NewResolver(host)

	candidates := []Candidate{
		{Target: Target{OS: "linux", Arch: "arm64", Libc: LibcGlibc}, Info: "arm64-binary"},
	}

	resolved, _ := resolver.Resolve("test-app", candidates)
	if resolved != nil {
		t.Error("expected nil for wrong arch")
	}
}

func TestResolverEmptyCandidates(t *testing.T) {
	host := Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc}
	resolver := NewResolver(host)

	resolved, info := resolver.Resolve("test-app", nil)
	if resolved != nil {
		t.Error("expected nil for empty candidates")
	}
	if info != nil {
		t.Error("expected nil info for empty candidates")
	}
}

func TestResolverNonLinuxExactMatch(t *testing.T) {
	host := Target{OS: "darwin", Arch: "arm64", Libc: LibcUnknown}
	resolver := NewResolver(host)

	candidates := []Candidate{
		{Target: Target{OS: "darwin", Arch: "arm64", Libc: LibcUnknown}, Info: "darwin-binary"},
	}

	resolved, _ := resolver.Resolve("test-app", candidates)
	if resolved == nil {
		t.Fatal("expected resolved target")
	}
	if resolved.Source != ResolutionExact {
		t.Errorf("expected exact match for darwin unknown-unknown, got %d", resolved.Source)
	}
}

func TestResolverHost(t *testing.T) {
	host := Target{OS: "linux", Arch: "amd64", Libc: LibcMusl}
	resolver := NewResolver(host)

	if resolver.Host() != host {
		t.Errorf("expected host %v, got %v", host, resolver.Host())
	}
}

func TestScoreCandidateWeights(t *testing.T) {
	host := Target{OS: "linux", Arch: "amd64", Libc: LibcMusl}
	resolver := NewResolver(host)

	exactMatch := resolver.scoreCandidate(Target{OS: "linux", Arch: "amd64", Libc: LibcMusl})
	unknownLibc := resolver.scoreCandidate(Target{OS: "linux", Arch: "amd64", Libc: LibcUnknown})
	mismatchLibc := resolver.scoreCandidate(Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc})
	wrongOS := resolver.scoreCandidate(Target{OS: "darwin", Arch: "amd64", Libc: LibcMusl})
	wrongArch := resolver.scoreCandidate(Target{OS: "linux", Arch: "arm64", Libc: LibcMusl})

	if exactMatch != 1110 {
		t.Errorf("exact match score: expected 1110, got %d", exactMatch)
	}
	if unknownLibc != 1105 {
		t.Errorf("unknown libc score: expected 1105, got %d", unknownLibc)
	}
	if mismatchLibc != 1101 {
		t.Errorf("mismatch libc score: expected 1101, got %d", mismatchLibc)
	}
	if wrongOS != 0 {
		t.Errorf("wrong OS score: expected 0, got %d", wrongOS)
	}
	if wrongArch != 0 {
		t.Errorf("wrong arch score: expected 0, got %d", wrongArch)
	}

	// Verify ordering invariants
	if exactMatch <= unknownLibc {
		t.Error("exact libc must beat unknown libc")
	}
	if unknownLibc <= mismatchLibc {
		t.Error("unknown libc must beat mismatch libc")
	}
}

func TestFallbackReasonMessages(t *testing.T) {
	tests := []struct {
		name     string
		host     Target
		resolved Target
		contains string
	}{
		{
			name:     "musl host glibc fallback",
			host:     Target{OS: "linux", Arch: "amd64", Libc: LibcMusl},
			resolved: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc},
			contains: "musl binary not available",
		},
		{
			name:     "glibc host unknown fallback",
			host:     Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc},
			resolved: Target{OS: "linux", Arch: "amd64", Libc: LibcUnknown},
			contains: "no glibc-specific binary",
		},
		{
			name:     "unknown host specific fallback",
			host:     Target{OS: "linux", Arch: "amd64", Libc: LibcUnknown},
			resolved: Target{OS: "linux", Arch: "amd64", Libc: LibcGlibc},
			contains: "exact match not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewResolver(tt.host)
			reason := resolver.fallbackReason(tt.resolved)
			if reason == "" {
				t.Error("expected non-empty fallback reason")
			}
			found := false
			for i := 0; i <= len(reason)-len(tt.contains); i++ {
				if reason[i:i+len(tt.contains)] == tt.contains {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected reason to contain %q, got %q", tt.contains, reason)
			}
		})
	}
}
