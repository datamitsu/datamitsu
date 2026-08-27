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

// verdictInputs is the cache value: the precondition under which the stored pass
// remains true. A mismatch is a miss, so every part only has to be *sufficient*
// to notice a change, never to explain it.
//
// The second return is the number of bytes read to produce it, which the caller
// attaches to its span; it is a by-product of work already done, so computing it
// costs nothing when tracing is off.
func verdictInputs(members, guards []string, root string) (string, int64) {
	parts := make([][]byte, 0, len(members)+len(guards)+2)
	parts = append(parts, []byte("m"))
	memberPaths, memberBytes := hashedPaths(members, root)
	for _, part := range memberPaths {
		parts = append(parts, []byte(part))
	}
	parts = append(parts, []byte("g"))
	guardPaths, guardBytes := hashedPaths(guards, root)
	for _, part := range guardPaths {
		parts = append(parts, []byte(part))
	}
	parts = append(parts, []byte("e"))
	for _, kv := range inheritedEnv() {
		parts = append(parts, []byte(kv))
	}
	bytesRead := memberBytes + guardBytes
	cntVerdictBytes.Add(bytesRead)
	return hashutil.XXH3Multi(parts...), bytesRead
}

// hashedPaths returns "<relpath>\x00<hash>" for each path, sorted, and the total
// number of bytes read. A missing file hashes to a sentinel so its
// disappearance is itself a change.
func hashedPaths(paths []string, root string) ([]string, int64) {
	out := make([]string, 0, len(paths))
	var bytesRead int64
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		hash := "(missing)"
		if data, readErr := os.ReadFile(p); readErr == nil {
			hash = hashutil.XXH3Hex(data)
			bytesRead += int64(len(data))
		}
		out = append(out, filepath.ToSlash(rel)+"\x00"+hash)
	}
	sort.Strings(out)
	return out, bytesRead
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

// verdictKeys returns the cache identity and input hash for a task, and whether
// the verdict cache applies to it at all.
func (e *Executor) verdictKeys(task Task) (key, inputs string, ok bool) {
	key, inputs, _, ok = e.verdictKeysMeasured(task)
	return key, inputs, ok
}

// verdictKeysMeasured is verdictKeys plus the number of bytes it read, which the
// executor attaches to the verdictKeys span so the cost of deciding to skip a
// tool can be compared against the cost of running it.
func (e *Executor) verdictKeysMeasured(task Task) (key, inputs string, bytesRead int64, ok bool) {
	granularity := config.InferGranularity(task.OpConfig)
	if e.cache == nil || granularity == config.GranularityFile {
		return "", "", 0, false
	}
	if task.OpConfig.Cache != nil && !*task.OpConfig.Cache {
		return "", "", 0, false
	}
	// repo granularity is opt-in, not opt-out: the input vector degenerates to a
	// content hash of every tracked file, which costs more than most of these
	// tools do and hits almost never — the repository changes between runs, that
	// is why you ran it. Declaring cache: true claims the tool is a closed world.
	if granularity == config.GranularityRepo && (task.OpConfig.Cache == nil || !*task.OpConfig.Cache) {
		return "", "", 0, false
	}
	// No members means the input vector is constant, so the verdict could never
	// mismatch — a permanent pass. Reachable when a per-file-scope operation
	// declares granularity "unit": the planner has no unit to describe.
	if len(task.UnitMembers) == 0 {
		return "", "", 0, false
	}
	key = verdictIdentity(task, task.UnitDir)
	inputs, bytesRead = verdictInputs(task.UnitMembers, task.UnitGuards, e.rootPath)
	return key, inputs, bytesRead, true
}

// recordVerdict stores a pass, but only for a run that actually covered its
// unit. Coverage comes from the planner; the executor cannot tell a narrowed run
// from a full one and must not guess.
func (e *Executor) recordVerdict(task Task, key, inputs string, ok bool) {
	if !ok || task.Coverage != CoverageComplete {
		return
	}

	// The inputs were hashed before the tool ran. If they moved underneath it —
	// an editor save, a concurrent build, or the tool itself rewriting files —
	// the pass belongs to a state that no longer exists.
	after, _ := verdictInputs(task.UnitMembers, task.UnitGuards, e.rootPath)
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
