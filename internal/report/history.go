package report

import (
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
)

// HistorySchema names the shape of a history line.
const HistorySchema = "datamitsu.history/1"

// HistoryLine is one run as --report history appends it: what it covered,
// whether it was complete, and per operation and tool its counts and
// durations. It holds no finding, no path and none of the paths a selection
// named, so a file of them is a trend that can be kept and shared.
type HistoryLine struct {
	Schema     string             `json:"schema"`
	StartedAt  time.Time          `json:"startedAt"`
	Datamitsu  Producer           `json:"datamitsu"`
	CI         HistoryCI          `json:"ci"`
	Selection  HistorySelection   `json:"selection"`
	Complete   bool               `json:"complete"`
	FailFast   bool               `json:"failFast"`
	Operations []HistoryOperation `json:"operations"`
}

// HistoryCI is the CI job a run ran in: its vendor, "" outside CI, and the
// commit and ref it built.
type HistoryCI struct {
	Vendor string `json:"vendor"`
	SHA    string `json:"sha"`
	Ref    string `json:"ref"`
}

// HistorySelection is what a run covered, without the paths it named.
type HistorySelection struct {
	// Mode is all, subtree, paths or empty.
	Mode       string `json:"mode"`
	FileScoped bool   `json:"fileScoped"`
	// ToolsFiltered is true for a run --tools narrowed.
	ToolsFiltered bool `json:"toolsFiltered"`
}

// HistoryOperation is one operation of a history line.
type HistoryOperation struct {
	Name     string        `json:"name"`
	Ran      bool          `json:"ran"`
	Success  bool          `json:"success"`
	Duration Millis        `json:"durationMs"`
	Tools    []HistoryTool `json:"tools"`
}

// HistoryTool is what one tool did in an operation, counted.
type HistoryTool struct {
	Name string `json:"name"`
	// Runs counts the processes that ran, Cached the files a cache answered
	// and Failed the invocations that failed on their own, on a finding at or
	// above failOn, or before they could run.
	Runs     int  `json:"runs"`
	Cached   int  `json:"cached"`
	Failed   int  `json:"failed"`
	Complete bool `json:"complete"`
	// Incomplete are the tool's reasons, so that a trend can tell a warm run
	// that replayed a pass nothing read (unparsed-cache-hit) from a tool that
	// stopped covering what it covered.
	Incomplete []Reason    `json:"incomplete"`
	Findings   LevelCounts `json:"findings"`
}

// LevelCounts counts findings per level.
type LevelCounts struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
	Hint    int `json:"hint"`
}

// Add counts one finding of level severity; any other level is not counted.
func (c *LevelCounts) Add(severity string) {
	switch config.Severity(severity) {
	case config.SeverityError:
		c.Error++
	case config.SeverityWarning:
		c.Warning++
	case config.SeverityInfo:
		c.Info++
	case config.SeverityHint:
		c.Hint++
	}
}

// History is run as one history line. A synthetic finding is not counted: its
// tool is counted as failed instead.
func History(run *Run) HistoryLine {
	line := HistoryLine{
		Schema:    HistorySchema,
		StartedAt: run.StartedAt,
		Datamitsu: run.Datamitsu,
		CI:        HistoryCI{Vendor: run.CI.Vendor, SHA: run.CI.SHA, Ref: run.CI.Ref},
		Selection: HistorySelection{
			Mode:          run.Selection.Mode,
			FileScoped:    run.Selection.FileScoped,
			ToolsFiltered: len(run.Selection.Tools) > 0,
		},
		Complete:   run.Complete,
		FailFast:   run.FailFast,
		Operations: make([]HistoryOperation, 0, len(run.Operations)),
	}
	for _, op := range run.Operations {
		hop := HistoryOperation{Name: op.Name, Ran: op.Ran, Success: op.Success, Duration: op.Duration, Tools: []HistoryTool{}}
		for _, tr := range op.Tools {
			hop.Tools = append(hop.Tools, historyTool(tr))
		}
		line.Operations = append(line.Operations, hop)
	}
	return line
}

func historyTool(tr ToolRun) HistoryTool {
	ht := HistoryTool{Name: tr.Name, Complete: tr.Complete, Incomplete: append([]Reason{}, tr.Incomplete...)}
	for _, inv := range tr.Invocations {
		switch inv.State {
		case "ran":
			ht.Runs++
		case "cached", "verdict-hit":
			ht.Cached += len(inv.Files)
		}
		switch inv.FailureKind {
		case FailureExit, FailureThreshold, FailureSetup:
			ht.Failed++
		}
		for _, f := range inv.Findings {
			if f.Kind != kindSynthetic {
				ht.Findings.Add(f.Severity)
			}
		}
	}
	return ht
}
