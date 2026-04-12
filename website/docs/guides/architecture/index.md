---
title: Internal Architecture
description: Overview of datamitsu's internal execution model - file discovery, task planning, parallel execution, and caching
---

# Internal Architecture

This section explains how datamitsu works under the hood. Understanding the internal execution model helps wrapper maintainers optimize tool configurations and advanced users debug unexpected behavior.

## How It All Fits Together

When you run `datamitsu check`, the system moves through four stages:

```mermaid
graph LR
    A[File Discovery] --> B[Task Planning]
    B --> C[Parallel Execution]
    C --> D[Cache Update]

    style A fill:#e8f4fd,stroke:#2196f3
    style B fill:#fff3e0,stroke:#ff9800
    style C fill:#e8f5e9,stroke:#4caf50
    style D fill:#f3e5f5,stroke:#9c27b0
```

1. **File Discovery** walks the repository tree, respecting `.gitignore` rules, and collects all files that match tool glob patterns.
2. **Task Planning** groups matched files into tasks based on tool priorities, scopes, and project boundaries. Overlapping globs are detected and resolved.
3. **Parallel Execution** runs task groups sequentially by priority level, but tasks within each group run in parallel across available CPU cores.
4. **Cache Update** records results per file so unchanged files are skipped on the next run.

## Why This Matters

**For wrapper maintainers:** Understanding how priorities and overlap detection work lets you write tool configurations that maximize parallelism. Misconfigured priorities can serialize tools that could run in parallel, slowing down CI pipelines.

**For advanced users:** Knowing how file discovery and caching interact explains why certain files are or aren't processed, and how to force cache invalidation when needed.

## Components

Each stage has its own detailed documentation:

| Component                            | What It Does                                 | Key Concepts                                                  |
| ------------------------------------ | -------------------------------------------- | ------------------------------------------------------------- |
| [Task Planning](./planner.md)        | Groups files into prioritized task batches   | Priority chunking, overlap detection, CWD-subtree restriction |
| [Parallel Execution](./execution.md) | Runs tasks with fail-fast semantics          | Two-layer model, context cancellation, progress tracking      |
| [File Discovery](./discovery.md)     | Walks the repo respecting ignore rules       | .gitignore-aware traversal, project auto-detection            |
| [Caching Strategy](./caching.md)     | Tracks per-file results for incremental runs | XXH3-128 invalidation keys, separate lint/fix tracking        |

## Reading Order

If you're new to datamitsu's internals, read in this order:

1. **File Discovery** -- how files enter the system
2. **Task Planning** -- how files become tasks
3. **Parallel Execution** -- how tasks run
4. **Caching Strategy** -- how results persist between runs

If you're debugging a specific issue, jump directly to the relevant component page.
