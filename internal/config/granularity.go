package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ToolGranularity is the smallest set of input files on which an operation's
// verdict is complete. Orthogonal to ToolScope (where the process starts) and to
// ToolArity (what goes in argv): this is about when an answer can be trusted.
type ToolGranularity string

// Tool granularities, from the smallest complete input set to the largest.
const (
	GranularityFile ToolGranularity = "file" // any subset of files is a complete input
	GranularityUnit ToolGranularity = "unit" // valid only over a whole project/module
	GranularityRepo ToolGranularity = "repo" // valid only over the whole repository
)

// InferGranularity derives granularity when the operation does not declare it.
//
// per-project maps to "unit" unconditionally — deliberately not inferred from
// the args shape. A tool like `ty` is per-project with a {files} list, shaped
// identically to prettier, but its verdict is cross-file; no inspection of argv
// can tell them apart. Being wrong towards "unit" costs a cache entry, being
// wrong towards "file" costs a wrong answer, so the default takes the safe
// direction and declaring "file" is an explicit speed decision.
func InferGranularity(op ToolOperation) ToolGranularity {
	if op.Granularity != "" {
		return op.Granularity
	}
	switch op.Scope {
	case ToolScopePerFile:
		return GranularityFile
	case ToolScopeRepository:
		if ArgsReferenceFiles(op.Args) {
			return GranularityFile
		}
		return GranularityRepo
	case ToolScopePerProject:
		return GranularityUnit
	default:
		// An unvalidated scope lands in the per-project branch of the planner, so
		// it gets the per-project answer here too.
		return GranularityUnit
	}
}

// WidenTo is how far the core may widen work beyond the requested selection.
type WidenTo string

// Widening levels, ordered target < unit < repo.
const (
	WidenToTarget WidenTo = "target" // only what was named
	WidenToUnit   WidenTo = "unit"   // may widen to the unit containing the target
	WidenToRepo   WidenTo = "repo"   // may widen to the whole repository
)

// widenRank orders the lattice so the LSP session policy can narrow but never
// widen the config-resolved value.
var widenRank = map[WidenTo]int{WidenToTarget: 0, WidenToUnit: 1, WidenToRepo: 2}

// Rank returns the position of w in the target < unit < repo lattice.
func (w WidenTo) Rank() int { return widenRank[w] }

// Execution holds run-shaping policy that is not tied to a single tool.
type Execution struct {
	// WidenTo is per operation; an operation left unset takes DefaultWidenTo.
	WidenTo map[OperationType]WidenTo `json:"widenTo,omitempty"`
}

// DefaultWidenTo is the core default for an operation with no declared policy.
// "unit" and not "repo": otherwise `dm lint ./one.ts` would drag knip, syncpack
// and the scanners across the whole repository and a targeted check would stop
// being targeted.
const DefaultWidenTo = WidenToUnit

// ValidWidenTo reports whether w is a known widening level.
func ValidWidenTo(w WidenTo) bool {
	_, ok := widenRank[w]
	return ok
}

// ValidateExecution rejects unknown operations and widening levels.
//
// Both are read through maps that yield the zero value for anything
// unrecognised, so without this a typo is silently the opposite of what was
// asked: widenTo: { fix: "Repo" } loads, fails every comparison against "repo",
// and ranks 0 — the same as "target", the narrowest policy. --widen-to has been
// checked since it was added; the config form deserves the same.
func ValidateExecution(e *Execution) error {
	if e == nil {
		return nil
	}
	var errs []string
	ops := make([]string, 0, len(e.WidenTo))
	for op := range e.WidenTo {
		ops = append(ops, string(op))
	}
	sort.Strings(ops)

	for _, op := range ops {
		if OperationType(op) != OpFix && OperationType(op) != OpLint {
			errs = append(errs, fmt.Sprintf(
				"execution.widenTo: unknown operation %q (want %q or %q)", op, OpFix, OpLint))
		}
		if level := e.WidenTo[OperationType(op)]; !ValidWidenTo(level) {
			errs = append(errs, fmt.Sprintf(
				"execution.widenTo.%s: unknown value %q (want %q, %q or %q)",
				op, level, WidenToTarget, WidenToUnit, WidenToRepo))
		}
	}

	if len(errs) > 0 {
		return errors.New("config validation failed:\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}

// ResolveWidenTo returns the policy for an operation. An explicit override wins
// outright, in both directions: it comes from --widen-to, which a person typed
// for this one run. The narrow-only rule of the lattice governs the LSP session
// policy instead, because that one is ambient — an editor must not be able to
// out-scope the project on every save. Nil-safe: an absent block means defaults.
func (e *Execution) ResolveWidenTo(op OperationType, override WidenTo) WidenTo {
	if override != "" {
		return override
	}
	if e != nil {
		if declared, ok := e.WidenTo[op]; ok && declared != "" {
			return declared
		}
	}
	return DefaultWidenTo
}
