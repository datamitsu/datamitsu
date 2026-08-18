# Plan: Tool invocation granularity — selection, granularity, arity, and the verdict cache

**Status:** design-doc v2 — **all decisions confirmed by the owner and closed**, including the
release sequence and the completeness flag (§11). Ready to hand off for implementation.
**Date:** 2026-08-18.
**Related:** `internal/tooling/planner.go`, `internal/tooling/executor.go`, `internal/cache`,
`internal/lsp`, `internal/config`, and the wrapper config that ships as
`@shibanet0/datamitsu-config`.

> **Why this exists.** Today the core decides _where_ a tool runs (`scope`) but has no model of
> _what a tool needs in order to answer correctly_. Every consequence of that gap is a silent one:
> repository-scoped tools vanish without a trace when invoked from a subdirectory, unit-scoped
> tools report a green tick without executing, and the LSP has grown a second, divergent
> granularity engine that fabricates argv paths. This plan closes the gap with three orthogonal
> concepts and ships the cache that makes them correct — not as a follow-up version.

---

## 0. The idea in one paragraph

A run has a **selection**: the whole repository, a subtree, or the paths you named. Every tool
operation declares (or, in every case in the current wrapper, _implies_) two independent facts:
**granularity** — the smallest set of input files on which its verdict is complete (`file`, `unit`,
`repo`) — and **arity** — the shape of the paths it accepts on argv (`many`, `one`, `dir`, `none`).
The core intersects the selection with those two facts, decides what actually runs, and — where the
run genuinely covered a whole unit — records a **verdict** in the cache keyed on everything that
could change the answer. `scope` is untouched and keeps its single meaning: the directory the
process starts in. Nothing is configured per file or per path; a tool is annotated once, in the
shared config package, and only when the inferred value is wrong.

---

## 1. Grounding — verified facts

Everything in this section was read first-hand at `cb242db`. It is recorded so the next person does
not re-derive it.

### 1.1 The reported symptom, and its exact cause

`internal/tooling/planner.go:233-237`:

```go
case config.ToolScopeRepository:
    // Repository scope: only run when cwd is the git root.
    if p.cwdPath != p.rootPath {
        continue
    }
```

This fires **before** glob matching, before `.datamitsuignore`, and before the explicit path
arguments are consulted (`planner.go:250-256`). Because `continue` appends nothing to `skipped` —
contrast `planner.go:200-208` and `:212-222`, which do — and because `SkipReason` has exactly two
values with the comment _"Only these two reasons are surfaced to the user"_
(`internal/tooling/types.go:36-48`), the tool is **erased, not skipped**: absent from the run
report, from every `--explain` level, and from `--explain=json`. `--tools <name>` cannot rescue it
either, because tool selection filters an already-collected task list (`planner.go:146-161`).

Per-project and per-file scopes treat cwd as a **filter**, not a drop (`planner.go:277`, `:295`,
`:609`), which is why they keep working from a subdirectory.

The narrowing machinery already exists: with a non-empty `files`, repository scope runs
`filterFilesByGlobs(files, globs)` (`planner.go:250-256`). Only the eligibility guard blocks it.

### 1.2 A cached run can report success without executing

`internal/tooling/executor.go:930` constructs the result with `Success: true`, and `:938-946`
returns it immediately when every file in the task is cache-clean:

```go
// If files were specified but all are cached, return success immediately.
// Do not skip when task.Files is nil (whole-project mode with no globs).
if len(task.Files) > 0 && len(filesToProcess) == 0 {
    ...
    return result
}
```

`filesToProcess` comes from `filterFilesByCache`, which compares **only per-file content hashes**
(`cache.FileEntry{ContentHash, Lint, Fix}` — `internal/cache/cache.go:37-42`). For `tsc`,
`task.Files` is the `.ts` set matched by `typescriptGlobs`; `tsconfig.json` is not in it. Therefore:
edit only `tsconfig.json` (say, `"strict": true`), no `.ts` file changes, every hash matches, and
**`tsc` never runs while the report shows a tick indistinguishable from a real pass.** The same
shape applies to editing only `.syncpackrc.json`, to a dependency update that changes `@types/*`,
and to deleting a `.ts` file.

### 1.3 The LSP is a second, divergent granularity engine

`internal/lsp/server.go:248-263` (`scopeTasksToFile`) pins `task.Files` to a single path for **every**
task, including repository-scoped ones, and for any task whose args contain no `{file}`/`{files}`
placeholder it **appends the path to argv** — its own doc comment says so: _"turning
`golangci-lint fmt` into `golangci-lint fmt <file>`"_. It shares the CLI's cache (`server.go:83`).
Two consequences: narrow editor runs can mark files clean for a later full run, and tools that take
no positional paths receive one anyway.

### 1.4 A narrowing request can escalate into a repository-wide run

`golangci-lint` declares no `globs` (wrapper `tools.ts:425-449`). In `planner.go:281` the condition
`len(matchedFiles) > 0 || len(opConfig.Globs) == 0` passes on the **second** disjunct with
`matchedFiles == nil`; `createPerProjectTasksWithFiles(ctx, task, nil)` then takes the
`len(files) == 0` branch (`planner.go:625`) and emits **one task per detected Go project**. So
`dm fix ./README.md` runs `golangci-lint run --fix` across every Go module. (This does not fire in a
repository with no `golang-package`; it does fire in this one.)

### 1.5 An empty file list means "no path arguments", which for a formatter means "everything"

`executor.go:951-958`: when `relativeFiles` is empty, `executeBatchChunk` runs the command with
**no** file arguments. For `oxfmt --write` that is "format the whole tree from cwd". The only fence
is `planner.go:261` / `:281` / `:321` (`len(matchedFiles) > 0 || len(opConfig.Globs) == 0`). This is
the single most load-bearing condition in the planner.

Separately, `executor.go:958-975`: when args reference no file placeholder, the tool runs **once
with no file arguments** and then `updateCacheAfterSuccess` marks every file in `task.Files` clean.
That is why `tsc` and `golangci-lint` are, today, accidentally safe from subset narrowing — the
paths never reach argv — and simultaneously why their cache bookkeeping is wrong.

### 1.6 `batch` conflates six things and encodes none of them well

`executor.go:468-473` derives it from `scope`:

```go
batch := task.OpConfig.Batch
if batch == nil {
    defaultBatch := task.OpConfig.Scope != config.ToolScopePerFile
    batch = &defaultBatch
}
if *batch || len(task.Files) == 0 { executeBatch(...) } else { executePerFile(...) }
```

A single boolean simultaneously controls process count, **argv path form** (relative via
`makeRelativePaths` at `executor.go:949`; absolute via `replacePlaceholders` at `executor.go:743`),
cache-write granularity, progress accounting, error attribution, and fail-fast applicability. And it
is derived from `scope`, which answers a different question entirely.

### 1.7 Measurements

Taken on this machine (Apple Silicon, 10 cores, warm FS cache), oxfmt 0.57.0 from the datamitsu
store, single small `.ts` file, 20 iterations each:

| invocation                                            | per run    |
| ----------------------------------------------------- | ---------- |
| `node -e ''`                                          | 46 ms      |
| `oxfmt --check f1.ts`                                 | 102 ms     |
| `oxfmt --check --config <root>/oxfmt.config.ts f1.ts` | **175 ms** |

So the wrapper's exact invocation shape costs ~175 ms of fixed overhead per process, ~73 ms of which
is loading the TypeScript config. Worker pool size is `min(max(NumCPU*3/4, 4), 16)`
(`internal/env/e.go:22-25`) — 7 on this host. Batch chunking is bounded by
`DATAMITSU_MAX_CMD_LENGTH`, default 32000 (`internal/env/e.go:45-49`).

Consequence, measured against a consumer monorepo: moving `oxfmt` from `repository` to
`per-project` would replace 1 process with 60+ (lower bound, counted via the `eslint` task count),
i.e. ~10.5 s of added CPU and ~1.5 s of wall clock on every hook and CI run. **That is why `scope`
is not the lever.**

### 1.8 Arity cannot be inferred from `{root}`

Classifying all `args:` blocks in the wrapper by placeholder shape (script over
`src/datamitsu-config/tools.ts`) yields:

| shape                                           | blocks |
| ----------------------------------------------- | ------ |
| standalone `"{files}"`                          | 33     |
| standalone `"{file}"`                           | 14     |
| standalone `"{root}"` / `"{cwd}"`               | 13     |
| `{root}`/`{cwd}` embedded inside a larger token | 6      |
| no path token at all                            | 18     |

The first two classes are unambiguous. The 19 blocks containing `{root}`/`{cwd}` are **not**: for
`knip`, `syncpack`, and `tflint` the `{root}` is the path to a _config file_ and there is no target
at all; for `grype` the target is embedded as `dir:{root}`; `trufflehog` has both a `{root}` target
and a `--exclude-paths {root}/...` config in one argv. No syntactic rule separates them. This is the
entire justification for a dedicated `{target}` placeholder (§3.3).

> The script finds 86 `args:` blocks; two of them sit inside commented-out duplicate entries
> (`tools.ts:414-423`, gitleaks — whose line 418 is not even valid TypeScript — and `:1008-1019`,
> knip). Re-derived independently by parsing operation blocks rather than bare `args:` matches: 84
> live operations (30 `fix:` + 54 `lint:`), classifying 33 `many` / 14 `one` / 19 ambiguous
> `{root}`/`{cwd}` / 18 with no path token. The two unambiguous classes match exactly; the entire
> discrepancy between the two methods sits in the ambiguous region, which is the argument for
> `{target}`.

---

## 2. Locked decisions

Confirmed by the owner during design. Not open for re-litigation.

1. **`scope` is not touched.** It keeps meaning "where the process starts" and keeps feeding
   `task.ProjectPath` → `cmd.Dir` and the `{cwd}` / `{toolCache}` placeholders
   (`executor.go:1268-1295`). Splitting it into independent axes was evaluated and rejected: it
   would require re-anchoring both placeholders, and an old binary reading a new config would see
   `Scope == ""` and fall through to the per-project branch (`planner.go:307`), converting oxfmt
   from one process into 60+.
2. **`dm fix` with no path arguments, run from a subdirectory, means "this subtree".** This is
   intended product behaviour, not an accident: descend into one app of a monorepo and fix only that
   app. Lefthook runs from the worktree root (git guarantees it, and `getStagedFiles` forces
   `cmd.Dir = rootPath`), as does this repository's own CI, so the cwd dependence causes no
   divergence here. Consumers are warned in the docs: a CI job using `working-directory:` or
   `pnpm --filter <pkg>` executes with cwd inside a package and therefore gets a **subtree run** —
   correct per this decision, but it must be a deliberate choice, not a surprise. The **only** change is that repository
   tools stop vanishing: narrowable ones run from the root restricted to the subtree, non-narrowable
   ones are reported as skipped.
3. **Arity is a separate axis with its own field.** `granularity: "file"` is _not_ split into
   file/files. `batch` is deleted.
4. **`execution.widenTo` is always an object with optional members**, keyed by operation.
5. **Correct caching for `unit` and `repo` ships in the same change** as the granularity gate. No
   "v1 without caching". Staging into PRs is fine; staging into versions is not.
6. **LSP behaviour is configurable**, with a recommended default.
7. **Everything committed is written in English.**

---

## 3. The model

### 3.1 `Selection` — what the user asked for

Built once in `internal/runner/runner.go` (replacing the bare `sc.files = args` at `:193` and
`normalizeFilePaths` at `:203`) and passed to the planner.

| Mode         | When                                                                                | Meaning             |
| ------------ | ----------------------------------------------------------------------------------- | ------------------- |
| `All`        | no path arguments and `cwd == rootPath`                                             | the complete answer |
| `Subtree(D)` | no path arguments and `cwd != rootPath` (`D = cwd`), or a directory argument        | this subtree        |
| `Paths(P)`   | one or more explicit path arguments, or `--file-scoped` with a non-empty staged set | exactly these       |
| `Empty`      | `--file-scoped` with nothing staged                                                 | explicitly nothing  |

`Empty` is a distinct mode, not an empty slice. Today `runner.go:194-200` produces `nil`, which
reaches `planner.go:251`, takes the `len(files) == 0` branch, sweeps the globs across the whole
repository, and — for a glob-less repository tool — reaches `executor.go:952` and runs the tool with
no file arguments. A pre-commit hook with an empty index can currently rewrite the entire repository.

The type is named `Selection`, not `Target`: `internal/target/target.go:35` already declares
`type Target struct` (os/arch/libc) and `runtimeconfig` imports it.

### 3.2 `granularity` — the smallest input set on which the verdict is complete

```go
type ToolGranularity string

const (
    GranularityFile ToolGranularity = "file" // any subset of files is a valid, complete input
    GranularityUnit ToolGranularity = "unit" // valid only over a whole project / module / compilation unit
    GranularityRepo ToolGranularity = "repo" // valid only over the whole repository
)
```

Inferred when absent, so day zero costs zero annotations:

```
scope == "per-file"                                  -> file
scope == "repository" && argsReferenceFiles(args)    -> file
scope == "repository"                                -> repo
otherwise (per-project, and the unvalidated default) -> unit
```

**`per-project` maps to `unit` unconditionally — deliberately not inferred from the args shape.**
This is the fail-safe direction. A tool such as `ty` (`tools.ts:1129`) is `per-project` with
`args: ["check", "{files}"]` — byte-identically shaped to `prettier` — but its verdict is
cross-corpus. No validation of argv shape can distinguish the two. Being wrong in the `unit`
direction costs a lost cache entry; being wrong in the `file` direction costs a wrong answer.

Declaring `granularity: "file"` is therefore always a _speed_ decision, and the validator enforces
`argsReferenceFiles(args) || scope == "per-file"` before allowing it. That gate **rejects it wherever
args carry no file placeholder** — `tsc`, `tsgo`, `golangci-lint`, `knip`, `syncpack`, `rustfmt`,
`helm`, `pre-commit`, `terragrunt-fmt`. It is not a complete defence: a tool such as `ty` _does_ pass
`{files}`, so the gate accepts a `file` declaration for it and only the fail-safe default plus human
judgement stand in the way. Say so plainly rather than claiming a structural guarantee that is not
there.

> Package-boundary work this implies: `argsReferenceFiles` is unexported and lives in
> `internal/tooling/executor.go:1010`, while the dependency direction is `tooling → config`
> (`internal/tooling/types.go` imports `internal/config`; `internal/config` imports nothing from
> `tooling`). The validator cannot call it where it stands. `argsReferenceFiles`, `InferArity` and
> `InferGranularity` all move into `internal/config` (or a shared leaf package) and are exported;
> the existing `tooling` call sites are repointed. This is a prerequisite inside S2, not an
> afterthought.

### 3.3 `arity` — the shape of paths on argv

```go
type ToolArity string

const (
    ArityMany ToolArity = "many" // an arbitrary list of paths
    ArityOne  ToolArity = "one"  // exactly one path per process
    ArityDir  ToolArity = "dir"  // exactly one directory; no file list
    ArityNone ToolArity = "none" // no paths on argv at all
)
```

A fifth placeholder, **`{target}`**, is added to `ToolArgPlaceholders`
(`internal/config/validate.go:601`, currently `{file, files, root, cwd, toolCache}`). It marks the
directory a scanner should scan. With it, inference becomes exact rather than heuristic:

```
any arg contains {target} -> dir
else any contains {files} -> many
else any contains {file}  -> one
else                      -> none
```

`arity` may be declared, but **only as an assertion**: a declared value that disagrees with the
inferred one is a config load error. Because inference is exact, an author cannot silence it with an
annotation — they must fix `args`. This is what makes the assertion safe, and it is precisely why
inferring `dir` from a bare `{root}` was rejected: that would have frozen a wrong inference for the
five operations where `{root}` is a config path.

The assertion earns its keep immediately on the three `yq` operations (`tools.ts:1246`, `:1259`,
`:1271`): they are correctly `per-file` with `{file}` today, but `yq -i` given two files splices the
second into the first as a second document and exits 0. A one-token edit from `{file}` to `{files}`
is silent data loss; `arity: "one"` makes it a load error.

**`batch` is retired**, with a hard error rather than silent acceptance. Dispatch moves from the
`batch` boolean to a four-way switch on arity.

> **The field must be kept in the Go struct to make the error possible.** Tool config never passes
> through `encoding/json`: it is exported from the goja VM by `vm.ExportTo(resultVal, cfg)`
> (`cmd/config_loader.go:527`, field mapping at `internal/engine/engine.go:64`). goja's export walks
> the **Go struct's** fields and pulls matching JS properties, so a JS key with no corresponding Go
> field is never consulted. Deleting `Batch` would therefore make `batch:` _invisible_, not fatal.
> Retain it as a deprecated tripwire field, reject any config in which it is non-nil, and delete the
> field only in a later release once no config carries it.

Two existing consumers must move with it. `internal/config/validate.go:718` reads
`op.Batch != nil && *op.Batch` inside the stdin/stdout contract check; that rule must be restated in
terms of `scope == "per-file" && arity ∈ {one, none}`. And the wrapper's hand-maintained `.d.ts`
fork still declares `batch?: boolean` (§8.1).

#### Dispatch table

`batch` conflated six concerns (§1.6). Each is now a function of arity alone:

| arity  | processes per task | argv paths                                            | chunking                                   | cache write                    | error attribution                |
| ------ | ------------------ | ----------------------------------------------------- | ------------------------------------------ | ------------------------------ | -------------------------------- |
| `many` | 1 (per chunk)      | relative to the working directory (`executor.go:949`) | yes, by `chunkFilesByCommandLength`        | once, after all chunks succeed | per task                         |
| `one`  | N, one per file    | absolute (`executor.go:743`)                          | no — the chunk is one file by construction | once, after all N succeed      | per file, aggregated to the task |
| `dir`  | 1                  | one directory substituted for `{target}`              | no                                         | once                           | per task                         |
| `none` | 1                  | none                                                  | no                                         | once                           | per task                         |

`one` and `none` differ from `many` in that the file list is never a chunking input; `none` differs
from `one` in that the list never reaches argv at all and serves only as a trigger deciding _whether_
and _where_ the task runs.

> Implementation trap, verified: retiring `batch` is behaviour-preserving **only because dispatch
> moves to arity**. `dotenv-linter` (`tools.ts:321-330`) is `scope: "per-file"` with an explicit
> `batch: true` and `args: ["fix", "{files}"]`. Removing the field and falling back to the
> scope-derived default would move it from `executeBatch` to `executePerFile` and flip its argv
> paths from relative to absolute. Classified by arity it is `many`, stays on the batch path, and its
> argv is unchanged.

#### Legality matrix

Declared combinations the validator must decide, rather than leave to inference:

| granularity × arity                                                                        | verdict                                                                                                                                                                                                                                                                                                                                                                                                              |
| ------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `file` × `many`, `file` × `one`                                                            | legal — the common formatter/linter shape                                                                                                                                                                                                                                                                                                                                                                            |
| `file` × `none`                                                                            | **legal iff `scope == "per-file"`**, rejected otherwise. Under per-file scope the planner atomises to one task per file with `ProjectPath = filepath.Dir(file)` (`planner.go:297-305`), so the file reaches the tool through cwd rather than argv — which is exactly what §5.5's `arity == none` rule already says. Without this cell `sort-package-json` (`tools.ts:876-895`), a config we ship, becomes unloadable |
| `file` × `dir`                                                                             | **rejected**: a `file` verdict cannot be earned from a directory argument                                                                                                                                                                                                                                                                                                                                            |
| `unit` × any                                                                               | legal                                                                                                                                                                                                                                                                                                                                                                                                                |
| `repo` × any                                                                               | legal                                                                                                                                                                                                                                                                                                                                                                                                                |
| `{target}` co-occurring with `{file}`/`{files}`                                            | **rejected**: the ordered inference would pick `dir` and silently drop the file list — the same class of silent loss the `yq` assertion exists to prevent                                                                                                                                                                                                                                                            |
| declared `arity` ≠ inferred `arity`                                                        | **rejected** (§3.3)                                                                                                                                                                                                                                                                                                                                                                                                  |
| declared `granularity: "file"` without `argsReferenceFiles(args) \|\| scope == "per-file"` | **rejected** (§3.2)                                                                                                                                                                                                                                                                                                                                                                                                  |
| any non-nil `batch`                                                                        | **rejected** (deprecated tripwire)                                                                                                                                                                                                                                                                                                                                                                                   |

### 3.4 `execution.widenTo` — how far the core may exceed what was asked

```ts
type WidenTo = "target" | "unit" | "repo";

execution?: {
  /**
   * How far the core may widen the work beyond the requested selection, per operation.
   * An operation left unset takes the core default.
   *   "target" - only what was named; anything needing more is reported as skipped
   *   "unit"   - may widen to the unit (project/module) containing the target
   *   "repo"   - may widen to the whole repository
   */
  widenTo?: Partial<Record<OperationType, WidenTo>>;
};
```

```go
type Execution struct {
    WidenTo map[OperationType]WidenTo `json:"widenTo,omitempty"`
}
```

`OperationType` is exactly `"fix" | "lint"` (`internal/config/config.go:51-57`), and
`Partial<Record<OperationType, …>>` mirrors the existing `operations` field
(`config/config.d.ts:835`) rather than inventing a second way to express "per operation". A typed
map, not `map[string]any`, so the Runtime Config policy is satisfied.

`omitempty` is load-bearing, not cosmetic: the whole config is `json.Marshal`ed into the cache
invalidation key (`cache.go:447`) and a key mismatch resets the entire cache file
(`cache.go:171-182`). An empty map serialises to nothing, so configs that set nothing see no cold
start on upgrade. Go sorts map keys when marshalling, so the key stays deterministic.

**Core defaults: `fix: "unit"`, `lint: "unit"`.** `lint` deliberately does not default to `"repo"`;
otherwise `dm lint ./one.ts` would drag `knip`, `syncpack`, `trivy` and `pre-commit` across the whole
repository and a targeted check would stop being targeted.

Resolution order, highest first:

```
--widen-to=target|unit|repo            one-off CLI flag, applies to the current operation
tools.X.operations.fix.widenTo         scalar - the context is already a single operation
execution.widenTo.fix                  project policy
"unit"                                 core default
```

The tool-level override is a scalar on purpose: inside `operations.fix` you are already in one
operation, and an object there would be nesting that expresses nothing.

This replaces the `--global` flag from earlier drafts; it degenerates into `--widen-to=repo`.

**One lattice, and the editor may only narrow it.** Order the values `target < unit < repo`. The LSP
session policy of §6.1 does not form a second, independent chain: the effective value is
`min(configResolved, sessionPolicy)`. So a config setting `execution.widenTo.fix = "target"` really
does switch off the §4.3 blast radius on editor save, and a config setting `"repo"` can never make
the LSP run `pre-commit run --all-files` on every keystroke — §6.2 calls that never acceptable, and
the lattice is what makes it impossible rather than merely discouraged.

---

## 4. Decision tables

Notation: `M` = `selection ∩ globs − excludeGlobs − .datamitsuignore`, computed by one shared helper
replacing the four near-identical blocks at `planner.go:249-260`, `:266-279`, `:287-295`, `:309-319`.

### 4.1 Eligibility — keyed on cwd, and on cwd alone

**§4.2 is the authoritative table. §4.1 is a derived pre-filter view; where the two appear to differ,
§4.2 wins.**

| granularity | `cwd == root`                                                     | `cwd` is a subdirectory                   |
| ----------- | ----------------------------------------------------------------- | ----------------------------------------- |
| `repo`      | runs (**unchanged**), file set filtered by globs exactly as today | reported skip `repository-scope-subdir`   |
| `unit`      | runs (**unchanged**)                                              | runs; unit set per §4.2                   |
| `file`      | runs (**unchanged**)                                              | runs; files filtered to cwd ∪ named paths |

Keyed on cwd, **never** on `Selection.Mode`. This is load-bearing for the pre-commit hook:
`lefthook.yaml` runs `datamitsu check --file-scoped` from the worktree root with an explicit staged
set, which §3.1 classifies as `Paths(P)`. Keying eligibility on the mode would silently stop
`syncpack`, `gitleaks`, `pre-commit` and `terragrunt-fmt` from running in the hook — a regression
disguised as a feature, and one that would also invalidate the §5.9 costing. Keying on cwd preserves
today's behaviour exactly: the set of tools that actually run from the root is unchanged, and the
only behavioural change in this whole plan is confined to the subdirectory case.

Note that `scope` does not appear in this table. Eligibility is a function of granularity;
`scope` only decides the working directory of whatever does run.

### 4.2 Selection × granularity

Notation: `M` = `selection ∩ globs − excludeGlobs − .datamitsuignore`. "arity-shaped argv" means argv
is built per §3.3's dispatch table: `none` gets no paths, `dir` gets one directory, `many`/`one` get
the file set.

| Selection \ granularity | `file`                                                                                                                                       | `unit`                                                                                                                                                                                                                             | `repo`                                                                                                                                                                             |
| ----------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `All`                   | processes start per `scope`; `Files` = all glob matches; chunked when `arity == many`. Coverage `complete`                                   | one task per detected project of a matching `projectTypes`; arity-shaped argv. Coverage `complete`                                                                                                                                 | one task at the git root; arity-shaped argv (`none`: no paths — the glob set is only a trigger; `dir`: `{target}` = git root; `many`/`one`: all glob matches). Coverage `complete` |
| `Subtree(D)`            | `Files = M ∩ subtree(D)`; empty ⇒ no task, reported `no-matching-files`. Coverage `complete` — for `file` granularity the unit _is_ the file | unit set = the project containing `D`, if any, **plus** all projects under `D` (§C6 below); empty ⇒ reported `no-matching-unit`, today a silent `return nil` at `planner.go:613` and `:677`. Coverage `complete` per unit that ran | not run, reported `not-narrowable`; runs only under `widenTo: "repo"`                                                                                                              |
| `Paths(P)`              | `Files = M`; `filterFilesToCwd` is **not** applied, fixing today's silent drop of `dm fix ../other/x.ts`. Coverage `complete`                | `M` grouped by nearest project (`groupFilesByProject`, `planner.go:534`); one task per distinct project. Coverage `complete` per unit that ran                                                                                     | eligible iff `cwd == root` (§4.1); then exactly as `All` — the named paths narrow `M` through the globs, as they do today. Otherwise `not-narrowable`                              |
| `Empty`                 | nothing                                                                                                                                      | nothing                                                                                                                                                                                                                            | nothing                                                                                                                                                                            |

The `Subtree(D)` unit set deliberately includes the **ancestor** project of `D`, not only its
descendants. Locked decision 2 is "descend into one app of a monorepo and fix that app"; standing in
`services/api/src` with the project root at `services/api` must fix that app, not report
`no-matching-unit`. This mirrors the nearest-project rule already used for `Paths(P)`.

The `unit` column is the subtle one. `unit` does **not** mean "withhold the paths". It means _the
process always covers a whole unit_; the named paths narrow the **report**, never the **analysis**.
For `eslint` (`arity: many`) argv is identical to what `file` granularity would produce, so only the
named files are rewritten. For `tsc` and `golangci-lint` (`arity: none`) argv contains no paths at
all, so `tsc` keeps its `tsconfig.json` and `golangci-lint` keeps its package — avoiding the
typecheck storm that its own FAQ documents as suppressing every other linter.

### 4.3 The blast-radius consequence of the chosen default

With `widenTo.fix = "unit"`, `dm fix ./swagger.json` will run a `unit` + `arity: none` fix operation
over the whole unit — `golangci-lint run --fix` rewrites files the user did not name. This follows
directly from the owner's "run everything by default" decision and is switched off with
`execution.widenTo.fix = "target"`. Recorded here so it is not discovered in the field.

---

## 5. The verdict cache

Gating the per-file cache to `granularity == "file"` makes `executor.go:938-946` unreachable for
`unit` and `repo` — which removes the false green of §1.2 and simultaneously leaves those tools with
no cache at all. Hence the verdict cache ships in the same change.

### 5.1 Structures — additive to `internal/cache/cache.go`

No msgpack tags: `FileEntry` (`cache.go:37-42`) and `File` (`cache.go:45-50`) carry none, so on-disk
keys are Go field names. Adding a tag anywhere would orphan the existing keys.

```go
// VerdictEntry records that one operation over one unit PASSED while its inputs
// hashed to InputHash. Failures are never stored.
type VerdictEntry struct {
    Tool        string    // debugging + prune
    Op          string    // "lint" | "fix"
    UnitDir     string    // relative to the git root; "" == git root
    InputHash   string    // xxh3-128 over the input vector
    Members     int       // membership size, reporting only
    ValidatedAt time.Time // TTL + prune
}

type File struct {
    InvalidationKey string
    ProjectPath     string
    LastPruned      time.Time
    Entries         map[string]FileEntry
    Verdicts        map[string]VerdictEntry // NEW; nil in files written by older builds
}
```

### 5.2 Key = identity, value = precondition

**Map key** = `XXH3Multi("dmv1", tool, op, unitDirRel, granularity, arity, raw (unexpanded) args…,
sorted op.Env pairs…, sorted env-allowlist pairs…)`.

Args are hashed **raw, before `expandPathPlaceholders`**. `{cwd}` / `{root}` / `{toolCache}` are
deterministic functions of `unitDirRel` and `rootPath`, both already in the vector; hashing expanded
args would bake absolute paths into the key and orphan the entire cache when the repository moves or
`DATAMITSU_CACHE_PATH` changes.

**Value** `InputHash` = `XXH3Multi("m", ⟨relPath, contentHash⟩ over all members, sorted; "g",
⟨relPath, contentHash | "(missing)"⟩ over all guards, sorted)`.

Coverage is deliberately **not** in `InputHash`. Coverage is a property of the _writer_ — whether
that run earned the right to record a verdict — not of the inputs. Folding it into the value would
prevent a narrowed run from ever matching an entry written by a complete run, which is precisely the
hit §5.5 depends on.

Identity in the key and inputs in the value gives a self-healing entry: editing a file produces a
hit-and-mismatch, i.e. one entry per (unit, operation) that overwrites itself, rather than an
unbounded family of orphans for `Prune` to chase.

Note on `hashutil.XXH3Multi`: it joins parts with a single `\0` and no length prefix
(`internal/hashutil/hashutil.go:29-46`). Today the global key mixes in **raw file contents**
(`cache.go:466-476`), so part boundaries are forgeable by content. Every part of the new vector is a
hex digest or a POSIX path (which cannot contain NUL), so the vector is unambiguous — a further
argument for §5.6.

### 5.3 Membership = full subtree coverage, computed by the planner

Members are **all tracked files under `UnitDir`**, taken from the repository walk the planner
already performs (`planner.go:900-908`, `p.cachedFiles`, gitignore-aware) — **not** `task.Files`.
`task.Files` has already been narrowed by the caller (`runner.go:195` for the lefthook staged set,
`:203` for explicit paths, glob filtering in `planner.go:261`/`:281`), and hashing it would let a
`--file-scoped` run record a verdict covering a whole unit.

The subtree is deliberately a **superset** of the glob set: a new `.cts`, a generated-but-tracked
file, a file in a nested sub-unit — all invalidate. Narrowing risks a false green; widening costs
only a miss. `node_modules` is excluded for free because the walker honours gitignore.

### 5.4 Guards — inputs outside the subtree that are cheaply knowable

- **G1. A fixed set of filenames along the ancestor chain** from `UnitDir` to the git root:
  `tsconfig*.json`, `eslint.config.*`, `.golangci.y*ml`, `package.json`, `go.mod`, `go.work`,
  `Chart.yaml`, `.editorconfig`, `.oxlintrc.json`, `prettier.config.*`, plus **always** the root and
  unit lock files (`pnpm-lock.yaml`, `go.sum`, `go.work.sum`, `uv.lock`, `Cargo.lock`). Fixed in Go,
  explicitly **not** derived from `projectTypes`: those markers were written for project _detection_
  (`typescript-project` matches the literal `tsconfig.json`, never `tsconfig.base.json`; and
  `pnpm-lock.yaml` is a marker of a _different_ project type, so `tsc` would never see it).
- **G2. Config files the args already name.** Any args token that expands via
  `expandPathPlaceholders` (`executor.go:1268-1295`) to an existing **file** becomes a guard. This
  covers `eslint -c {cwd}/eslint.config.mjs`, `prettier --config {cwd}/prettier.config.mjs`,
  `oxlint -c {cwd}/.oxlintrc.json`, `oxfmt --config {root}/oxfmt.config.ts`, and a dozen more, at no
  configuration cost. **Hard exclusion:** nothing under `env.GetCachePath()` — `{toolCache}` is an
  _output_ (tsbuildinfo, `GOLANGCI_LINT_CACHE`), and folding an output into a precondition
  guarantees a permanent miss.
- **G3. `invalidateOn`, redefined** as "additional guard patterns for this unit": doublestar,
  resolved relative to `UnitDir` **and every ancestor directory up to the git root**. The ancestor
  resolution is what makes `packages/shared/**` expressible; without it sibling dependencies are
  unfixable in principle.
- **G4. Environment.** The inherited environment filtered by a **prefix
  allowlist** (`GO*`, `CARGO*`, `RUST*`, `NODE_*`, `NPM_*`, `PYTHON*`, `PIP_*`, `UV_*`, `JAVA_*`,
  `TS_*`, `ESLINT_*`, `RUFF_*`, `TF_*`, `TFLINT_*`). `executor.go:518` does
  `cmd.Env = mergeEnvLayers(cmd.Environ(), …)`, so a tool inherits the entire environment and
  `GOFLAGS=-tags=integration` changes golangci-lint's package graph today with no key change.
  Hashing the whole environment is impossible (TERM, session ids, TMPDIR would prevent every hit);
  the prefix list is single, global, and visible in `datamitsu config runtime`. `op.Env` is **not**
  hashed here — it is hashed **raw** on the key side (§5.2). One side, one discipline: hashing it
  expanded on the value side would reintroduce the absolute paths that §5.2 removes and would break
  the §5.10 guarantee that a repository move does not orphan the cache.

### 5.5 Read and write gates

There is one closed enum, `coverage: "complete" | "partial"`, used for the Go field on `Task`, the
JSON key on `TaskJSON`, and the `--require-coverage` flag (§11.4). `coverage` is a **per-task**
property meaning "this process covered its own unit" — never a statement about the repository, which
is expressed by the run-level predicate in §11.4. There is no second concept called `fidelity`.

**Read** happens in `executeTask`, **before** the arity dispatch (`executor.go:468`), when
`granularity != "file" && cache enabled && !expired && InputHash matches`. Coverage is **not** part
of the read condition: a hit means every member and guard is byte-identical to the run that passed,
which is exactly as sound for a narrowed invocation as for a complete one. Gating the read on
coverage would force every LSP save and every `dm fix x.ts` to re-run tsc and golangci-lint from
cold, destroying §6.2's argument that correctness lives in the cache gate so the execution filter is
free to be tuned for latency. A hit returns `Success: true` and one progress callback, exactly as
`executor.go:938-946` does today for the per-file case. Placing the read before the dispatch is what
mechanically guarantees a unit task never reaches `filterFilesByCache`.

**Write** is gated on coverage, and there is exactly **one** write point per task, reached after
every process and every chunk of that task has succeeded. This replaces the current scatter of four
`updateCacheAfterSuccess` calls — `executor.go:895` (inside `executePerFile`), `:972`, `:989`,
`:1001` — which are _per-process_ and would otherwise let the first successful process of an
`arity: one` task record a unit PASS while a later process still fails. The same shape applies to a
multi-chunk `many` task.

Both all-cached early returns must be gated too, not just the batch one: `executor.go:938-946` and
the per-file twin at `executor.go:706-716`. Missing the second is not hypothetical — §3.2 maps every
`per-project` operation to `unit`, and §3.3 dispatches `{file}` args to arity `one`, i.e. onto the
per-file path, so a `unit` task lands there routinely and would keep writing per-file entries and
keep returning success without executing.

**`Coverage` is set by the planner and only by the planner.** It cannot be derived from the args
shape, because `lsp/server.go:249-263` appends a bare path to placeholder-less tasks, leaving
`argsReferenceFiles` false — so a rule of the form "no file placeholders ⇒ the run covered the unit"
would let single-file editor formatting forge a full unit PASS. Only the planner knows both
`UnitMembers` and `Selection`.

`coverage == "complete"` iff:

- `arity == none` — argv does not depend on the selection, so the run always covered the unit
  (`tsc`, `golangci-lint`: byte-identical command to a full run);
- `arity == dir` and the substituted directory equals `UnitDir`;
- `arity == many` and the passed set ⊇ `UnitMembers ∩ globs`;
- `arity == one` and the union of the per-file tasks the planner emitted for this unit ⊇
  `UnitMembers ∩ globs`. Because the planner atomises `arity: one` into one task per file
  (`planner.go:297-305`), coverage for this case is a property of the **task group**, not of a single
  task; the planner therefore stamps the group and the single write point fires once the last member
  of the group succeeds.

Everything else is `"partial"`.

**Torn reads.** For `lint`, hash the input vector before and after the run and write only on a match
(cost: one lost hit). For `fix`, write the post-hash, and on a pre/post mismatch delete the sibling
`lint` verdict of the same identity — mirroring what `AfterFix` already does per file
(`cache.go:355-406`).

### 5.6 Fixing `invalidateOn`

Today `cache.go:466` does `absPath := filepath.Join(projectPath, file)` where `projectPath` is the
git root (`runner.go:217`, `lsp/server.go:72`). `packages/*/tsconfig.json` is joined literally,
`os.ReadFile` fails, and the constant `"(missing)"` is written — so editing any nested tsconfig never
changes the key.

**Decision: remove `invalidateOn` from the global key entirely.** Delete the loop at
`cache.go:453-477`, the `invalidateOnFiles` parameter of `NewCache`, its collector in
`runner.go:1299-1321`, and the helper in `lsp/server.go:89-109`. The global key stays
version + config JSON + selectedTools.

Three reasons: it is broken today; even fixed it is _global_, so editing one nested tsconfig would
reset the **entire** cache file for every tool (`cache.go:171-182`) — strictly worse than
invalidating one unit; and removing it takes a filesystem read out of a key that the CLI and the LSP
must compute identically, while also removing the `\0` boundary forgery.

Cost: **zero.** `invalidateOn` appears zero times in the wrapper's `tools.ts` (verified). Third-party
configs that use it are cold on this release anyway.

### 5.7 On-disk format and upgrade

No migration code, for two independent reasons:

1. `ldflags.Version` is the first component of the global key (`cache.go:444`); any mismatch resets
   the whole `File` (`cache.go:171-182`). The first run on a new release is cold by construction.
2. The msgpack change is additive both ways: structs encode as maps keyed by field name
   (`StructAsArray` is false and set nowhere). A new binary reading an old file finds no `Verdicts`
   key and leaves it nil (every lookup misses); an old binary reading a new file skips the unknown
   key. A corrupt file is already non-fatal (`cache.go:124-132`).

**All four `File` construction sites must initialise `Verdicts`** — `cache.go:126`, `:143`, `:175`,
`:419` — otherwise the map is nil and the first verdict write panics with `assignment to entry in nil
map`. (It is _not_ a staleness hazard: all four build a fresh `&File{…}`, so a forgotten field yields
no verdicts, not old ones. And note `func (c *Cache) Clear()` at `cache.go:417` has no production
caller — `datamitsu cache clear` goes through the package-level `ClearAll` / `ClearProject`
(`cmd/cache.go:109`, `:134`), which `os.RemoveAll` the directory.)

`Prune` (at most daily, from `Load`) gains a second pass: drop verdicts whose `UnitDir` no longer
exists or whose `ValidatedAt` is older than the TTL. That is why `VerdictEntry` carries `UnitDir` and
`ValidatedAt` as open fields behind an opaque key.

### 5.8 Concurrent writes

`Save()` marshals the whole in-memory `File` and atomically renames over the old one; `NewCache`
reads the file exactly once. Today a long-lived LSP session clobbering CLI writes is **fail-open**
(a lost warm cache). With verdicts it becomes **fail-closed**: clobbering would resurrect a verdict
the CLI already invalidated.

`Save()` must therefore re-read the file under `saveMu` and merge:

- if the on-disk `InvalidationKey` differs — **do not write**, and log (our config is stale);
- verdicts: larger `ValidatedAt` wins; future timestamps are clamped to `now` (a backwards clock jump
  otherwise breaks both the TTL and conflict resolution);
- `FileEntry`: on a matching `ContentHash`, union the `Lint`/`Fix` lists; on a **differing**
  `ContentHash`, **delete the entry** and force a miss. Union is not an option — `FileEntry` carries
  no timestamp, "newer" is undecidable, and any other choice attributes a list of passing tools to
  content they never saw.

Read-modify-write is not atomic across processes (no lock file, no CAS on rename), so interleaving
loses writes. That is a deliberate fail-open: correctness rests on "hash mismatch ⇒ delete", not on
mutual exclusion. File locks in a tool invoked concurrently from a git hook, an editor, and CI are a
source of hangs, not guarantees.

### 5.9 `repo` granularity

Same mechanism with `UnitDir == gitRoot`, but `cache` defaults to **false** (`ToolOperation.Cache`
already exists — `config.go:96`). The key degenerates to a content hash of the whole repository:
hit rate in an edit-run loop is near zero, cost approaches the cost of the tool itself, and the
justification is weakest (gitleaks walks the real tree including gitignored files, invisible to the
hash). One code path, no v2; `cache: true` is written by whoever claims their tool is a closed world.
The same rule covers the pseudo-unit for files belonging to no project (`planner.go:565-567`,
`belongsTo = p.rootPath`).

**A cost to name up front:** today `gitleaks` (globs `["**/*"]`) hits `executor.go:938-946` on a warm
cache and **does not run at all** — a fast false green. After gating it runs every time: ~+394 ms per
`datamitsu check`. That is the price of the correctness fix.

### 5.10 Structural guarantees vs bounded ones

**Structurally impossible, not merely unlikely:**

- a binary or tool-version upgrade, an `args` edit, or a declared `env` change — the whole config is
  serialised into the global key (`cache.go:447`), including app versions and SHA-256s;
- **deleting** a file from a unit — the member vector is recomputed from a fresh walk, two parts
  vanish, the hash changes;
- an edit in a nested sub-unit — subtree coverage is a strict superset;
- a narrowed run (`--file-scoped`, `dm fix <path>`, the LSP) recording a unit verdict — `Coverage` is
  set only by the planner;
- mtime/size games — content hashes only;
- a cache-root change or a repository move — raw args in the key, no absolute paths.

**Bounded only, by guards plus TTL — the staleness window is named, not hidden:**

- `extends` / `references` / `include: ../shared/**` reaching outside the subtree and not covered by
  G1/G3. The realistic trigger is a dependency update, which changes a lock file, which is always in
  G1. In this repository both chains lead into gitignored `node_modules`;
- a hand edit inside `node_modules` with no lock-file change;
- gitignored generated inputs (invisible to the walker by construction);
- network-dependent verdicts (`govulncheck` queries a remote database — the verdict changes with zero
  local input change);
- environment variables outside `op.Env` and outside the prefix allowlist.

**Fail-open (a miss or lost warmth, never a stale PASS):** interleaved concurrent `Save`, a version
change, unknown or missing msgpack keys, a guard file appearing or disappearing.

TTL is `unitCacheTTLMinutes` in `runtimeconfig.Effective`, wired through `Compute()`, with the
constant in `runtimeconfig`, an `envVar` in `internal/env/e.go` and a getter in `env.go` — per the
Runtime Config Policy checklist. Default 1440 (24 h); 0 disables the verdict cache entirely.
`datamitsu config runtime | jq .unitCacheTTLMinutes` mechanically answers "how stale can a cached
PASS be".

Hashing cost is roughly what is already paid: `filterFilesByCache` already calls `ShouldRun` per
file, and `ShouldRun` already XXH3-hashes the file when an entry exists. The subtree adds only tracked
files that no glob matched.

---

## 6. LSP policy

### 6.1 Three layers

**Layer 1 — per operation, inside `config.Config`** (correct: these fields change the meaning of a
cache entry, so they belong in the invalidation key). In `ToolOperation`
(`internal/config/config.go:89-107`):

```go
Granularity ToolGranularity `json:"granularity,omitempty"`
Arity       ToolArity       `json:"arity,omitempty"`
LSP         *bool           `json:"lsp,omitempty"` // author veto; nil = session policy decides
```

Mirrored in **three** places. Two are in-core — `config/config.d.ts` and the embedded
`internal/config/config.d.ts` — kept identical by the `cp` in `Taskfile.yaml` and enforced by
`TestConfigDTSCopiesAreByteIdentical` (`internal/config/oci_test.go:200-208`). The third is the
wrapper's hand-maintained fork, `datamitsu-config/src/datamitsu-config/datamitsu.config.d.ts`, which
is **not** guarded by any test and still declares `batch?: boolean` at `:913`. It is the file that
actually types `tools.ts`, so the §8.1 edits will not typecheck until it is updated; it is therefore
on the mandatory list.

**Layer 2 — session policy, emphatically NOT in `config.Config`.** The whole `Config` is marshalled
into the invalidation key (`cache.go:447`); an editor latency preference must not wipe the shared
cache. The existing `lsp` config key is a `Record` of server declarations with no room for a policy
scalar, so it is rejected as a home too.

LSP `initializationOptions` (per the specification the whole object belongs to the server):

```ts
interface DatamitsuInitializationOptions {
  format?: {
    widenTo?: "target" | "unit"; // "repo" is deliberately not in the enum
    timeoutMs?: number;
    tools?: Record<string, boolean>;
  };
}
```

With an environment fallback per the Environment Variable Usage Policy:
`DATAMITSU_LSP_FORMAT_WIDEN_TO` (default `"unit"`) and `DATAMITSU_LSP_FORMAT_TIMEOUT_MS` (default
`"3000"`), getters in `internal/env/env.go`. The fallback is mandatory: the shipped VS Code extension
does not send `initializationOptions` at all, and nvim/helix/emacs even less so.

Priority, highest first: (1) `lsp: false` on the operation — absolute author veto; (2)
`format.tools[name] === false` — absolute user veto; (3) `format.tools[name] === true` — user opt-in,
still losing to the author veto; (4) the `format.widenTo` class; (5) env; (6) the built-in default.
Any `false` wins; a `true` requires permission from both sides. Deliberately less
"the-editor-is-always-right" than usual, because some operations mutate lock files and some run
network scanners. The session policy can only narrow the config-resolved `widenTo`, never widen it
(§3.4).

One wrinkle to accept knowingly: `lsp: false` lives in `config.Config` and therefore in the
invalidation key (`cache.go:447`), so adding or flipping it resets the whole cache file for every
tool and every user — even though it changes no verdict. Unlike `granularity` and `arity`, which
genuinely change what a cache entry means, this one is in the hashed region only for want of a
better home. It is a maintainer-frequency edit in a shared config package, so one cache reset per
change is acceptable; if that ever stops being true, move the veto out of the hashed portion rather
than weakening the key.

**Layer 3 — introspection.** `runtimeconfig.Effective` gains `LspFormatWidenTo`,
`LspFormatTimeoutMs` and `UnitCacheTTLMinutes`, wired through `Compute()`. `Compute()` is pure and
reads only env, so an `initializationOptions` override must be echoed back in the `initialize` result
and logged to the stderr JSON-L stream.

### 6.2 The default: `format.widenTo = "unit"`

Run `file` and `unit`, never `repo`.

Against the `"target"` alternative: both Go fix operations (`tools.ts:428`, `:453`) are `per-project`
with no file arguments, i.e. `unit`. So are ruff, ruff-format, rustfmt, and the terraform family.
Under a `"target"` default, saving a `.go` file would run **nothing** — while format-on-save for Go
is the flagship documented scenario in the VS Code installation guide.

Running a unit operation in an editor is _expensive but not wrong_: it is exactly what
`datamitsu fix` does. Correctness lives in the **cache** gate (`Coverage`), not in the **execution**
gate — which is precisely why the execution filter is free to be tuned for latency.

`repo` is absent from the enum because `pre-commit run --all-files` on save is never acceptable.

`timeoutMs` is a watchdog, not a policy axis. Cancellation happens only at a priority-group boundary:
interrupting mid-chain yields a file that kept eslint's edits but not prettier's, so the same save
produces different bytes depending on machine load and CI reformats it afterwards.

### 6.3 What is deleted

`scopeTasksToFile` and `hasFilePlaceholder` (`lsp/server.go:248-274`) are removed outright. The
function is not an optimisation but a source of knowingly wrong commands: it appends a bare path to
exactly the operations whose arity is `none`. Verified by execution:
`tsc --noEmit --incremental --tsBuildInfoFile X a.ts` → `error TS5112`, exit 1, zero diagnostics;
`syncpack lint X` → "unexpected argument"; `knip X` → "does not take positional arguments".

The replacement is `Plan(ctx, OpFix, Selection{Mode: Paths, Paths: []string{absPath}}, nil)` plus the
session policy handed to the planner. **The LSP owns no granularity logic.** The filter must run
before `planApps` and `EnsureTools`, or a first save on a fresh clone downloads tools it will not run.

`selectedTools` stays nil — load-bearing: substituting a filtered tool set would diverge the
invalidation key from the CLI's and drive the two processes into a mutual reset loop.

---

## 7. What the user sees

Scene: git root `/repo`, cwd `/repo/services/api`, monorepo containing Go and Terraform as well.

```
$ cd /repo/services/api
$ datamitsu fix ./swagger.json

┏━ fix ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
┃ ◑ target: 1 file · services/api/swagger.json · narrowed run
┃ ✓ oxfmt              186ms        1 file             [repository·file]
┃ ✓ yq-json             43ms        1 file             [per-file·file]
┃ ✓ prettier           298ms   ×1   1 file · 1 unit    [per-project·unit]
┃ ✓ eslint             1.31s   ×1   1 file · 1 unit    [per-project·unit]
┃ ⊘ pre-commit                      not run (whole-repo verdict — cannot narrow)
┃ ⊘ syncpack                        not run (whole-repo verdict — cannot narrow)
┃ ⊘ terragrunt-fmt                  not run (whole-repo verdict — cannot narrow)
┃ ⊘ 19 tools                        not run (target matched no file, no unit)
┃ ⊘ 4 tools                         skipped (disabled in config)
┗━ 4 tools · 4 runs · done in 1.84s · 26 not run · partial ━━━━━━━━━━━━━━━

◑ partial fix — 3 tools answer a whole-repository question and did not run:
    pre-commit, syncpack, terragrunt-fmt
  complete answer   datamitsu fix                                (from /repo)
  run them anyway   datamitsu fix ./swagger.json --widen-to=repo
  make it an error  datamitsu fix ./swagger.json --require-coverage=repo
  full breakdown    datamitsu fix ./swagger.json --explain
```

Incompleteness is disclosed at four volumes, descending: the `◑ target:` header line (printed only
when the selection is not `All`); by name for `not-narrowable` (few, and the real loss); as collapsed
counters for `no-matching-files` / `no-matching-unit` and config skips (expanded by `--explain`, so a
monorepo does not print dozens of `⊘` lines on every `dm check`); and a `partial` token in the footer
plus a single advisory block per command.

The badge `[repository·file]` extends the existing faint scope suffix in the runner output — the
decision is readable without a flag, satisfying "Introspectable by Design".

**The invariant this makes visible:** cwd is no longer a switch. `datamitsu fix
services/api/swagger.json` from `/repo` produces a byte-identical plan.

Machine twins: `PlanJSON` gains `target: {kind, paths}` with `omitempty`; `TaskJSON` gains
`granularity` and `coverage` (both always present); `SkippedToolJSON` keeps its shape — new reasons
ride the existing stable `SkipReason.String()` string. Byte stability under
`go test ./test/cli/ -count=2` holds because tool names are already sorted in the planner and
collapsed lists print in the same order with no absolute paths. Everything new is suppressed when the
selection is `All`, so the existing explain goldens stay byte-identical apart from the always-present
`granularity` key.

---

## 8. Wrapper config migration

### 8.1 Mandatory, shipping in lockstep with the core

1. **Delete `batch:`** — 33 lines. Behaviour-preserving _because dispatch moves to arity_ (§3.3).
2. **`gitleaks`** (`tools.ts:408`): `"{files}"` → `"{target}"`, plus `arity: "dir"`. One token and one
   line. The only mandatory `args` change, and the only silent bug in the set.

   **Measured against gitleaks 8.30.1, not inferred.** Given two file arguments it reports
   `scanned ~11114 bytes` — byte-identical to `gitleaks dir .` over the same fixture, in both
   argument orders, exit 0, no warning. One argument scans exactly that file (101 B / 1001 B for the
   two fixtures). So with more than one path it discards them all and scans the working directory.

   The consequence is larger than the chunking arithmetic suggested: datamitsu's file selection has
   never reached gitleaks at all. Globs, `.datamitsuignore` pruning and `--file-scoped` are equally
   inert — it always scans the whole working directory. Chunking only decides whether that happens
   once or twice.

3. **`granularity: "file"`** on the `per-project` + `{files}` operations that the fail-safe rule
   places in `unit`: eslint, prettier, ruff, ruff-format, sqruff. **cspell and oxlint are deliberately
   NOT in this list** — cspell runs with `--unique` (`tools.ts:273`) and oxlint's enabled rule
   categories are unaudited, so neither is established as per-file, and §3.2 says being wrong in the
   `file` direction costs a wrong answer. They move to §8.2, gated on a differential check. Conversely, an
   explicit `granularity: "unit"` on `ty` (`tools.ts:1129`) — a typechecker whose verdict is
   cross-file.
4. **`arity: "one"`** on the three `yq` operations as an assertion against a destructive edit.

Net: −33 / +18 lines. **The config gets smaller.**

### 8.2 Optional, each independently safe

5. Rewrite `{root}` / `{cwd}` to `{target}` for the scanners (bearer, blint, deptry, droast, grype,
   helm, kubeconform, osv-scanner, terraform-docs, tofu-fmt, trivy, trufflehog, zizmor). Without it
   they infer as `none` and behave exactly as today; with it, subtree narrowing becomes available.
6. `invalidateOn` on golangci-lint (`.golangci.y*ml`, `go.sum`) — the one class that hides its config
   from argv.
7. `sort-package-json` from `per-file` to `repository` + `{files}` — N spawns collapse to one.
   Verified: given two explicit `package.json` paths it reports "Found 2 files. 2 files successfully
   sorted." and sorts both.
8. `granularity: "file"` for cspell and oxlint, each gated on a differential check: run the tool
   globally and narrowed and assert `findings(narrow) ⊆ findings(global) | thatFile`. Until that
   passes they stay `unit`, which is slower and correct.
9. Move selected fix operations to the existing stdin/stdout contract (`golangci-lint fmt --stdin`
   exists; `ToolInputStdin` / `ToolOutputStdout` are implemented in the core and used by the wrapper
   zero times).

---

## 9. Staging

Six stages. Each leaves the tool **shippable**, and none is "a version without caching" — but note
honestly that the §1.2 false green is closed in S3 and not before, so S1 and S2 improve visibility
and argv correctness while that specific defect remains. Say so in the release notes rather than
implying otherwise.

**`execution.widenTo` enforcement lands in S1, together with the deletion of the
`planner.go:235` guard.** Deleting the guard without the replacement gate would make every
non-narrowable repository tool (syncpack, knip, pre-commit) eligible from a subdirectory and — past
the `:261` fence, which they clear because they have no globs — run repository-wide. That is exactly
what the `unit` default exists to prevent.

**S1 — `Selection` and the planner.** Introduce `Selection{Mode, Dir, Paths}`; change
`Planner.Plan` to take it instead of the overloaded `files []string` (which today cannot distinguish
"an empty selection" from "the whole world"); unify the four file-matching blocks into one helper.
No dependencies.
_Tests:_ the `Selection` mapping is exercised through the existing planner tests; `test/cli` goldens
must not move, since S1 changes no eligibility.

> **Amended during implementation: the `planner.go:235` deletion moved out of S1 into S3.** §C1
> requires it to land together with `execution.widenTo` enforcement, and widenTo is defined over
> granularity — which S3 introduces. Deleting the guard in S1 would leave the core unable to tell a
> narrowable repository tool from a whole-repo one, so every one of them would become eligible from
> a subdirectory and, past the `:261` fence, run repository-wide. S1 and S2 therefore change no
> eligibility at all: they are pure plumbing plus the argv-shape fix, and the user-visible narrowing
> behaviour arrives in S3 with the model that makes it safe. Turning the silent exits into reported
> skips moves with it, for the same reason: the skip reason cannot be named before granularity
> exists to name it.

**S2 — arity.** `ToolArity`, `{target}` in `ToolArgPlaceholders` (`validate.go:601`), `InferArity`,
the declaration-as-assertion invariants and the §3.3 legality matrix, a four-way dispatch replacing
`executor.go:468-481`, chunking only for `many`, and a tripwire on a **retained deprecated** `Batch`
field (§3.3 — deleting the field would make `batch:` invisible to goja, not fatal). Two prerequisites
inside this stage: move and export `argsReferenceFiles` / `InferArity` / `InferGranularity` into
`internal/config` and repoint the `tooling` call sites (§3.2); and restate the stdin/stdout contract
rule at `validate.go:718`, which reads `op.Batch` today, in terms of
`scope == "per-file" && arity ∈ {one, none}`. Deleting `scopeTasksToFile`
(`lsp/server.go:248-274`) rides here — it is the same class of violation. Wrapper changes 8.1.1–8.1.2
and 8.1.4 land together.
Depends on S1. **Release constraint:** see §11.2 — the core and wrapper are sequenced across five
releases and the `batch` tripwire is a _warning_ here, becoming a hard error only in R5. Note the
config does **not** travel by OCI digest: `internal/ocibundle/ocibundle.go:55` limits bundle layers to
`.bin/`, `.runtimes/`, `.apps/`, `.uv/python` and `.parsers/` — there is no config subtree, and the
config ships as an npm package and a GitHub release asset.
_Tests:_ arity classification over all args vectors; a validator rejection table, one case per cell;
LSP tests asserting no path is appended.

**S3 — granularity and the verdict cache (atomic).** Cannot be split: the moment the per-file cache
is gated to `granularity == "file"`, unit tools have no cache, and a split would leave the system
slow and still false-green at the boundary. Contents: `ToolGranularity` + `InferGranularity`; `Task`
fields `UnitDir`, `UnitMembers`, `UnitGuards`, `Coverage`; `VerdictEntry` + `File.Verdicts` + the four
initialisation sites; `ShouldRunVerdict` / `AfterVerdict` beside `ShouldRun` / `AfterFix` under the
same mutex and atomics so the runner footer keeps working; the read gate before the arity dispatch;
replacing `updateCacheAfterSuccess` for non-file granularity; torn-read handling; guards G1–G4;
removing `invalidateOn` from `calculateInvalidationKey` and its two collectors; `Save()`
read-merge-write with "hash mismatch ⇒ delete"; the `Prune` pass; the TTL through
`runtimeconfig`/`env`. Wrapper change 8.1.3 lands here.
Depends on S1 (Coverage derives from Selection) and S2 (arity decides when Coverage is Complete with
no paths on argv).
_Tests:_ editing only `tsconfig.json` produces a unit-verdict miss (regression test for §1.2); a
narrowed run cannot write a verdict; msgpack degradation both ways; key and `InputHash` determinism;
a `ContentHash` conflict during merge yields a miss; a verdict write against each of the four `File`
construction paths does not panic on a nil map; a narrowed run still gets a verdict **hit** (the
§A2 regression — coverage gates writes, never reads).

**S4 — LSP policy.** `initializationOptions` + env fallback + echo into `runtimeconfig`; the filter
before `planApps`; the invalidation-key check on `Save`; the watchdog at group boundaries; a VS Code
extension release. Depends on S2 and S3. The risk here is scheduling, not code.

**S4b — reporting and flags.** The user-visible half of the design, which no other stage owns: the
new `SkipReason` values (`internal/tooling/types.go:36-48` still has exactly two), the `◑ target:`
header, collapsed skip counters, the `partial` footer and advisory block from §7, `PlanJSON.target`,
`TaskJSON.granularity` and `TaskJSON.coverage`, the run-level `complete` / `coverage` scalars, and
the `--widen-to` / `--require-coverage` flags with the exit-code table of §11.4.
Depends on S1 for `Selection` and S3 for `coverage`. Regenerates the three help goldens; everything
else is suppressed when the selection is `All`, so the explain goldens move only by the added keys.

**S5 — optional wrapper improvements.** §8.2, plus the cspell and oxlint `granularity: "file"`
downgrades held back from §8.1, each gated on its differential check. Each independent; none is a
correctness precondition.

---

## 10. Risks

1. **The tripwire/lockstep window (S2).** A new core rejecting `batch` plus an old config, or a new
   config plus an old core. Mitigated by release ordering (§11) and by the fact that a new config on
   an old core is fail-open — goja drops unknown keys silently, and an old core has no narrowing at
   all, so it behaves exactly as today. Note the version gate is leaky in two
   different ways: `dev` builds satisfy any requirement **silently, with no warning at all**
   (`internal/version/check.go:40-42` returns `skipped=false`), while `0.0.0-unstable.*` prereleases —
   which this project ships actively — return `skipped=true` and are only **warned** about, never
   blocked (`check.go:44-46`, warning emitted at `cmd/config_loader.go:418-430`).
2. **`HasOverlap` ordering (S1/S3).** A narrowed repository task has `ProjectPath == rootPath` while a
   per-project task has the package directory. The narrowed branch must precede **both** the
   repository short-circuit and the different-`ProjectPath` early-false, or narrowed oxfmt will be
   declared disjoint from — and race — an eslint task rewriting the same file. The twin logic in the
   plan formatter must be changed identically or `--explain` will misreport parallelism.
3. **The `planner.go:261` fence (S1).** Too liberal and a narrow request formats the whole repository;
   too strict and full runs stop invoking glob-less tools. It needs a dedicated test per branch.
4. **Guard list drift (S3).** G1 is a hand-maintained list of filenames. A new ecosystem's config
   file is not covered until someone adds it, and the failure mode is a stale PASS bounded only by
   the TTL. Accepted and documented, with `invalidateOn` as the per-unit escape hatch.
5. **Runtime cost becomes visible (S3).** `unit` and `repo` tools lose per-file caching and run every
   time: tsc, tsgo, golangci-lint, knip, syncpack, pre-commit, and gitleaks (+~394 ms per check).
   This is a deliberate trade of visible slowness for a silent lie, but it will read as a regression
   unless it is called out in the release notes.
6. **Golden-test churn.** Contained by suppressing every new output element when the selection is
   `All`, but the help goldens change with the new flags.

---

## 11. Decided: rollout and completeness reporting

This section replaces the former "Open items". Both items are decided, and the three-step rollout that was circulated for review is **rejected and replaced** — not weakened. The replacement is a release sequence gated by `getMinVersion()` (see §11.1, which reverses the capability-probe design this section originally proposed).

### 11.0 Rejected premises

Four premises of the reviewed draft are factually wrong and are corrected here before anything is built on them.

1. **"The config is digest-pinned in an OCI registry."** False. `internal/ocibundle/ocibundle.go:55` restricts bundle layers to `.bin/`, `.runtimes/`, `.apps/`, `.uv/python`, `.parsers/` — there is no config subtree. `internal/ociartifact` pulls exactly one thing, the WASM parser module (`ArtifactTypeParsers`, `internal/ociartifact/parsers.go:36`, sole caller `internal/parsermanager/parsermanager.go:353`). `datamitsu.config.oci-ghcr.js` is a config **file that contains** a store-bundle pin; it travels by npm tarball and GitHub release asset. The actual core↔config coupling is npm semver plus the consumer's lockfile plus `bin/datamitsu.js` resolving the core through `@datamitsu/datamitsu/get-exe.js`. Nothing about this rollout may reason from digest pinning.
2. **"`getMinVersion()` can gate the rollout."** False on every build we ship. `internal/version/check.go:40-42` returns silently for `"dev"`; `:44-46` returns `skipped=true` for `IsUnstable`, which `cmd/config_loader.go:426-429` renders as a `Warn` only. The wrapper's `scripts/sync-datamitsu-version.ts:57-61` additionally refuses to update the floor when the pinned core is unstable — and it is (`0.0.0-unstable.20260817.17164c7`), so the published floor is stale by construction. `getMinVersion()` is retained as a courtesy floor for stable users and is **not load-bearing anywhere in this plan**. Fixing it is a separate change and explicitly out of scope here.
3. **"`{target}` can expand as `{root}` in a first, harmless core release."** False. `gitleaks` today receives `{files}` (`tools.ts:398-408`), which `replacePlaceholders` (`internal/tooling/executor.go:1224`) expands to N relative arguments; `{root}` expands to one absolute path (`expandPathPlaceholders`, `executor.go:1268-1269`). Swapping them turns every narrowed run — including `datamitsu check --file-scoped` in the pre-commit hook — into a whole-repository scan under `--exit-code 1`, and it also flips the dispatch branch, because `argsReferenceFiles` (`executor.go:1010-1017`) matches only `{file}`/`{files}`. That is a silent verdict change, i.e. exactly the broken window the draft claimed not to have. **`{target}` therefore ships in the same release as arity dispatch, never before it.**
4. **"The wrapper can be released before or after the core with only scheduling care."** True in the unstable channel, where both repositories move together by hand, and enforced in the stable one by `getMinVersion()`. See §11.1.

### 11.1 The mechanism: `getMinVersion()`, and nothing else

**Reversed after implementation, on the owner's call.** §11.0 item 2 called the
version gate "inert on every build we ship" and built a `facts().capabilities`
probe around it. That reads a deliberate affordance as a defect.

`getMinVersion()` is skipped for `dev` and `0.0.0-unstable.*` builds
(`internal/version/check.go:40-46`) because an unstable version is a semver
prerelease of `0.0.0` and therefore sorts below every real release — enforcing it
would reject every config on every unstable build. Unstable is the parallel
development channel: both repositories are in one pair of hands and are kept in
step by whoever is editing them. Nothing needs gating there.

Anything that actually ships goes out as a stable release, and there
`getMinVersion()` enforces normally. A wrapper that needs the arity core declares
it; an older stable core refuses the config with a clear version error. That is
the whole mechanism, and it already worked.

The probe is therefore removed: `facts().capabilities`, `facts().argPlaceholders`
and the `internal/configcontract` package are gone. `facts().version` stays as
diagnostics. No capability branches appear in the wrapper — §11.2's R2/R4 collapse
into "release the core stable, then bump the wrapper's floor and migrate".

Recorded rather than quietly reverted, because the reasoning that produced the
probe was internally consistent and would otherwise be re-derived: the error was
in the premise, not the deduction.

### 11.2 The release sequence

Five releases. Every one is independently shippable, and after R4 exists there is **no core version, past or future, on which the current wrapper config fails** — that single property is what removes every window.

---

**R1 — core. The contract release. No behaviour changes at all.**

Ships: `facts().version`; a deprecation **warning** on any non-nil `Batch` (`internal/config/config.go:93`) naming the tool, the operation and the release that will reject it; the type-only declarations for `granularity`, `arity`, `execution.widenTo` and `{target}` added to `config/config.d.ts` **and** `internal/config/config.d.ts` (they must stay byte-identical — `internal/config/oci_test.go:197-208` enforces it), each noting that it needs a core new enough to implement it; a **debug** log when declared `getBeforeConfigs()` are skipped because `--before-config` was passed (today the substitution is completely silent, which is how the docker images swap in their baked config unnoticed). Debug rather than info, decided during implementation: this is the normal path for every pnpm-wrapper invocation, so info would be noise, and naming _which_ declared configs were skipped would mean running `discoverBeforeConfigs` — a second goja engine evaluating the auto config — purely to describe an intended override. The log names the auto config and the flag paths that won, which is free; a `RenderSlice` comment recording that it is a Go-struct projection and must never round-trip a tool-bearing config (`internal/dockerfile/slice.go:86`, `:92`).

A user on any wrapper sees: a warning line per `batch:` key, and nothing else. Argv, plans, goldens and exit codes are unchanged.

Guarantee: additive-only. Every field is new; no existing field changes meaning; no placeholder is added.

Implemented on `feat/tool-invocation-granularity-r1`, then partly reverted: the
capability probe and its `internal/configcontract` leaf package were removed per
§11.1, leaving the `batch` deprecation warning, `facts().version`, and the type
declarations.

**Explicitly NOT in R1: `{target}`.** Per §11.0 item 3.

---

**R2 — wrapper. The neutral release. No config semantics change.**

Ships: `@datamitsu/datamitsu` bumped to R1 in a commit that touches nothing else, `pnpm i`, `pnpm exec task -- docker:generate`, `docker/` committed, `node bin/datamitsu.js init` verified green, then publish. Also: `@shibanet0/*` added to `minimumReleaseAgeExclude` in the wrapper's `pnpm-workspace.yaml:17-20` (the core repo already has it at `:23-27`) so config and core are never quarantined asymmetrically; the wrapper's `.d.ts` fork replaced by a CI check that it equals the core's `config/config.d.ts` modulo the single leading `// prettier-ignore` line (verified: `diff` reports exactly `0a1`).

Why this release exists at all: the wrapper has a hard bootstrapping loop — `bin/datamitsu.js:12-20` runs the **pinned** core against the config `pnpm build` just produced, and `prepare` is `pnpm build && pnpm datamitsu init`, and lefthook runs `datamitsu init` at pre-commit priority 10. And `docker/Dockerfile:8` bakes a base image whose datamitsu runs `devtools split-config` over the **full** new config, with `publish-npm` downstream of `publish-docker`. Both mean the core dependency must move in its own commit, before any config change. R2 is that commit.

A user sees: nothing. Byte-identical config output.

Guarantee: the published `datamitsu.config.base.js` is unchanged apart from the dependency bump, so this release cannot break any core.

---

**R3 — core. Arity. (§9 S1 + S2.)**

Ships: `Selection`; `{target}` added to `ToolArgPlaceholders` (`validate.go:601`) **and deliberately not to `ToolEnvPlaceholders` (`validate.go:602`)**; `{target}` expanded **only** in `replacePlaceholders` (`executor.go:1224-1262`), never in `expandPathPlaceholders` (`executor.go:1268`), which is shared with env-value expansion (`replaceEnvPlaceholders`, `executor.go:1301-1313`) and has no selection, no `UnitDir` and no `Task` — implementing it there would be the one line R5 has to rip out; `ToolArity` + `InferArity`; the four-way dispatch replacing `executor.go:469-477` **and the three other independent re-derivations of the same scope-based default** (`executor.go:442-449` progress units, `internal/tooling/plan_formatter.go:352-355` explain JSON, `internal/runner/runner.go:372-379` progress totals); the stdin/stdout contract rule restated in terms of `scope == "per-file" && arity ∈ {one, none}` (`validate.go:718` reads `op.Batch` today); `hasFilePlaceholder` (`internal/lsp/server.go:267-274`) extended with `{target}` in the same commit — it is a second, independent placeholder enumeration and a `{target}`-bearing task would otherwise get a bare path appended by `scopeTasksToFile` (`server.go:248-263`), turning `gitleaks dir <root>` into `gitleaks dir <root> <file>`; `argsReferenceFiles` pinned by test to exclude `{target}`; `batch` still **accepted and warned**, not rejected.

A user on a pre-R4 wrapper sees: the same warnings as R1, and argv identical to today for all 84 operations. The two operations whose declared `batch` deviates from the scope default — `dotenv-linter` fix+lint (`tools.ts:321-341`, per-file + `batch:true`) and `sort-package-json` fix+lint (`tools.ts:876-895`, per-file + `batch:false`) — land on the arity that reproduces exactly what their `batch` asked for: `{files}` → `many` → the batch path; no placeholder → `none` → one run per planner-atomised task, cwd unchanged.

A user on a _newer_ wrapper (R4) sees the R4 branch taken. See R4.

Guarantee: `batch` is honoured-or-equalled, never rejected, so **no config in existence fails to load on R3**. `TestPlaceholderSetsMatchExecutor` (`internal/tooling/tooling_test.go:298-307`) makes the placeholder half self-checking — adding `target` to the slice without the expansion fails the build.

---

**R4 — wrapper. The migration release.**

Ships: all 33 `batch:` keys deleted; `arity` assertions added on the three `yq` operations; `gitleaks` switched to `{target}` with `arity: "dir"`; and `getMinVersion()` bumped to the stable core that implements arity, which is what makes the change safe — an older stable core refuses the config with a version error rather than mis-running it.

A user on any core sees: on pre-R3 cores, byte-identical behaviour to today plus `batch` warnings where the core emits them; on R3+, arity dispatch with `gitleaks` correctly scanning one directory instead of being handed ~912 paths it ignores.

Guarantee — this is the load-bearing one: **the R4 config is correct on every core that has ever shipped.** There is therefore no ordering constraint on the catalog bump in this repository (`pnpm-workspace.yaml:12-13`, consumed via `datamitsu.config.ts:3-5`), no constraint on `dependabot`/`renovate`, no constraint on the docker images' baked config, and no constraint on the GitHub release assets that `getRemoteConfigs()` consumers point at (which are exempt from the version gate anyway — `cmd/config_loader.go:385-389`). Every one of those hazards dissolves rather than being scheduled around.

Also in R4: produce `datamitsu.config.oci-dockerhub.js` for unstable releases too, or drop it from the npm `files` array — today it is stable-only but listed unconditionally, so an unstable-tracking consumer pointing `getBeforeConfigs()` at it gets a bare `no such file` from `cmd/config_loader.go:643-645` that will be misdiagnosed as this rollout.

---

**R5 — core. Granularity. (§9 S3 + S4 + S4b.)**

Ships: everything in §9 S3/S4/S4b; a non-nil `Batch` becomes a **hard error**; `TaskJSON.Batch` (`plan_formatter.go:59`) is **removed** and replaced by `granularity` / `arity` / `coverage`.

Preconditions, all mechanically checkable in the S5 checklist:

1. R4 published **and at least 7 days old** — `minimumReleaseAge: 10080` applies to `@shibanet0/datamitsu-config` in every consumer workspace that does not exempt it, so releasing R5 sooner would guarantee the one failing pair.
2. `test/e2e/testdata/datamitsu.config.oci-ghcr.js` regenerated at an R4 artefact — it is a frozen full-config snapshot carrying **24** `batch` keys and would otherwise break `DATAMITSU_TEST_OCI=1 go test -tags e2e_oci ./test/e2e/...`.
3. This repository's own `datamitsu.config.ts:30-31` bench block (2 `batch:false` keys, gated on `DATAMITSU_BENCH=1`) cleaned in the same commit, or `scripts/bench-overhead.sh` breaks.
4. The rejection message is directed. Today the unknown-placeholder/validation error names neither the config layer nor the version, because `ValidateTools` runs once over the merged config after the layer loop (`cmd/config_loader.go:240`) and is wrapped without a source label (`validate.go:761`). R5 threads the source label through and the `batch` message reads: _"tool X operation Y: `batch` was removed in <version>; argv shape now comes from `arity` (see …). Config layer: <path>. Upgrade `@shibanet0/datamitsu-config` to >= <R4> — that release is safe on every datamitsu version."_

A user on an R4+ config sees: no error, plus the granularity model becoming live. A user on a pre-R4 config sees: a hard, directed load failure on every command. That is the residual, stated in §11.6.

Guarantee in the config-first direction (new config, old core): none needed — see R4. Guarantee in the core-first direction (new core, old config): R3 shipped a full release of announced deprecation, and the remedy named in the error has no ordering constraint of its own.

### 11.3 Amendments this forces on earlier sections

- **§3.3 legality matrix, one cell.** `granularity: "file" × arity: "none"` is **legal when `scope == "per-file"`**, rejected otherwise. Reason: under per-file scope the planner atomises into one task per file with `ProjectPath = filepath.Dir(file)` (`planner.go:297-305`), so the file reaches the tool through cwd rather than argv and the run does cover its unit — which is what §5.5's `arity == none` rule already says. Without this, `sort-package-json` (`tools.ts:876-895`) — a config we ship — becomes unloadable, and the alternative fix (§8.2 item 7, "unverified; confirm the CLI accepts a list first") would be dragged onto the critical path.
- **§4.2 coverage cells, corrected.** The `file` column's `partial` stamps under `Subtree(D)` and `Paths(P)` are **wrong** and become `complete`. For `file` granularity the unit _is_ the file; a task that ran on the files it was given covered them. As written, §4.2 contradicts §5.5 — coverage gates verdict writes, and `partial` file tasks would kill the per-file cache under every narrowed run, including the ones `executor.go:895` writes today. §5.5 is the load-bearing definition and wins: **`coverage` is a per-task property meaning "this process covered its own unit", never a statement about the repository.** Repository-level under-coverage is expressed by the run-level predicate in §11.4, not by this field.
- **§7 advisory block.** `--widen-to=repo` and the completeness flag are not sibling remedies at every level. Under `--require-coverage=unit`, `--widen-to=repo` does satisfy the assertion (it un-skips the `not-narrowable` tools). Under `--require-coverage=repo` it does not, and the only remedy is re-running from the git root with no path arguments. The advisory block prints the remedy for the level actually requested.
- **§8.1.** Item 2 is one token and one line, and the note that it "buys correctness, not narrowing" belongs there — `{target}` does not satisfy `argsReferenceFiles`, so `gitleaks` infers `repo`, and §4.2's `repo` row pins `{target}` to the git root under both `All` and `Paths(P)`. The selection-awareness of `{target}` is inert for the entire R4 config; the first tool that exercises it is a §8.2 scanner in S5.
- **§9.** S2's release constraint is rewritten against §11.2. S5's checklist gains the three regeneration items above. The `{target}` work moves wholly into S2 (R3); nothing about it lands earlier.
- **§12.** "No backward compatibility for `batch`" is amended to: rejected with a hard error **in R5, after one full release of an explicit deprecation warning in R3**. The §12 rationale was that _silence_ is the worst outcome (`vm.ExportTo` would drop the key unannounced); a warning is not silence, and a deprecation window is not a half-implementation.
- **§13.8.** "Explain goldens change only by the added `granularity` key" is false: `TaskJSON.Batch` is already in all three (`test/cli/testdata/golden/{check,fix,lint}_explain_json.txt`). Corrected churn budget: **6 files** — 3 help goldens for the new flags, 3 explain-json goldens for the removal of `batch` and the addition of `granularity`/`arity`/`coverage`/`complete`.
- **`chunkFilesByCommandLength`** (`executor.go:1356-1369`) strips only `{files}` when computing base length, so a `{target}` arg is measured as 8 bytes. Harmless: §3.3 rejects `{target}` co-occurring with `{files}`, and chunking applies only to `arity: many`. Pre-existing for `{root}`/`{cwd}`/`{toolCache}`; not fixed here, recorded as known.

### 11.4 The flag

**Name: `--require-coverage=unit|repo`.** `--require-complete` is rejected. Reason: it reuses the token `complete`, which this design defines as a _per-task_ enum value, and it never says complete at what scope — the exact ambiguity that produced the "1 of 9 units passes" bug. The enum reuses §3.4's existing `target < unit < repo` lattice verbatim, and converting a boolean to an enum later would be breaking while adding a value is not. `target` is deliberately absent: it is the always-true crutch value.

**Semantics.** Both levels are evaluated by the planner and the runner over the whole run.

- `--require-coverage=unit` passes iff: no operation was dropped for a narrowing reason; every planned task has `coverage == "complete"`; no unsupported-platform skip. Meaning: _every unit I touched was fully analysed._ Usable in a hook and in `pnpm --filter pkg dm lint`.
- `--require-coverage=repo` passes iff all of the above **and** `Selection.Mode == All`. Meaning: _the answer covers the repository._ The `Selection.Mode == All` clause is not a formality — it is the only place the denominator exists. A repository-level assertion cannot be aggregated from per-task coverage, because §4.2 stamps `complete` on each unit that _ran_; without this clause `dm check --require-coverage=repo pkg/api/x.ts` would exit 0 after checking one unit of nine.

**Narrowed selections are permitted and reported honestly.** The reviewed draft's "report, don't forbid" is adopted; the former §11.2 lean toward rejecting the combination is deleted. Honest reporting beats an artificial flag conflict.

**One combination is forbidden: `--tools` + `--require-coverage`, as a usage error (exit 2).** Reason: `filterSkippedBySelectedTools` (`internal/tooling/planner.go:333-345`) _deletes_ the skip entries of unselected tools before anything can observe them, so the assertion would be mechanically true over a one-tool debug subset. `--tools` is documented as "(for debugging)" (`cmd/lint.go:39`); a coverage assertion over a debug subset is meaningless, not merely imprecise.

**What counts as incompleteness (fails):**

1. `not-narrowable` — a `repo`-granularity operation dropped under `Subtree(D)`. Fails both levels.
2. `repository-scope-subdir` — §4.1 eligibility drop. Fails both levels.
3. Any planned task with `coverage == "partial"` — i.e. `unit` granularity whose passed set ⊉ `UnitMembers ∩ globs`, or an `arity: one` group that did not cover its unit. Fails both levels.
4. Unsupported-platform skip (`SkipReasonUnsupportedPlatform`) — the host genuinely did not check the repository. Fails both levels.
5. `Selection.Mode != All` — fails `repo` only, by the clause above. This subsumes `Selection.Empty` (`--file-scoped` with an empty index): it passes `unit` (nothing was staged is a complete answer to "check the staged set") and fails `repo`, with no special-case rule.

**What does not count (never fails):**

6. `skip: true` in config — established, documented semantics (`runner.go:622-630`; `website/docs/reference/cli-commands.md:157-159`). Failing here would make every repository with opt-in tools permanently red.
7. `.datamitsuignore` pruning (`planner.go:242-259`) — a checked-in repo-owner policy declaring the tool inapplicable. A full run honouring ignores **is** that repository's complete answer.
8. `projectType` absent (`planner.go:187`) — would require every repository to contain every ecosystem.
9. Globs matched nothing (`planner.go:261`, `:281`, `:321`) — vacuously complete; there was nothing to check.
10. `no-matching-files` / `no-matching-unit` under narrowing — same class as 9: a property of the repository's shape, not of the run. Reported in `skipped[]`, not counted.
11. **Verdict-cache hit — counts as complete.** §5.5's read gate argues a hit is byte-identical in precondition to the run that passed; counting it as incomplete would silently disable the cache for every CI user of the flag.

**Two cases reported but not escalated:** fail-fast cancellation (`FailureReasonCancelled`) reports `complete:false` and leaves the exit code alone; and `check` where fix fails and lint is never planned (`cmd/check.go:23`, `runner.go:742-746`) emits no lint document at all — lint's completeness is _unknown_, not incomplete, and the run-level AND is false because a term is missing.

**Exit codes.** The current surface cannot express this: `cmd/root.go:158-168` is a single `os.Exit(1)` for tool failure, config error and `--fail-on-skip` alike, and `cmd/llms.go:17` records that "the rest of the CLI only ever exits 0 or 1". Introduce `CodedError interface{ error; ExitCode() int }`, checked with `errors.As` at that one site.

| code | meaning                                                    |
| ---- | ---------------------------------------------------------- |
| 0    | ok                                                         |
| 1    | tool failure — **unchanged**, so existing CI keeps working |
| 2    | usage (generalises `exitLlmsUsage`)                        |
| 3    | reserved, stays llms-local (`exitLlmsUnknownPage`)         |
| 4    | coverage assertion failed                                  |

Precedence: **tool failure (1) outranks coverage (4)**, and the machine surfaces still report `complete:false` in that case. Pinned by a test: `check --require-coverage=repo` on a repository where fix fails exits 1, not 4, and still reports `complete:false` for fix.

**Relation to `--fail-on-skip`. Keep it; do not deprecate it in this release.** It stays meaningful with the coverage flag off — a narrowed CI run that still wants to know the host lacked a binary. Concretely: unsupported-platform is one of the reasons that sets `complete:false`, so `--require-coverage` implies `--fail-on-skip`; passing both evaluates both predicates and prints both messages in a pinned order, exiting 4 once; `--fail-on-skip` is **re-routed from exit 1 to exit 4** — a breaking change, acceptable under the alpha Product Stage, and called out in the release notes. Revisit deprecation one release after R5, not in it.

**Explain mode: the flag is active, deliberately diverging from `--fail-on-skip`.** Coverage is planner-set (§5.5: "set by the planner and only by the planner"), so it can be asserted without running anything — a faster, download-free CI gate. `--fail-on-skip` is a no-op in explain mode (`runner.go:294-299`) and `TestExplainFlagMatrix` (`test/cli/check_fix_lint_test.go:194-202`) pins exit 0 for it; add a sibling case pinning the **non-zero** exit for `--require-coverage`, so the divergence is pinned rather than discovered.

**Wiring sites that must accumulate completeness, or the flag silently passes:** the zero-groups early return (`runner.go:302-312`, a second `recordSkips` call distinct from `:612`), the main path (`runner.go:612`), and the explain path (`runner.go:295-299`). `RunContinuation` (`runner.go:686-698`) forces the assertion off exactly as it forces `failOnSkip=false`, or `datamitsu setup` from a subdirectory starts failing.

**JSON shape.**

- **No `incomplete[]` array.** `SkippedToolJSON` (`plan_formatter.go:32-39`) is already byte-for-byte the proposed shape `{toolName, operation, reason, detail}`, and §7 already routes the new reasons through it. Two lists over the same facts drift, and one of them is the one CI parses. Per-tool evidence for reasons 1, 2 and 4 rides `skipped[]`; per-task evidence for reason 3 rides the `coverage` key §7 adds to `TaskJSON`.
- **`PlanJSON` gains exactly two scalars** (`plan_formatter.go:23-28`): `complete bool` — **not** `omitempty`, because `false` is the value CI needs — and `coverage string` ∈ `"repo" | "unit" | "partial"`, the same three-word ladder as the flag.
- **New `SkipReason` values** (`internal/tooling/types.go:36-48` has exactly two today): `not-narrowable`, `repository-scope-subdir`, `no-matching-files`, `no-matching-unit`. The closed set is pinned by a test the way `SkipReason.String()` already is.
- **JSON-L: `Complete *bool json:"complete,omitempty"` on `TypeDone`** — a pointer, mirroring `Success *bool` (`internal/uievent/uievent.go:80-83`), whose comment states this exact reason; `Skipped int json:"skipped,omitempty"` at `:87` already demonstrates the drop-zero trap. **No nested array on `Event`**: `uievent` is a deliberately flat leaf envelope (`uievent.go:52-55`) that imports nothing from `internal/tooling`, and a nested reason list would force a duplicate enum into the leaf package. Incompleteness rides one flat line per occurrence reusing `Tool`/`Op`/`Msg`, via a new `StatusSkip` on `TypeToolRun` (the status set at `uievent.go:40-49` has no skip value today).
- **Scope is per-operation on both surfaces, plus one run-level event.** `check --explain=json` prints two concatenated documents (two `"operation"` keys in `test/cli/testdata/golden/check_explain_json.txt`), and `TypeDone` carries `Op` (`runner.go:600-609`). `complete` is therefore per-operation, and `runSequential` emits one additional run-level terminal JSON-L event after the operation loop — next to `sc.skipFailure()` at `runner.go:749-752` — carrying the run-level `complete` and `coverage`. Both are pinned by tests; neither is left implied.

### 11.5 Residual risk

1. **The core-first upgrade against a pre-R4 config is a total failure, not a degraded one.** A user who takes R5 through a channel decoupled from their config — homebrew, scoop, winget, a floating GitHub Action major tag, the VS Code extension's bundled binary, docker `latest`, a global `npx datamitsu@latest` — while their repository pins a pre-R4 config gets a hard error from `cmd/config_loader.go:240` on **every** command, including `init`, `store clear` and `exec`. Nothing shipped in R1–R4 can reach a config that predates R4; that is arithmetic, not an oversight. Mitigations are R3's full deprecation release, the directed error naming the layer and the minimum wrapper version, and the fact that "upgrade the config" is unconditionally safe. The failure being _total_ rather than _partial_ is accepted: a config load that half-succeeds is the silence §12 refuses.
2. **A wrapper release and the core release it requires must go out in that order.** `getMinVersion()` enforces it for stable consumers; in the unstable channel it is the author's to keep, which is what that channel is for.
3. **Pre-R1 cores cannot warn about what they do not know.** A hand-written user config that copies R4's `granularity`/`arity` onto a pre-R1 core has those keys dropped by `vm.ExportTo` (`cmd/config_loader.go:527`) with no diagnostic, and the R1 `.d.ts` additions only reach editors after a `datamitsu init` on an R1+ core (`internal/managedconfig/links.go:345-347`). Irreducible.
4. **`getMinVersion()` remains inert on `dev` and `0.0.0-unstable.*` builds.** It is not load-bearing here, but this rollout also does not fix it — so there is still no working version floor for any _future_ change that genuinely needs one, and `RenderSlice` hardcodes `"0.0.0"` (`internal/dockerfile/slice.go:92`), permanently exempting slices. Tracked separately.
5. **The docker images' baked config always wins over a repository's declared `getBeforeConfigs()`**, because the ENTRYPOINT always passes `--before-config` and `cmd/config_loader.go:328` skips declared before-configs whenever the flag is present (`--before-config` is a `StringSliceVar`, `cmd/root.go:105`, so a user-supplied one _appends_). R1's info log makes the substitution visible; it does not remove it. After R4 it is harmless for this rollout, since any config version runs on any core — but the version a user thinks they pinned is still not the version that ran.

---

## 11b. Shipped, and deliberately deferred (2026-08-19)

R5 is implemented on `feat/tool-invocation-granularity-r5`. Recorded here so the
gap between plan and branch is explicit rather than something a later reader
rediscovers.

**Shipped:** granularity with inference; eligibility by granularity instead of
the cwd guard; `execution.widenTo`; the verdict cache with membership, guards
G1–G4 and the TTL; the per-file cache gated to file granularity; `invalidateOn`
as a per-unit guard; the LSP policy and the removal of `scopeTasksToFile`;
`--widen-to` and `--require-coverage`; `granularity`/`arity`/`coverage` in
explain JSON; both new runtime parameters in `datamitsu config runtime`; docs.

**Deferred, with the reason:**

- **`batch` is still a warning, not the hard error §11.2 R5 specifies**, and
  `TaskJSON.Batch` is retained. Rejecting it breaks every config still carrying
  the field, and the wrapper migration that removes it is written but unreleased
  — it waits on a stable core with arity. Flip both in the release that follows
  the wrapper's, not before.
- **Exit code 4 for a coverage failure.** Needs `CodedError` at `cmd/root.go`'s
  single exit site and re-routes `--fail-on-skip` through it, which is a breaking
  change to an existing flag and belongs in its own commit. Until then a coverage
  failure exits 1 and is indistinguishable from a tool failure — which is the
  distinction the flag exists to provide, so this is a real gap, not a cosmetic
  one.
- **`PlanJSON.complete` / `coverage`, the remaining `SkipReason` values, the
  `uievent` fields, and the `◑ target:` header with collapsed counters** from §7.
  Skips are already reported by name, so this is presentation on top of working
  reporting.
- **The `--tools` conflict fires after the run**, not as an up-front usage error.
- **§5.5's torn-read pre/post hashing for lint.** A file edited _during_ a lint
  can still have its verdict recorded against the pre-edit inputs.

**Two §4.2 cells were unimplemented and are fixed on this branch** — both
pre-existing on `main`, so neither was a regression, but both are silent losses:
`Paths(P) × file` applied the cwd filter to explicitly named paths, and
`Subtree(D) × unit` dropped the project containing the subtree.

## 12. Non-goals

Not deferred versions — explicit refusals or genuine impossibilities.

- **No knowledge base of upstream CLI contracts.** `arity` asserts what argv datamitsu builds, not
  what upstream accepts. We do not call `--help`, parse usage strings, or maintain a registry. A
  config declaring `{files}` to a tool that takes one path is caught only because the author must
  write `{target}` to get a directory. Validating argv shape is not validating an external CLI, and
  the latter is unattainable across 62 independently released tools.
- **No first-principles justification of the unit cache.** `extends` through `node_modules`,
  `references`, `include: ../shared/**`, gitignored generated inputs, and network verdicts are
  invisible to any local hash. There is no local hash that covers `govulncheck`'s remote database.
  The defence is guards plus lock files, `invalidateOn` with ancestor resolution, a named TTL, and
  `cache: false`. The boundary is declared and introspectable rather than hidden.
- **No `network: true` flag or per-tool exceptions.** One flag for one operation is exactly the
  per-tool configuration this design exists to remove. `govulncheck` is documented as `cache: false`.
- **No caching of failures.** Only PASS is stored. A failing verdict must reproduce so the user sees
  the diagnostics; a cached FAIL withholds an error that may already be fixed.
- **No distributed, shared, or remote cache, and no artefact storage.** The record is a boolean
  verdict plus its precondition, never output content. Reconstructing "the same output" would require
  determinism the tools do not have.
- **No inter-process locking of the cache file.** Merge is best-effort; interleaving loses writes.
  Deliberate fail-open — correctness rests on "hash mismatch ⇒ delete".
- **No LSP concurrency rework and no diagnostics.** The server stays single-threaded with formatting
  as its only capability. The `diagnostics` block in `initializationOptions` is left deliberately
  unoccupied so a future default for diagnostics is not extracted from a knob shared with formatting.
- **`scope` is not touched.** Neither `Selection`, nor `granularity`, nor `arity` moves the working
  directory. Under `Subtree(D)` with arity `none` the cwd stays `ProjectPath`, or `tsc` would stop
  finding its `tsconfig.json` and `{toolCache}` would move along with the key.
- **No mtime/size shortcuts** and no size threshold for large tracked files. That is exactly the
  unsoundness this work removes; the cost is accepted and measurable.
- **No automatic config repair.** The validator rejects but never rewrites `args`. The only way to
  get a directory is to write `{target}`; the only way to get a list is to write `{files}`.
- **No backward compatibility for `batch` — after one full release of warning.** R3 warns, R5 rejects with a hard error; never silently ignored. The
  product is alpha, and silence would be the worst outcome here: `vm.ExportTo`
  (`cmd/config_loader.go:527`) consults only fields present on the Go struct, so a config carrying
  `batch:` would simply have it dropped, with the behaviour change arriving unannounced. That is why
  the field is retained as a deprecated tripwire rather than deleted (§3.3).

---

## 13. Verification

End-to-end checks that must pass before the work is considered done.

1. **The original symptom.** From a package subdirectory, `dm fix ./x.json` runs oxfmt; from the
   root, `dm fix <pkg>/x.json` produces a byte-identical plan.
   `dm fix --explain=json | jq '[.groups[].parallelGroups[].tasks[].toolName]'` agrees in both.
2. **No false green.** Edit only `tsconfig.json`; `dm lint` must actually execute `tsc`. This is the
   §1.2 regression test and the single most important assertion in the suite.
3. **No forged coverage.** A `--file-scoped` run and an LSP save must not write a unit verdict;
   assert `Verdicts` is unchanged after both.
4. **No escalation.** `dm fix ./README.md` from the root must not plan `golangci-lint` for every Go
   module.
5. **No empty-list widening.** `dm check --file-scoped` with an empty index must run nothing;
   `jq '[.groups[].parallelGroups[].tasks[].fileCount] | add'` must be 0 or null.
6. **Arity assertions bite.** Changing a `yq` operation from `{file}` to `{files}` must fail config
   load, not corrupt a file.
7. **Cache degradation is safe.** A new binary on an old cache file, and an old binary on a new one,
   both miss rather than error.
8. **Golden stability.** `go test ./test/cli/ -count=2` stays byte-stable. Churn budget is **6 files**:
   3 help goldens for the new flags, and 3 explain-json goldens
   (`test/cli/testdata/golden/{check,fix,lint}_explain_json.txt`) for the removal of `TaskJSON.Batch`
   and the addition of `granularity` / `arity` / `coverage` / `complete`.
9. **Introspection.** `datamitsu config runtime | jq .unitCacheTTLMinutes` and
   `jq .lspFormatWidenTo` return the effective values, including env overrides.
