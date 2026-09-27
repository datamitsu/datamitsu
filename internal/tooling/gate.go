package tooling

import (
	"fmt"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
)

// GateDecision is what a gate made of one parsed process's findings.
type GateDecision struct {
	// Failed reports that findings gate the process. The executor fails it
	// only when it exited 0: a non-zero exit is a failure at any threshold.
	Failed bool
	// Reason is the error a gated process fails with.
	Reason string
	// Gating are the indexes into the process's Diagnostics of the findings
	// that gate.
	Gating []int
}

// Gate decides, for one process whose output was parsed, which of its findings
// gate. It may annotate proc — its findings' Reported and Gates, its FailOn
// and GateActive — and is called for every such process, concurrently.
type Gate func(task Task, proc *ProcessResult) GateDecision

// SetGate wires the gate the executor applies after parsing a process's
// output. Without one, the exit code alone decides.
func (e *Executor) SetGate(gate Gate) {
	e.gate = gate
}

// ThresholdError is the failure of a process that exited 0 with findings at or
// above its operation's failOn.
type ThresholdError struct {
	Reason string
}

func (e *ThresholdError) Error() string { return e.Reason }

// ThresholdGate is the failOn gate. A finding at or above its operation's
// effective threshold — config.EffectiveFailOn of the operation and global —
// is reported; it gates when the module that parsed it declares the severity
// contract, which contract answers per module. A process whose module does not
// is judged by its exit code alone, and when its threshold is not the default
// ignored is told the tool, so a threshold nobody enforces is not silent.
func ThresholdGate(global config.Severity, contract func(module string) bool, ignored func(tool string)) Gate {
	return func(task Task, proc *ProcessResult) GateDecision {
		failOn := config.EffectiveFailOn(task.OpConfig, global)
		level := diagnostic.Severity(failOn.Level())
		active := task.Tool.OutputParser != nil && contract(task.Tool.OutputParser.Module)
		proc.FailOn, proc.GateActive = failOn, active

		var gating []int
		for i := range proc.Diagnostics {
			d := &proc.Diagnostics[i]
			d.Reported = d.Severity <= level
			d.Gates = d.Reported && active
			if d.Gates {
				gating = append(gating, i)
			}
		}
		parsed := proc.Extraction == ExtractionParsedClean || proc.Extraction == ExtractionParsedFindings
		if parsed && !active && failOn != config.DefaultFailOn && ignored != nil {
			ignored(task.ToolName)
		}
		if len(gating) == 0 {
			return GateDecision{}
		}
		noun := "findings"
		if len(gating) == 1 {
			noun = "finding"
		}
		return GateDecision{
			Failed: true,
			Reason: fmt.Sprintf("%d %s at or above failOn=%s", len(gating), noun, failOn),
			Gating: gating,
		}
	}
}

// applyGate runs the gate over a parsed process and returns the failure it
// adds to one that exited 0; nil when it adds none.
func (e *Executor) applyGate(task Task, proc *ProcessResult, exitedZero bool) error {
	if e.gate == nil {
		return nil
	}
	decision := e.gate(task, proc)
	if !decision.Failed || !exitedZero {
		return nil
	}
	proc.ThresholdFailed = true
	return &ThresholdError{Reason: decision.Reason}
}
