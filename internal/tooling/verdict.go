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

// verdictInputs is the cache value: the precondition under which the stored pass
// remains true. A mismatch is a miss, so every part only has to be *sufficient*
// to notice a change, never to explain it.
func verdictInputs(members, guards []string, root string) string {
	parts := make([][]byte, 0, len(members)+len(guards)+2)
	parts = append(parts, []byte("m"))
	for _, part := range hashedPaths(members, root) {
		parts = append(parts, []byte(part))
	}
	parts = append(parts, []byte("g"))
	for _, part := range hashedPaths(guards, root) {
		parts = append(parts, []byte(part))
	}
	return hashutil.XXH3Multi(parts...)
}

// hashedPaths returns "<relpath>\x00<hash>" for each path, sorted. A missing
// file hashes to a sentinel so its disappearance is itself a change.
func hashedPaths(paths []string, root string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		hash := "(missing)"
		if data, readErr := os.ReadFile(p); readErr == nil {
			hash = hashutil.XXH3Hex(data)
		}
		out = append(out, filepath.ToSlash(rel)+"\x00"+hash)
	}
	sort.Strings(out)
	return out
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

// unitMembers returns every tracked file under unitDir, from the walk the
// planner already did. Deliberately wider than the operation's globs: a new
// .cts, a generated-but-tracked file or an edit in a nested sub-unit all count.
// Narrowing this risks a stale pass; widening it only costs a miss.
func (p *Planner) unitMembers(unitDir string) []string {
	if !p.cacheInitialized {
		return nil
	}
	if unitDir == p.rootPath {
		return p.cachedFiles
	}
	prefix := unitDir + string(filepath.Separator)
	out := make([]string, 0, len(p.cachedFiles))
	for _, f := range p.cachedFiles {
		if strings.HasPrefix(f, prefix) {
			out = append(out, f)
		}
	}
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
		if st, err := os.Stat(path); err != nil || st.IsDir() {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}

	// Walk up to the git root: a unit inherits whatever its ancestors declare.
	for dir := unitDir; ; dir = filepath.Dir(dir) {
		for _, name := range guardNames {
			add(filepath.Join(dir, name))
		}
		if dir == p.rootPath || !strings.HasPrefix(dir, p.rootPath) {
			break
		}
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
func coverageFor(task Task, sel Selection, unitMatched []string) Coverage {
	switch config.EffectiveArity(task.OpConfig) {
	case config.ArityNone, config.ArityDir:
		// argv does not depend on the selection, so the command is byte-identical
		// to the one a full run would issue.
		return CoverageComplete
	case config.ArityMany, config.ArityOne:
		if sel.Mode == SelectionAll {
			return CoverageComplete
		}
		if len(task.Files) >= len(unitMatched) {
			return CoverageComplete
		}
		return CoveragePartial
	}
	return CoveragePartial
}

// attachUnit records what a task's verdict is about, so the executor can cache
// it without having to work out any of this for itself.
func (p *Planner) attachUnit(task *Task, sel Selection, unitDir string, matched []string) {
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
	task.UnitMembers = p.unitMembers(unitDir)
	task.UnitGuards = p.unitGuards(*task, unitDir)
	task.Coverage = coverageFor(*task, sel, matched)
}

// verdictTTL is how long a stored pass is trusted. Beyond it the operation runs
// again, which bounds the staleness the guard list cannot rule out — an
// `extends` chain reaching outside the unit, a hand edit inside node_modules, a
// network-dependent verdict.
func (e *Executor) verdictTTL() time.Duration {
	return time.Duration(env.GetUnitCacheTTLMinutes()) * time.Minute
}

// verdictKeys returns the cache identity and input hash for a task, and whether
// the verdict cache applies to it at all.
func (e *Executor) verdictKeys(task Task) (key, inputs string, ok bool) {
	if e.cache == nil || config.InferGranularity(task.OpConfig) == config.GranularityFile {
		return "", "", false
	}
	if task.OpConfig.Cache != nil && !*task.OpConfig.Cache {
		return "", "", false
	}
	key = verdictIdentity(task, task.UnitDir)
	inputs = verdictInputs(task.UnitMembers, task.UnitGuards, e.rootPath)
	return key, inputs, true
}

// recordVerdict stores a pass, but only for a run that actually covered its
// unit. Coverage comes from the planner; the executor cannot tell a narrowed run
// from a full one and must not guess.
func (e *Executor) recordVerdict(task Task) {
	key, inputs, ok := e.verdictKeys(task)
	if !ok || task.Coverage != CoverageComplete {
		return
	}
	e.cache.AfterVerdict(key, cache.VerdictEntry{
		Tool:      task.ToolName,
		Op:        string(task.Operation),
		UnitDir:   task.UnitDir,
		InputHash: inputs,
		Members:   len(task.UnitMembers),
	})
	// A fix that rewrote files makes any lint verdict for the same tool unsound,
	// mirroring what AfterFix already does per file.
	if task.Operation == config.OpFix {
		e.cache.InvalidateVerdicts(task.ToolName)
	}
}
