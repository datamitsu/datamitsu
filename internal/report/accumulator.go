package report

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/textpos"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// ParserFacts looks up what a parser module described about a parser key,
// without loading the module; ok is false when the run never described it.
type ParserFacts func(module, parser string) (facts parsermanager.ParserFacts, ok bool)

// Options is what an Accumulator needs to know about the run it records.
type Options struct {
	// Root is the repository root every path is made relative to.
	Root   string
	Tools  config.MapOfTools
	Apps   binmanager.MapOfApps
	FailOn config.Severity
	// Parsers answers for the parser modules the run described; nil when none
	// is configured.
	Parsers ParserFacts
	// Secrets are the values the run masks; an output tail cut through one
	// loses the part that could hold it.
	Secrets *Secrets
}

// Accumulator collects a run's operations as they happen. The runner feeds it
// from the executor's result callback and builds the Run once the last
// operation has ended. It is safe for concurrent use.
type Accumulator struct {
	opts Options

	mu  sync.Mutex
	ops []*OperationRecord
}

// NewAccumulator starts recording a run.
func NewAccumulator(opts Options) *Accumulator {
	return &Accumulator{opts: opts}
}

// OperationRecord collects one operation.
type OperationRecord struct {
	acc  *Accumulator
	name string
	ran  bool

	plan    *tooling.ExecutionPlan
	taskDir func(tooling.Task) string
	results map[string]tooling.ExecutionResult
	stopped []Cancel
	// pending counts, per tool, the planned tasks whose results have not
	// arrived; flushed marks the tools whose findings were handed out.
	pending map[string]int
	flushed map[string]bool

	success  bool
	duration int64
}

// BeginOperation records that an operation started with plan, whose task IDs
// the executor assigns in place. taskDir names the directory a task runs in,
// for the tasks the run never reached.
func (a *Accumulator) BeginOperation(name string, plan *tooling.ExecutionPlan, taskDir func(tooling.Task) string) *OperationRecord {
	rec := &OperationRecord{
		acc:     a,
		name:    name,
		ran:     true,
		plan:    plan,
		taskDir: taskDir,
		results: map[string]tooling.ExecutionResult{},
		pending: map[string]int{},
		flushed: map[string]bool{},
	}
	if plan != nil {
		for _, group := range plan.Groups {
			for _, task := range group.Tasks {
				rec.pending[task.ToolName]++
			}
		}
	}
	a.mu.Lock()
	a.ops = append(a.ops, rec)
	a.mu.Unlock()
	return rec
}

// NotRun records an operation the run never reached; one that began is left
// as it is.
func (a *Accumulator) NotRun(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, op := range a.ops {
		if op.name == name {
			return
		}
	}
	a.ops = append(a.ops, &OperationRecord{acc: a, name: name})
}

// ToolFinding is a finding with the task that reported it, as the event
// stream carries it.
type ToolFinding struct {
	TaskID  string
	Dir     string
	Finding Finding
}

// AddTask folds one task's result into the operation. When it is the last
// planned task of its tool to arrive, it returns every finding of that tool in
// the operation, with the fingerprints the report will hold: those depend on
// all of a tool's findings, so a tool's findings are final only once it has
// finished. Synthetic findings are not among them. Like every method of an
// OperationRecord it does nothing on a nil record, which is what a run that
// records nothing holds.
func (o *OperationRecord) AddTask(result tooling.ExecutionResult) []ToolFinding {
	if o == nil {
		return nil
	}
	o.acc.mu.Lock()
	defer o.acc.mu.Unlock()
	o.results[result.TaskID] = result
	o.pending[result.ToolName]--
	if o.pending[result.ToolName] > 0 {
		return nil
	}
	return o.flushTool(result.ToolName)
}

// Flush returns the findings of the tools whose last task never arrived — the
// run stopped before it — as AddTask would have.
func (o *OperationRecord) Flush() []ToolFinding {
	if o == nil {
		return nil
	}
	o.acc.mu.Lock()
	defer o.acc.mu.Unlock()
	names := make([]string, 0, len(o.pending))
	for name := range o.pending {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []ToolFinding
	for _, name := range names {
		out = append(out, o.flushTool(name)...)
	}
	return out
}

// Stopped records a task the run stopped: cancelled after it started, or never
// started.
func (o *OperationRecord) Stopped(c Cancel) {
	if o == nil {
		return
	}
	o.acc.mu.Lock()
	defer o.acc.mu.Unlock()
	o.stopped = append(o.stopped, c)
}

// Operation is the operation as a report lists it, built from what was
// recorded so far, for a consumer that shows an operation as soon as its
// tasks have ended; its success and duration are those End recorded, if it
// was called. It is not masked.
func (o *OperationRecord) Operation() Operation {
	if o == nil {
		return Operation{}
	}
	o.acc.mu.Lock()
	defer o.acc.mu.Unlock()
	return o.acc.buildOperation(o)
}

// End records whether the operation succeeded and how long its tools took, the
// "done in" of its footer.
func (o *OperationRecord) End(success bool, durationMs int64) {
	if o == nil {
		return
	}
	o.acc.mu.Lock()
	defer o.acc.mu.Unlock()
	o.success, o.duration = success, durationMs
}

func (o *OperationRecord) flushTool(name string) []ToolFinding {
	if o.flushed[name] || o.plan == nil {
		return nil
	}
	o.flushed[name] = true
	tr := o.acc.buildTool(o, name)
	var out []ToolFinding
	for _, inv := range tr.Invocations {
		for _, f := range inv.Findings {
			if f.Kind != kindSynthetic {
				out = append(out, ToolFinding{TaskID: inv.TaskID, Dir: inv.Dir, Finding: f})
			}
		}
	}
	return out
}

// BuildInfo is what the runner knows about the run as a whole.
type BuildInfo struct {
	Version       string
	Configuration string
	StartedAt     time.Time
	EndedAt       time.Time
	Selection     Selection
	FailFast      bool
	Exports       []Export
	CI            CIEnvironment
}

// Build turns what was recorded into a Run, sorted so that one input always
// gives the same document.
func (a *Accumulator) Build(info BuildInfo) *Run {
	a.mu.Lock()
	defer a.mu.Unlock()
	run := &Run{
		Schema:     SchemaVersion,
		Datamitsu:  Producer{Version: info.Version, Configuration: info.Configuration},
		StartedAt:  info.StartedAt.UTC(),
		EndedAt:    info.EndedAt.UTC(),
		Selection:  info.Selection,
		FailFast:   info.FailFast,
		Operations: make([]Operation, 0, len(a.ops)),
		Exports:    append([]Export{}, info.Exports...),
		CI:         info.CI,
	}
	names := make([]string, 0, len(a.ops))
	for _, op := range a.ops {
		run.Operations = append(run.Operations, a.buildOperation(op))
		names = append(names, op.name)
	}
	run.Selection.ExcludedTools = excludedTools(a.opts.Tools, names, run.Selection.Tools)
	judge(run, a.opts.Tools)
	return run
}

func (a *Accumulator) buildOperation(o *OperationRecord) Operation {
	op := Operation{
		Name:      o.name,
		Ran:       o.ran,
		Success:   o.success,
		Duration:  Millis(o.duration),
		Skipped:   []Skip{},
		Cancelled: append([]Cancel{}, o.stopped...),
		Tools:     []ToolRun{},
	}
	if o.plan == nil {
		return op
	}
	for _, s := range o.plan.Skipped {
		op.Skipped = append(op.Skipped, Skip{Tool: s.ToolName, Reason: s.Reason.String(), Detail: s.Detail})
	}
	sort.SliceStable(op.Skipped, func(i, j int) bool { return op.Skipped[i].Tool < op.Skipped[j].Tool })
	sort.SliceStable(op.Cancelled, func(i, j int) bool {
		a, b := op.Cancelled[i], op.Cancelled[j]
		if a.Tool != b.Tool {
			return a.Tool < b.Tool
		}
		if a.Dir != b.Dir {
			return a.Dir < b.Dir
		}
		return taskSeq(a.TaskID) < taskSeq(b.TaskID)
	})

	tools := map[string]*ToolRun{}
	for _, group := range o.plan.Groups {
		for _, task := range group.Tasks {
			if tools[task.ToolName] == nil {
				tools[task.ToolName] = a.buildTool(o, task.ToolName)
			}
		}
	}
	// A tool with no binary for this host did not run where it was asked to:
	// its run is listed, incomplete, so no consumer reads its absence as clean.
	for _, s := range o.plan.Skipped {
		if s.Reason != tooling.SkipReasonUnsupportedPlatform || tools[s.ToolName] != nil {
			continue
		}
		tr := a.newToolRun(o.name, s.ToolName)
		tr.Incomplete = []Reason{ReasonPlatformSkip}
		tools[s.ToolName] = tr
	}
	for _, tr := range tools {
		op.Tools = append(op.Tools, *tr)
	}
	sort.Slice(op.Tools, func(i, j int) bool { return op.Tools[i].Name < op.Tools[j].Name })
	return op
}

// buildTool is everything one tool did in an operation: an invocation per
// process of each of its planned tasks, in plan order, with its findings
// settled across all of them.
func (a *Accumulator) buildTool(o *OperationRecord, name string) *ToolRun {
	tr := a.newToolRun(o.name, name)
	for _, group := range o.plan.Groups {
		for _, task := range group.Tasks {
			if task.ToolName != name {
				continue
			}
			var result *tooling.ExecutionResult
			if r, ok := o.results[task.ID]; ok {
				result = &r
			}
			tr.Invocations = append(tr.Invocations, a.invocations(task, result, o.taskDir, tr)...)
		}
	}
	sortInvocations(tr.Invocations)
	settleFindings(tr)
	return tr
}

func (a *Accumulator) newToolRun(op, name string) *ToolRun {
	tool := a.opts.Tools[name]
	toolOp := tool.Operations[config.OperationType(op)]
	tr := &ToolRun{
		Name:        name,
		App:         a.appRef(toolOp.App),
		FailOn:      string(config.EffectiveFailOn(toolOp, a.opts.FailOn)),
		Invocations: []Invocation{},
	}
	tr.mayHoldSecrets = withholdsOutput(tool, a.opts.Parsers)
	if p := tool.OutputParser; p != nil {
		tr.Parser = &ParserRef{Module: p.Module, Parser: p.Parser}
		if a.opts.Parsers != nil {
			if facts, ok := a.opts.Parsers(p.Module, p.Parser); ok {
				tr.Parser.Version = facts.Version
				tr.Parser.Schema = facts.Schema
				tr.Parser.ColumnUnit = facts.Tool.ColumnUnit
				tr.GateActive = facts.Contract
				tr.Category = facts.Tool.Category
			}
		}
	}
	return tr
}

// withholdsOutput reports a tool whose output no output of a run may carry:
// one its parser module puts in the security category, whose output may be
// the secret it found, and one with a parser the run never described — its
// module did not load, or does not list the key — which may be one.
func withholdsOutput(tool config.Tool, parsers ParserFacts) bool {
	p := tool.OutputParser
	if p == nil {
		return false
	}
	if parsers == nil {
		return true
	}
	facts, ok := parsers(p.Module, p.Parser)
	return !ok || facts.Tool.Name == "" || facts.Tool.Category == categorySecurity
}

func (a *Accumulator) appRef(name string) AppRef {
	app, ok := a.opts.Apps[name]
	if !ok {
		return AppRef{Name: name}
	}
	info := binmanager.DescribeApp(name, app)
	return AppRef{Name: name, Kind: info.Type, Version: info.Version, OfficialURL: app.OfficialURL}
}

// invocations lists what one planned task did: one invocation per process it
// planned, one for the files a cache answered, or a single stand-in for a task
// that spawned nothing.
func (a *Accumulator) invocations(task tooling.Task, result *tooling.ExecutionResult, taskDir func(tooling.Task) string, tr *ToolRun) []Invocation {
	base := Invocation{
		TaskID:      task.ID,
		Scope:       string(task.OpConfig.Scope),
		Granularity: string(config.InferGranularity(task.OpConfig)),
		Arity:       string(config.EffectiveArity(task.OpConfig)),
		Coverage:    string(task.Coverage),
		WholeUnit:   tooling.WholeUnit(task),
		Files:       []FileResult{},
		Findings:    []Finding{},
	}
	if taskDir != nil {
		base.Dir = taskDir(task)
	}
	parsed := parsesOutput(task.Tool, task.OpConfig)

	if result == nil {
		inv := base
		inv.ID = task.ID + "#0"
		inv.State = string(tooling.ProcessNotStarted)
		inv.Extraction, inv.Provenance = string(tooling.ExtractionNone), provenanceNone
		for _, file := range plannedFiles(task) {
			inv.Files = append(inv.Files, FileResult{Path: a.rel(file), State: string(tooling.FileNotStarted)})
		}
		sortFiles(inv.Files)
		return []Invocation{inv}
	}

	base.Dir = result.RelativeDir
	base.WholeUnit = result.WholeUnit
	byFile := make(map[string]tooling.FileResult, len(result.FileResults))
	for _, fr := range result.FileResults {
		byFile[fr.File] = fr
	}
	claimed := map[string]bool{}
	var out []Invocation

	for _, proc := range result.Processes {
		inv := base
		inv.ID = proc.ID
		inv.State = string(proc.State)
		inv.ExitCode = proc.ExitCode
		inv.Success = proc.Success
		inv.FailureKind = failureKind(proc)
		inv.Duration = Millis(proc.DurationMs)
		inv.Extraction = string(proc.Extraction)
		inv.Provenance = provenanceOf(proc.Extraction)
		for _, file := range proc.Files {
			if fr, ok := byFile[file]; ok && !claimed[file] {
				claimed[file] = true
				inv.Files = append(inv.Files, a.fileResult(fr))
			}
		}
		shown := ShownOf(proc)
		for i, d := range proc.Diagnostics {
			f := a.finding(task.ToolName, d, tr)
			f.Shown = shown[i]
			inv.Findings = append(inv.Findings, f)
		}
		if f, ok := syntheticFinding(task.ToolName, proc, tr.Category); ok {
			inv.Findings = append(inv.Findings, f)
		}
		inv.OutputTail = outputTail(proc, tr.mayHoldSecrets, a.opts.Secrets.Values())
		out = append(out, inv)
	}
	// A process given no path answers for every file no other process and no
	// cache claimed.
	for i, proc := range result.Processes {
		if len(proc.Files) > 0 {
			continue
		}
		for _, fr := range result.FileResults {
			if !claimed[fr.File] && fr.State != tooling.FileCached {
				claimed[fr.File] = true
				out[i].Files = append(out[i].Files, a.fileResult(fr))
			}
		}
	}

	var rest []tooling.FileResult
	for _, fr := range result.FileResults {
		if !claimed[fr.File] {
			rest = append(rest, fr)
		}
	}
	if len(rest) > 0 || len(out) == 0 {
		inv := base
		inv.Extraction, inv.Provenance = string(tooling.ExtractionNone), provenanceNone
		if len(out) == 0 {
			inv.Duration = Millis(result.Duration)
		}
		state := standInState(result, rest)
		inv.State = state
		switch state {
		case string(tooling.FileCached), string(tooling.FileVerdictHit):
			inv.ID = task.ID + "#cached"
			if state == string(tooling.FileVerdictHit) {
				inv.ID = task.ID + "#verdict"
			}
			inv.Success = true
			if parsed {
				inv.Extraction, inv.Provenance = string(tooling.ExtractionParsedClean), provenanceParser
			}
		default:
			inv.ID = task.ID + "#0"
			switch state {
			case string(tooling.ProcessSetupFailed):
				inv.FailureKind = failureSetup
			case string(tooling.ProcessCancelled):
				inv.FailureKind = failureCancelled
			}
		}
		for _, fr := range rest {
			inv.Files = append(inv.Files, a.fileResult(fr))
		}
		out = append(out, inv)
	}
	for i := range out {
		sortFiles(out[i].Files)
		sortFindings(out[i].Findings)
	}
	return out
}

// standInState is the state of the invocation that stands in for what no
// process accounts for: the files a cache answered, or a task that spawned
// nothing.
func standInState(result *tooling.ExecutionResult, rest []tooling.FileResult) string {
	for _, fr := range rest {
		switch fr.State {
		case tooling.FileVerdictHit:
			return string(tooling.FileVerdictHit)
		case tooling.FileCached:
			return string(tooling.FileCached)
		case tooling.FileRan:
		case tooling.FileCancelled, tooling.FileNotStarted, tooling.FileSetupFailed:
			return string(fr.State)
		}
	}
	switch {
	case result.Cached:
		return string(tooling.FileCached)
	case result.IsCancelled() && result.Started():
		return string(tooling.ProcessCancelled)
	case result.IsCancelled():
		return string(tooling.ProcessNotStarted)
	case !result.Success:
		return string(tooling.ProcessSetupFailed)
	}
	return string(tooling.FileCached)
}

func (a *Accumulator) fileResult(fr tooling.FileResult) FileResult {
	return FileResult{Path: a.rel(fr.File), State: string(fr.State), Success: fr.Success, ExitCode: fr.ExitCode}
}

func (a *Accumulator) finding(tool string, d diagnostic.Diagnostic, tr *ToolRun) Finding {
	f := Finding{
		FingerprintBasis: BasisNone,
		Tool:             tool,
		Source:           d.Source,
		Code:             d.Code,
		RuleURL:          d.URL,
		Severity:         d.Severity.String(),
		Reported:         d.Reported,
		Gates:            d.Gates,
		Kind:             kindIssue,
		Message:          string(tooling.StripCSI([]byte(d.Message))),
		Provenance:       provenanceParser,
		Location: Location{
			Path:      a.rel(d.File),
			Row:       d.Row,
			EndRow:    d.EndRow,
			Col:       d.Col,
			EndCol:    d.EndCol,
			Precision: string(textpos.Unknown),
		},
	}
	f.lineHash = inputOf(tool, f.Location.Path, d).lineHash
	switch {
	case d.Anchor != nil:
		f.FingerprintBasis = d.Anchor.Basis
		f.Location.Chars, f.Location.Bytes, f.Location.Utf16 = d.Anchor.Chars, d.Anchor.Bytes, d.Anchor.UTF16
		f.Location.Precision = string(d.Anchor.Precision)
	case d.File != "":
		f.FingerprintBasis = BasisRow
	}
	if tr.Parser != nil {
		f.Location.Unit = tr.Parser.ColumnUnit
	}
	if tr.Category == categorySecurity {
		f.Kind = kindSecurity
	}
	return f
}

// settleFindings drops the findings of a tool that one of its invocations
// already reported — overlapping chunks, or units that share a file — and
// numbers the rest across all of its invocations, which is what makes two
// findings on one line of one file distinct however the tool's work was split.
func settleFindings(tr *ToolRun) {
	// The same finding may weigh more where it came from a process that
	// failed: the one listed is the strongest, in the invocation that reported
	// it, and the first of equals.
	best := map[fingerprintInput][2]int{}
	// A finding the terminal showed in any of its invocations is shown once,
	// wherever it is listed: what is shown does not depend on which duplicate
	// was kept.
	shown := map[fingerprintInput]bool{}
	for i, inv := range tr.Invocations {
		for j, f := range inv.Findings {
			if f.Kind == kindSynthetic {
				continue
			}
			input := f.input()
			shown[input] = shown[input] || f.Shown
			if pos, seen := best[input]; !seen || stronger(f, tr.Invocations[pos[0]].Findings[pos[1]]) {
				best[input] = [2]int{i, j}
			}
		}
	}
	var in []fingerprintInput
	var at [][2]int
	for i := range tr.Invocations {
		inv := &tr.Invocations[i]
		kept := inv.Findings[:0]
		for j, f := range inv.Findings {
			// A synthetic finding stands for its own invocation, never for
			// another's, however alike their failures read.
			if f.Kind != kindSynthetic && best[f.input()] != [2]int{i, j} {
				continue
			}
			if f.Kind != kindSynthetic {
				f.Shown = shown[f.input()]
			}
			kept = append(kept, f)
		}
		inv.Findings = kept
		for j, f := range inv.Findings {
			in = append(in, f.input())
			at = append(at, [2]int{i, j})
		}
	}
	for k, fp := range fingerprints(in) {
		tr.Invocations[at[k][0]].Findings[at[k][1]].Fingerprint = fp
	}
}

// stronger reports whether a says more than b: it gates where b does not, is
// reported where b is not, or is more severe.
func stronger(a, b Finding) bool {
	switch {
	case a.Gates != b.Gates:
		return a.Gates
	case a.Reported != b.Reported:
		return a.Reported
	}
	return config.Severity(a.Severity).Level() < config.Severity(b.Severity).Level()
}

// input is what the finding's fingerprint is computed from.
func (f Finding) input() fingerprintInput {
	return fingerprintInput{
		tool: f.Tool, code: f.Code, relPath: f.Location.Path, lineHash: f.lineHash,
		row: f.Location.Row, col: f.Location.Col, source: f.Source, message: f.Message,
	}
}

// rel is a path as every export writes it: relative to the repository root
// with "/", or unchanged outside it.
func (a *Accumulator) rel(path string) string {
	return RelPath(a.opts.Root, path)
}

// RelPath makes an absolute path relative to root with "/" separators. A path
// outside root, or one that cannot be made relative, is returned as is; an
// empty path stays empty.
func RelPath(root, path string) string {
	if path == "" || root == "" {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return path
	}
	return rel
}

// parsesOutput reports whether the output of op is read by the tool's parser:
// a formatter's stdout is file content, which no parser reads, so its cache
// hits replay an exit code, not a parsed-clean pass.
func parsesOutput(tool config.Tool, op config.ToolOperation) bool {
	return tool.OutputParser != nil && op.Output != config.ToolOutputStdout
}

func plannedFiles(task tooling.Task) []string {
	if tooling.WholeUnit(task) && len(task.UnitMembers) > 0 {
		return task.UnitMembers
	}
	return task.Files
}

func failureKind(proc tooling.ProcessResult) string {
	switch proc.State {
	case tooling.ProcessRan:
		switch {
		case proc.Success:
			return ""
		case proc.ThresholdFailed:
			return failureThreshold
		default:
			return failureExit
		}
	case tooling.ProcessCancelled:
		return failureCancelled
	case tooling.ProcessSetupFailed:
		return failureSetup
	case tooling.ProcessNotStarted:
	}
	return ""
}

func provenanceOf(e tooling.Extraction) string {
	if e == tooling.ExtractionParsedClean || e == tooling.ExtractionParsedFindings {
		return provenanceParser
	}
	return provenanceNone
}

// Values of Invocation.FailureKind, Finding.Kind, the provenances and the
// categories this package writes.
const (
	failureExit      = "exit"
	failureThreshold = "threshold"
	failureCancelled = "cancelled"
	failureSetup     = "setup"

	kindIssue     = "issue"
	kindSecurity  = "security"
	kindSynthetic = "synthetic"

	provenanceParser    = "parser"
	provenanceNone      = "none"
	provenanceSynthetic = "synthetic"

	categorySecurity = "security"
)

// taskSeq is the plan-order number a task ID ends with, "<tool>:<dir>:<seq>".
func taskSeq(taskID string) int {
	i := strings.LastIndexByte(taskID, ':')
	if i < 0 {
		return 0
	}
	n, _ := strconv.Atoi(taskID[i+1:])
	return n
}

// invocationOrder sorts invocations by their task's plan order, then by process
// number, with the stand-ins after a task's processes.
func invocationOrder(id string) (seq, n int) {
	task, suffix, _ := strings.Cut(id, "#")
	seq = taskSeq(task)
	switch suffix {
	case "cached":
		return seq, 1 << 30
	case "verdict":
		return seq, 1<<30 + 1
	}
	n, _ = strconv.Atoi(suffix)
	return seq, n
}

func sortInvocations(list []Invocation) {
	sort.SliceStable(list, func(i, j int) bool {
		si, ni := invocationOrder(list[i].ID)
		sj, nj := invocationOrder(list[j].ID)
		if si != sj {
			return si < sj
		}
		if ni != nj {
			return ni < nj
		}
		return list[i].ID < list[j].ID
	})
}

func sortFiles(files []FileResult) {
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool { return findingLess(fs[i], fs[j]) })
}

func findingLess(a, b Finding) bool {
	switch {
	case a.Location.Path != b.Location.Path:
		return a.Location.Path < b.Location.Path
	case a.Location.Row != b.Location.Row:
		return a.Location.Row < b.Location.Row
	case a.Location.Col != b.Location.Col:
		return a.Location.Col < b.Location.Col
	case a.Source != b.Source:
		return a.Source < b.Source
	case a.Code != b.Code:
		return a.Code < b.Code
	default:
		return a.Message < b.Message
	}
}
