package tooling

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/trace"

	"go.uber.org/zap"
)

// guardNames are files outside a unit that can still change its verdict. Fixed
// in Go rather than derived from projectTypes: those markers exist to *detect* a
// project (typescript-project matches the literal tsconfig.json, never
// tsconfig.base.json), and a lock file is usually a marker of some other type
// entirely, so a tool would never see it.
var guardNames = []string{
	"tsconfig.json", "tsconfig.base.json", "jsconfig.json",
	"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs", "eslint.config.ts",
	".eslintrc.json", ".oxlintrc.json", "prettier.config.mjs", "prettier.config.js",
	".golangci.yaml", ".golangci.yml",
	"package.json", "go.mod", "go.work", "Cargo.toml", "pyproject.toml", "Chart.yaml",
	".editorconfig",
	"pnpm-lock.yaml", "package-lock.json", "yarn.lock",
	"go.sum", "go.work.sum", "uv.lock", "Cargo.lock", "poetry.lock",
}

// verdictIdentity is the cache key: what makes two runs the same question.
//
// Args are hashed raw, before placeholder expansion. {cwd}/{root}/{toolCache}
// are deterministic functions of unitDir and the root, both already in the
// vector, so expanding first would only bake absolute paths in and orphan every
// entry when the repository moves.
func verdictIdentity(task Task, unitDirRel string) string {
	parts := make([][]byte, 0, 6+len(task.OpConfig.Args)+len(task.OpConfig.Env))
	parts = append(parts,
		[]byte("dmv1"),
		[]byte(task.ToolName),
		[]byte(task.Operation),
		[]byte(unitDirRel),
		[]byte(config.InferGranularity(task.OpConfig)),
		[]byte(config.EffectiveArity(task.OpConfig)),
	)
	for _, arg := range task.OpConfig.Args {
		parts = append(parts, []byte(arg))
	}
	for _, kv := range sortedEnv(task.OpConfig.Env) {
		parts = append(parts, []byte(kv))
	}
	return hashutil.XXH3Multi(parts...)
}

// envPrefixes are the inherited environment variables that can change a tool's
// answer. Tools inherit the whole environment (executor.go mergeEnvLayers), so
// without this GOFLAGS=-tags=integration would change golangci-lint's package
// graph with no effect on the key. Hashing the whole environment instead is not
// an option: TERM, session ids and TMPDIR would prevent every hit.
var envPrefixes = []string{
	"GO", "CARGO", "RUST", "NODE_", "NPM_", "PYTHON", "PIP_", "UV_",
	"JAVA_", "TS_", "ESLINT_", "RUFF_", "TF_", "TFLINT_",
}

// inheritedEnv returns the allowlisted environment as sorted k=v pairs.
func inheritedEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		for _, prefix := range envPrefixes {
			if strings.HasPrefix(name, prefix) {
				out = append(out, kv)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// cntVerdictBytes is the volume the verdict cache reads to decide whether to
// skip a tool. Paired with the tool's own spawn duration it is the whole trade:
// a tool that runs in 40 ms over a unit whose bytes take 60 ms to hash is not
// being helped by the cache.
var cntVerdictBytes = trace.NewCounter("cache.verdict_bytes_hashed")

// pathState is one input as the pre-run pass saw it: the entry that went into
// the hash, plus the stat the post-run probe compares against. Recording the
// stat is free — reading the file already had to fstat it.
type pathState struct {
	path  string
	entry string // "<relpath>\x00<hash>"
	size  int64
	mod   time.Time
	ident fileIdent // the part of the stat a writer cannot restore
	read  bool      // the file was opened and hashed; false means the sentinel
}

// verdictSnapshot is the pre-run input vector plus everything needed to decide,
// after the run, whether any of it moved — without reading it all again.
type verdictSnapshot struct {
	inputs  string
	root    string
	members []pathState
	guards  []pathState
	// taken is stamped *before* the first read, so any write that could still
	// hide inside an mtime tick is one this snapshot flags as unsafe.
	taken time.Time
}

// hash is the pre-run input vector, and "" for a task the cache does not apply
// to — callers pass it straight to the cache, which treats "" as a miss.
func (s *verdictSnapshot) hash() string {
	if s == nil {
		return ""
	}
	return s.inputs
}

// mtimeGranularity is the coarsest modification-time resolution this code will
// assume it might be running on (FAT stores two-second ticks). A file modified
// within one tick of the pre-run pass cannot be cleared by a stat comparison at
// all, because a later write could land in the same tick, so the probe re-hashes
// it instead of trusting the stat.
const mtimeGranularity = 2 * time.Second

// verdictInputs is the cache value: the precondition under which the stored pass
// remains true. A mismatch is a miss, so every part only has to be *sufficient*
// to notice a change, never to explain it.
//
// It reads every path itself. This is the second pass an OpFix run pays for, so
// it deliberately does not *consult* the content memo: a fixer rewrites the
// files it just read, which is exactly the write a stat-validated memo cannot
// see. It does overwrite what it finds there — the pre-fix hashes are precisely
// the entries a later task in the same process must not be handed.
func verdictInputs(members, guards []string, root string) string {
	snap, _ := verdictSnapshotMode(members, guards, root, contentMemo, memoRewrite)
	return snap.inputs
}

// verdictSnapshotOf is verdictInputs plus the per-path stats, which is what lets
// recordVerdict check the inputs again for the price of a stat per file rather
// than a whole second read of the unit. It is the pre-run pass, so it shares the
// process-scoped content memo with every other task of the run.
func verdictSnapshotOf(members, guards []string, root string) (*verdictSnapshot, int64) {
	return verdictSnapshotMode(members, guards, root, contentMemo, memoShared)
}

// verdictSnapshotMode is verdictSnapshotOf against an explicit memo and memo
// mode; a nil memo reads every path.
func verdictSnapshotMode(members, guards []string, root string, memo *hashMemo, mode memoMode) (*verdictSnapshot, int64) {
	snap := &verdictSnapshot{root: root, taken: time.Now()}
	memberStates, memberBytes := hashedStates(members, root, memo, mode)
	guardStates, guardBytes := hashedStates(guards, root, memo, mode)
	snap.members, snap.guards = memberStates, guardStates
	snap.inputs = hashStates(memberStates, guardStates)

	bytesRead := memberBytes + guardBytes
	cntVerdictBytes.Add(bytesRead)
	return snap, bytesRead
}

// refresh recomputes the input vector after the run, re-hashing only the paths
// whose size or modification time moved. Everything else is answered by a stat.
func (s *verdictSnapshot) refresh() (string, int64) {
	members, memberBytes := s.refreshStates(s.members)
	guards, guardBytes := s.refreshStates(s.guards)

	bytesRead := memberBytes + guardBytes
	cntVerdictBytes.Add(bytesRead)
	return hashStates(members, guards), bytesRead
}

func (s *verdictSnapshot) refreshStates(prev []pathState) ([]pathState, int64) {
	out := make([]pathState, len(prev))
	var bytesRead int64
	for i, st := range prev {
		if s.settled(st) {
			out[i] = st
			continue
		}
		// nil memo: this is the check that the pre-run pass moved, and answering
		// it from the memo that pass filled would compare a value against itself.
		fresh, n := hashedState(st.path, s.root, nil, memoShared)
		out[i], bytesRead = fresh, bytesRead+n
	}
	return out, bytesRead
}

// settled answers "can a stat alone prove this path is byte-identical to what
// the pre-run pass hashed?". Every uncertain answer is false, which costs a
// re-hash; there is no case in which it may guess true.
func (s *verdictSnapshot) settled(st pathState) bool {
	fi, err := os.Stat(st.path)
	if err != nil {
		// Still absent, still the sentinel. If it *was* read, it has vanished —
		// that is a change, and re-hashing is what records it.
		return !st.read
	}
	if fi.IsDir() || !st.read {
		return false
	}
	if fi.Size() != st.size || !fi.ModTime().Equal(st.mod) {
		return false
	}
	// A rewrite that restores the original mtime — `rsync -a`, `cp -p`, an
	// archive extraction — leaves size and mtime untouched, and no anchoring of
	// the tick guard can see it. The inode-change time can: the write moves it
	// and the restoring utimes call moves it again. Where the platform reports no
	// change time (known == false) nothing here can rule that rewrite out, so the
	// path is re-hashed rather than trusted.
	fresh := identOf(fi)
	if !fresh.known || fresh != st.ident {
		return false
	}
	// Unchanged stat, but a file last modified within a tick of the pre-run pass
	// could have been rewritten at the same length during the run and still show
	// this mtime. Only a re-hash can tell.
	return !st.mod.After(s.taken.Add(-mtimeGranularity))
}

// hashStates folds the member and guard entries, plus the allowlisted
// environment, into the input hash. Entries are sorted so the vector does not
// depend on the order the planner happened to collect paths in.
func hashStates(members, guards []pathState) string {
	parts := make([][]byte, 0, len(members)+len(guards)+3)
	parts = append(parts, []byte("m"))
	for _, entry := range sortedEntries(members) {
		parts = append(parts, []byte(entry))
	}
	parts = append(parts, []byte("g"))
	for _, entry := range sortedEntries(guards) {
		parts = append(parts, []byte(entry))
	}
	parts = append(parts, []byte("e"))
	for _, kv := range inheritedEnv() {
		parts = append(parts, []byte(kv))
	}
	return hashutil.XXH3Multi(parts...)
}

func sortedEntries(states []pathState) []string {
	out := make([]string, 0, len(states))
	for _, st := range states {
		out = append(out, st.entry)
	}
	sort.Strings(out)
	return out
}

// hashedStates reads every path in order. A missing file hashes to a sentinel,
// so its disappearance is itself a change.
func hashedStates(paths []string, root string, memo *hashMemo, mode memoMode) ([]pathState, int64) {
	out := make([]pathState, 0, len(paths))
	var bytesRead int64
	for _, p := range paths {
		st, n := hashedState(p, root, memo, mode)
		out, bytesRead = append(out, st), bytesRead+n
	}
	return out, bytesRead
}

// hashedState reads one path and records both its content hash and the stat that
// produced it. The stat comes from the open handle, so it describes the bytes
// that were actually hashed and not a later state of the path.
//
// With a memo in memoShared mode, a path whose stat still matches an entry the
// memo took under is answered without reading it; the entry's own validity check
// is in lookup. In memoRewrite mode the bytes are always read, and what they
// hash to replaces whatever the memo held for that path.
func hashedState(p, root string, memo *hashMemo, mode memoMode) (pathState, int64) {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		rel = p
	}
	st := pathState{path: p, entry: filepath.ToSlash(rel) + "\x00(missing)"}

	if memo != nil && mode == memoShared {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			ident := identOf(fi)
			if hash, ok := memo.lookup(p, fi.Size(), fi.ModTime(), ident); ok {
				st.entry = filepath.ToSlash(rel) + "\x00" + hash
				st.size, st.mod, st.ident, st.read = fi.Size(), fi.ModTime(), ident, true
				return st, 0
			}
		}
	}

	// Stamped before the read, so an entry can be compared against the tick its
	// own bytes were taken in rather than against whenever it is looked up.
	taken := time.Now()

	f, err := os.Open(p)
	if err != nil {
		return st, 0
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		return st, 0
	}
	// Streamed from the open handle, so the hash and the stat that guards it come
	// from the same file description without materializing the file in memory.
	hash, err := hashutil.XXH3Reader(f)
	if err != nil {
		return st, 0
	}
	ident := identOf(fi)
	st.entry = filepath.ToSlash(rel) + "\x00" + hash
	st.size, st.mod, st.ident, st.read = fi.Size(), fi.ModTime(), ident, true
	memo.store(p, hash, fi.Size(), fi.ModTime(), ident, taken)
	return st, fi.Size()
}

func sortedEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// Verdict-planning counters. attachUnit runs per task and each of its three
// steps walks the repository file list again, or stats an ancestor chain, so the
// call counts are what expose the tasks × files product.
var (
	cntGuardStat       = trace.NewCounter("plan.unit.guard_stats")
	cntGuardStatMiss   = trace.NewCounter("plan.unit.guard_stats_enoent")
	cntUnitMembersScan = trace.NewCounter("plan.unit.member_scans")
)

// unitMembers returns every tracked file under unitDir, from the walk the
// planner already did. Deliberately wider than the operation's globs: a new
// .cts, a generated-but-tracked file or an edit in a nested sub-unit all count.
// Narrowing this risks a stale pass; widening it only costs a miss.
func (p *Planner) unitMembers(unitDir string) []string {
	cntUnitMembersScan.Add(1)

	if !p.cacheInitialized {
		return nil
	}
	if unitDir == p.rootPath {
		return p.cachedFiles
	}

	// Memoized per unit directory. The scan is a full pass over every tracked
	// file in the repository, and every tool planning a task in the same
	// directory — six of them for a typical TypeScript package — produced the
	// identical list. The result is read-only for callers (it ends up in
	// Task.UnitMembers, which is only ever ranged over), exactly as the root
	// case above has always shared p.cachedFiles.
	p.unitCacheMu.Lock()
	if cached, ok := p.memberCache[unitDir]; ok {
		p.unitCacheMu.Unlock()
		return cached
	}
	p.unitCacheMu.Unlock()

	prefix := unitDir + string(filepath.Separator)
	out := make([]string, 0, len(p.cachedFiles))
	for _, f := range p.cachedFiles {
		if strings.HasPrefix(f, prefix) {
			out = append(out, f)
		}
	}

	p.unitCacheMu.Lock()
	if p.memberCache == nil {
		p.memberCache = make(map[string][]string)
	}
	p.memberCache[unitDir] = out
	p.unitCacheMu.Unlock()
	return out
}

// ancestorGuards returns the guard files that exist on the chain from unitDir up
// to the repository root.
//
// Memoized per directory: the walk stats every name in guardNames at every
// ancestor level, the overwhelming majority of which do not exist, and the
// answer depends only on the directory — not on the task. Without the memo a
// repository with sixty packages and six per-project tools paid the same few
// hundred failing stats sixty times over.
func (p *Planner) ancestorGuards(unitDir string) []string {
	p.unitCacheMu.Lock()
	if cached, ok := p.guardCache[unitDir]; ok {
		p.unitCacheMu.Unlock()
		return cached
	}
	p.unitCacheMu.Unlock()

	var out []string
	for dir := unitDir; ; dir = filepath.Dir(dir) {
		for _, name := range guardNames {
			path := filepath.Join(dir, name)
			cntGuardStat.Add(1)
			st, err := os.Stat(path)
			if err != nil || st.IsDir() {
				cntGuardStatMiss.Add(1)
				continue
			}
			out = append(out, path)
		}
		if dir == p.rootPath || !strings.HasPrefix(dir, p.rootPath) {
			break
		}
	}

	p.unitCacheMu.Lock()
	if p.guardCache == nil {
		p.guardCache = make(map[string][]string)
	}
	p.guardCache[unitDir] = out
	p.unitCacheMu.Unlock()
	return out
}

// unitGuards collects the inputs outside unitDir that can still change the
// verdict: named config files along the ancestor chain, plus any argument that
// expands to an existing file.
func (p *Planner) unitGuards(task Task, unitDir string) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(path string) {
		if _, dup := seen[path]; dup {
			return
		}
		cntGuardStat.Add(1)
		st, err := os.Stat(path)
		if err != nil || st.IsDir() {
			cntGuardStatMiss.Add(1)
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}

	// Walk up to the git root: a unit inherits whatever its ancestors declare.
	// The chain depends only on the directory, so it is resolved once and shared
	// by every task rooted there; the two sources below are per task.
	for _, path := range p.ancestorGuards(unitDir) {
		seen[path] = struct{}{}
		out = append(out, path)
	}

	// Anything the args point at is a declared input by construction. Excludes
	// the tool cache: that is an *output*, and folding an output into a
	// precondition guarantees a permanent miss.
	toolCache := filepath.Join(p.rootPath, ".datamitsu-cache")
	for _, arg := range task.OpConfig.Args {
		expanded := strings.ReplaceAll(arg, "{root}", p.rootPath)
		expanded = strings.ReplaceAll(expanded, "{cwd}", unitDir)
		if !strings.Contains(expanded, string(filepath.Separator)) {
			continue
		}
		if strings.Contains(expanded, "{") || strings.HasPrefix(expanded, toolCache) {
			continue
		}
		if filepath.IsAbs(expanded) {
			add(expanded)
		}
	}

	// invalidateOn names extra inputs for this operation. Resolved against the
	// unit and every ancestor up to the git root — without the ancestor walk a
	// monorepo package could not name a config that lives above it, which is
	// where they usually live.
	for _, pattern := range task.OpConfig.InvalidateOn {
		for dir := unitDir; ; dir = filepath.Dir(dir) {
			add(filepath.Join(dir, pattern))
			if dir == p.rootPath || !strings.HasPrefix(dir, p.rootPath) {
				break
			}
		}
	}

	sort.Strings(out)
	return out
}

// coverageFor decides whether a planned task covers its whole unit. Only the
// planner can answer this: the executor sees one task and cannot tell a narrowed
// run from a full one, and a run that guessed would record a verdict it did not
// earn.
//
// The denominator is derived here, from UnitMembers, and is never passed in. An
// earlier version took it as a parameter and every caller passed the task's own
// file list, making the test `len(files) >= len(matched)` a tautology: partial
// was unreachable, so a one-file run stamped a whole-unit pass and the next full
// run hit that verdict and skipped — the exact defect this cache exists to
// remove, rebuilt inside it.
func (p *Planner) coverageFor(task Task) Coverage {
	switch config.EffectiveArity(task.OpConfig) {
	case config.ArityNone, config.ArityDir:
		// argv does not depend on the selection, so the command is byte-identical
		// to the one a full run would issue.
		return CoverageComplete
	case config.ArityMany, config.ArityOne:
		// What a full run would have handed this unit, computed from members
		// rather than from the selection.
		full := p.filterFilesByIgnore(task.ToolName,
			p.excludeFilesByGlobs(
				p.filterFilesByGlobs(task.UnitMembers, task.OpConfig.Globs),
				task.OpConfig.ExcludeGlobs))

		got := make(map[string]struct{}, len(task.Files))
		for _, f := range task.Files {
			got[f] = struct{}{}
		}
		for _, f := range full {
			if _, ok := got[f]; !ok {
				return CoveragePartial
			}
		}
		return CoverageComplete
	}
	return CoveragePartial
}

// attachUnit records what a task's verdict is about, so the executor can cache
// it without having to work out any of this for itself.
func (p *Planner) attachUnit(task *Task, unitDir string) {
	attachSpan := trace.Start(trace.CatPlan, "attachUnit")
	defer func() {
		attachSpan.EndWith(
			trace.A("tool", task.ToolName),
			trace.A("members", len(task.UnitMembers)),
			trace.A("guards", len(task.UnitGuards)),
		)
	}()

	// A file-granularity operation has no unit beyond the files it was given:
	// each file's verdict stands alone, which is what the per-file cache already
	// records.
	if config.InferGranularity(task.OpConfig) == config.GranularityFile {
		task.Coverage = CoverageComplete
		return
	}

	rel, err := filepath.Rel(p.rootPath, unitDir)
	if err != nil || rel == "." {
		rel = ""
	}
	task.UnitDir = filepath.ToSlash(rel)

	membersSpan := trace.Start(trace.CatPlan, "unitMembers")
	task.UnitMembers = p.unitMembers(unitDir)
	membersSpan.EndWith(trace.A("members", len(task.UnitMembers)))

	guardsSpan := trace.Start(trace.CatPlan, "unitGuards")
	task.UnitGuards = p.unitGuards(*task, unitDir)
	guardsSpan.EndWith(trace.A("guards", len(task.UnitGuards)))

	coverageSpan := trace.Start(trace.CatPlan, "coverageFor")
	task.Coverage = p.coverageFor(*task)
	coverageSpan.End()
}

// verdictTTL is how long a stored pass is trusted. Beyond it the operation runs
// again, which bounds the staleness the guard list cannot rule out — an
// `extends` chain reaching outside the unit, a hand edit inside node_modules, a
// network-dependent verdict.
func (e *Executor) verdictTTL() time.Duration {
	eff, err := runtimeconfig.Get()
	if err != nil {
		// Init() runs from cobra.OnInitialize; a caller that skipped it (a test,
		// an embedded use) gets the compile-time default rather than no cache.
		return time.Duration(env.GetUnitCacheTTLMinutes()) * time.Minute
	}
	return time.Duration(eff.UnitCacheTTLMinutes) * time.Minute
}

// verdictKeys returns the cache identity and input hash for a task, whether the
// verdict cache applies to it at all, and the number of bytes it read — which
// the executor attaches to the verdictKeys span so the cost of deciding to skip
// a tool can be compared against the cost of running it.
func (e *Executor) verdictKeys(task Task) (key string, snap *verdictSnapshot, bytesRead int64, ok bool) {
	granularity := config.InferGranularity(task.OpConfig)
	if e.cache == nil || granularity == config.GranularityFile {
		return "", nil, 0, false
	}
	if task.OpConfig.Cache != nil && !*task.OpConfig.Cache {
		return "", nil, 0, false
	}
	// repo granularity is opt-in, not opt-out: the input vector degenerates to a
	// content hash of every tracked file, which costs more than most of these
	// tools do and hits almost never — the repository changes between runs, that
	// is why you ran it. Declaring cache: true claims the tool is a closed world.
	if granularity == config.GranularityRepo && (task.OpConfig.Cache == nil || !*task.OpConfig.Cache) {
		return "", nil, 0, false
	}
	// No members means the input vector is constant, so the verdict could never
	// mismatch — a permanent pass. Reachable when a per-file-scope operation
	// declares granularity "unit": the planner has no unit to describe.
	if len(task.UnitMembers) == 0 {
		return "", nil, 0, false
	}
	key = verdictIdentity(task, task.UnitDir)
	snap, bytesRead = verdictSnapshotOf(task.UnitMembers, task.UnitGuards, e.rootPath)
	return key, snap, bytesRead, true
}

// recordVerdict stores a pass, but only for a run that actually covered its
// unit. Coverage comes from the planner; the executor cannot tell a narrowed run
// from a full one and must not guess.
func (e *Executor) recordVerdict(task Task, key string, snap *verdictSnapshot, ok bool) {
	if !ok || snap == nil || task.Coverage != CoverageComplete {
		return
	}
	inputs := snap.inputs

	// The inputs were hashed before the tool ran. If they moved underneath it —
	// an editor save, a concurrent build, or the tool itself rewriting files —
	// the pass belongs to a state that no longer exists.
	//
	// A read-only operation gets there by stat: nothing it did can have rewritten
	// a file at the same length and mtime, and the snapshot re-hashes every path
	// whose stat is inconclusive. A fix rewrites files by design — that is exactly
	// the write an mtime tick can hide — so it pays for the full second pass.
	var after string
	if task.Operation == config.OpFix {
		after = verdictInputs(task.UnitMembers, task.UnitGuards, e.rootPath)
	} else {
		after, _ = snap.refresh()
	}
	if after != inputs {
		if task.Operation != config.OpFix {
			// A read-only operation that saw shifting inputs proves nothing.
			log.Debug("inputs changed during the run; not recording a verdict",
				zap.String("tool", task.ToolName), zap.String("unit", task.UnitDir))
			return
		}
		// A fix is expected to change its inputs — that is its job — so record
		// the state it produced rather than the one it started from.
		inputs = after
	}
	e.cache.AfterVerdict(key, cache.VerdictEntry{
		Tool:      task.ToolName,
		Op:        string(task.Operation),
		UnitDir:   task.UnitDir,
		InputHash: inputs,
		Members:   len(task.UnitMembers),
	})
	// A fix that rewrote files makes the matching lint verdict unsound. Only that
	// sibling: dropping every verdict for the tool would delete the one just
	// written, and no fix operation could ever hit the cache again.
	//
	// The sibling carries the lint operation's own config. Relabelling the fix
	// task was not enough — verdictIdentity hashes args, env, granularity and
	// arity, so a fix carrying --write and a lint carrying --check hash
	// differently, and the delete landed on a key no lint had ever written while
	// the real lint verdict survived.
	if task.Operation == config.OpFix {
		lintOp, ok := task.Tool.Operations[config.OpLint]
		if !ok {
			return
		}
		sibling := task
		sibling.Operation = config.OpLint
		sibling.OpConfig = lintOp
		e.cache.DeleteVerdict(verdictIdentity(sibling, sibling.UnitDir))
	}
}
