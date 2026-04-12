---
title: Task Planning
description: How datamitsu groups files into prioritized task batches with overlap detection and CWD-subtree restriction
---

# Task Planning

The planner is the second stage of datamitsu's execution pipeline. It takes the list of discovered files and transforms them into an ordered execution plan — deciding what runs when, what can run in parallel, and what must wait.

## Priority-Based Chunking

Every tool operation has a `priority` value (a number). The planner groups all tasks by priority and creates one **TaskGroup** per unique priority level:

```mermaid
graph TD
    subgraph "Input: All Tasks"
        T1["prettier (priority: 10)"]
        T2["eslint (priority: 20)"]
        T3["tsc (priority: 20)"]
        T4["custom-lint (priority: 30)"]
    end

    subgraph "Group 1 (priority: 10)"
        G1T1["prettier"]
    end

    subgraph "Group 2 (priority: 20)"
        G2T1["eslint"]
        G2T2["tsc"]
    end

    subgraph "Group 3 (priority: 30)"
        G3T1["custom-lint"]
    end

    T1 --> G1T1
    T2 --> G2T1
    T3 --> G2T2
    T4 --> G3T1

    G1T1 -->|"completes"| G2T1
    G1T1 -->|"completes"| G2T2
    G2T1 -->|"completes"| G3T1
    G2T2 -->|"completes"| G3T1

    style G1T1 fill:#e8f5e9,stroke:#4caf50
    style G2T1 fill:#fff3e0,stroke:#ff9800
    style G2T2 fill:#fff3e0,stroke:#ff9800
    style G3T1 fill:#f3e5f5,stroke:#9c27b0
```

**Execution behavior:**

- Groups execute **sequentially** — Group 1 must finish before Group 2 starts.
- Tasks **within** a group execute **in parallel** across available CPU cores.
- Lower priority numbers run first (priority 10 before priority 20).

This design lets you express ordering constraints. Formatters should run before linters because formatters may change files that linters need to check:

```javascript
export function getConfig(input) {
  return {
    ...input,
    tools: {
      prettier: {
        name: "prettier",
        operations: {
          fix: {
            app: "prettier",
            args: ["--write", "{files}"],
            priority: 10, // Runs first — formats files
            globs: ["**/*.{js,ts,tsx,css,json,md}"],
          },
        },
      },
      eslint: {
        name: "eslint",
        operations: {
          lint: {
            app: "eslint",
            args: ["{files}"],
            priority: 20, // Runs after formatters finish
            globs: ["**/*.{js,ts,tsx}"],
          },
        },
      },
      tsc: {
        name: "tsc",
        operations: {
          lint: {
            app: "tsc",
            args: ["--noEmit"],
            priority: 20, // Same priority as eslint — runs in parallel with it
            globs: ["**/*.{ts,tsx}"],
            scope: "per-project",
          },
        },
      },
    },
  };
}
```

All tasks at the same priority level are placed in one group. Within each group, the executor forms parallel sub-groups of non-overlapping tasks (see next section). Sub-groups run sequentially, but tasks within each sub-group run in parallel. This ensures overlapping tasks never execute concurrently while maximizing parallelism for non-overlapping tasks.

## Overlap Detection

Before running tasks in parallel, the planner checks whether two tasks might operate on the same files. This prevents race conditions where two tools write to the same file simultaneously.

The overlap detection algorithm uses a multi-level approach:

```mermaid
graph TD
    Start["Two tasks to compare"] --> RepoScope{"Either task has<br/>repository scope?"}
    RepoScope -->|Yes| Overlap["OVERLAP ⚠️"]
    RepoScope -->|No| DiffProject{"Different<br/>project paths?"}
    DiffProject -->|Yes| NoOverlap["NO OVERLAP ✓"]
    DiffProject -->|No| PerFile{"Both per-file scope<br/>with different files?"}
    PerFile -->|Yes| NoOverlap
    PerFile -->|No| GlobCheck{"Glob extensions<br/>overlap?"}
    GlobCheck -->|Yes| Overlap
    GlobCheck -->|No| NoOverlap
    GlobCheck -->|"Can't determine"| Overlap

    style Overlap fill:#ffebee,stroke:#f44336
    style NoOverlap fill:#e8f5e9,stroke:#4caf50
```

**Key rules:**

1. **Repository-scope tasks always overlap** with everything — they operate on the entire repository.
2. **Different project paths never overlap** — tasks in `packages/frontend/` and `packages/backend/` are guaranteed disjoint.
3. **Per-file tasks with different files never overlap** — each processes exactly one file.
4. **Glob pattern analysis** — the planner extracts file extensions from glob patterns and checks for shared extensions.

### Glob Extension Analysis

The planner extracts extensions from patterns like `*.js`, `**/*.{ts,tsx}`, and checks whether two tools share any extensions:

| Tool A Globs | Tool B Globs    | Overlap?    | Reason                                  |
| ------------ | --------------- | ----------- | --------------------------------------- |
| `**/*.ts`    | `**/*.css`      | No          | Different extensions                    |
| `**/*.ts`    | `**/*.{ts,tsx}` | Yes         | `.ts` shared                            |
| `**/*.ts`    | `**/*.d.ts`     | Yes         | `.ts` is a suffix of `.d.ts`            |
| `Makefile`   | `**/*.go`       | Assumed yes | Can't extract extension from `Makefile` |

The algorithm is **conservative** — when it can't prove two patterns are disjoint, it assumes overlap. This is safer than incorrectly allowing parallel writes to the same file.

### Practical Impact on Configuration

Understanding overlap detection helps you write configurations that maximize parallelism:

```yaml
# These tools will NOT run in parallel due to overlapping extensions:
prettier:
  fix:
    priority: 10
    globs: ["**/*.{js,ts,tsx,css,json,md}"]

stylelint:
  lint:
    priority: 10
    globs: ["**/*.css"] # css overlaps with prettier — placed in separate sub-group


# Better: put stylelint at a different priority, or accept sequential execution
```

```yaml
# These tools CAN run in parallel (different project paths in a monorepo):
# Tool A runs in packages/frontend/, Tool B runs in packages/backend/
# Even with the same globs, they operate on disjoint file sets
```

## CWD-Subtree Restriction

When you run datamitsu from a subdirectory instead of the repository root, the planner restricts its scope to only the files and projects within that subdirectory. This is useful in large monorepos where you want to check only the package you're working on.

**Behavior from different directories:**

```bash
# From repository root — processes everything
~/repo$ datamitsu check
# → Runs all tools on all files across all projects

# From a subdirectory — processes only that subtree
~/repo$ cd services/api
~/repo/services/api$ datamitsu check
# → Only processes files under services/api/
# → Only runs per-project tasks for projects under services/api/
# → Skips repository-scope tasks entirely
```

**Three scope types behave differently:**

| Scope         | From root                      | From subdirectory                      |
| ------------- | ------------------------------ | -------------------------------------- |
| `repository`  | Runs once at root              | **Skipped entirely**                   |
| `per-project` | Runs for all detected projects | Runs only for projects under cwd       |
| `per-file`    | Processes all matched files    | Processes only matched files under cwd |

**Example monorepo:**

```
repo/
├── packages/
│   ├── frontend/   (package.json)
│   ├── backend/    (go.mod)
│   └── shared/     (package.json)
└── services/
    └── api/        (go.mod)
```

```bash
# From repo/packages/ — processes frontend, backend, and shared
~/repo/packages$ datamitsu check

# From repo/packages/frontend/ — processes only frontend
~/repo/packages/frontend$ datamitsu check

# From repo/services/ — processes only the api service
~/repo/services$ datamitsu check
```

The restriction uses path containment checks — a file or project path must be a descendant of the current working directory to be included. Sibling directories with similar prefixes are correctly excluded (e.g., running from `services/api` does not include `services/api-admin`).

## File-to-Project Assignment

When a tool has `per-project` scope with file globs, the planner needs to decide which project each matched file belongs to. It uses a **nearest-parent algorithm**:

1. Sort all detected projects by path depth (deepest first).
2. For each matched file, walk through the sorted projects.
3. The first project whose path is a parent of the file path wins.
4. Files not under any detected project are assigned to the repository root.

This matters in monorepos with nested projects:

```
repo/
├── packages/
│   ├── frontend/        (package.json)  ← Project A
│   │   └── src/
│   │       └── app.ts                   ← Assigned to Project A
│   └── shared/          (package.json)  ← Project B
│       └── src/
│           └── utils.ts                 ← Assigned to Project B
└── config.ts                            ← Assigned to repo root
```

Each file belongs to exactly one project — its nearest parent. This prevents duplicate processing when projects are nested. The tool then runs once per project with only that project's files, using the project directory as the working directory.
