# Internal Architecture

> Overview of datamitsu's internal execution model - file discovery, task planning, parallel execution, and caching

This section explains how datamitsu works under the hood. Understanding the internal execution model helps wrapper maintainers optimize tool configurations and advanced users debug unexpected behavior.

## How It All Fits Together

When you run `datamitsu check`, the system moves through a startup phase and then four stages:

```mermaid
graph LR
    S[Startup and Config Load] --> A[File Discovery]
    A --> B[Task Planning]
    B --> C[Parallel Execution]
    C --> D[Cache Update]

    style S fill:#fce4ec,stroke:#e91e63
    style A fill:#e8f4fd,stroke:#2196f3
    style B fill:#fff3e0,stroke:#ff9800
    style C fill:#e8f5e9,stroke:#4caf50
    style D fill:#f3e5f5,stroke:#9c27b0
```

0. **Startup and Config Load** resolves the repository root and evaluates every config source into the merged configuration everything downstream reads.
1. **File Discovery** performs one gitignore-aware repository walk, sorts the
   resulting inventory, and shares it with bundled checks and the planner.
2. **Task Planning** groups matched files into tasks based on tool priorities, scopes, and project boundaries. Overlapping globs are detected and resolved.
3. **Parallel Execution** runs task groups sequentially by priority level, but tasks within each group run in parallel across available CPU cores.
4. **Cache Update** records per-file passes for independent operations and
   complete unit/repository verdicts for coarser operations.

## Why This Matters

**For wrapper maintainers:** Understanding how priorities and overlap detection work lets you write tool configurations that maximize parallelism. Misconfigured priorities can serialize tools that could run in parallel, slowing down CI pipelines.

**For advanced users:** Knowing how file discovery and caching interact explains why certain files are or aren't processed, and how to force cache invalidation when needed.

## Components

Each stage has its own detailed documentation:

| Component                             | What It Does                                   | Key Concepts                                                           |
| ------------------------------------- | ---------------------------------------------- | ---------------------------------------------------------------------- |
| [Startup & Config Load](./startup.md) | Resolves the repo root, evaluates config       | Config-evaluation cache, git-root memo, extension-based type stripping |
| [Task Planning](./planner.md)         | Groups inventory into prioritized tasks        | Granularity, widening, overlap detection, CWD selection                |
| [Parallel Execution](./execution.md)  | Runs tasks with fail-fast semantics            | Two-layer model, context cancellation, progress tracking               |
| [File Discovery](./discovery.md)      | Builds one shared, sorted repository inventory | .gitignore traversal, project auto-detection, inventory reuse          |
| [Caching Strategy](./caching.md)      | Tracks file passes and unit/repo verdicts      | XXH3-128 keys, guards, TTL, concurrent persistence                     |
| [WASM Output Parsers](./parsers.md)   | Sandboxed parsers + formatting diff-in-core    | Rust→WASM, SHA-256 trust, https or OCI source, wazero, Myers diff      |

## Reading Order

If you're new to datamitsu's internals, read in this order:

1. **Startup and Config Load** -- what every invocation pays before anything else
2. **File Discovery** -- how files enter the system
3. **Task Planning** -- how files become tasks
4. **Parallel Execution** -- how tasks run
5. **Caching Strategy** -- how results persist between runs

If you're debugging a specific issue, jump directly to the relevant component page.
