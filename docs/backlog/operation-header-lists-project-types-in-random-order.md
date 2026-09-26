---
worth: yes
where: internal/tooling/planner.go:160
added: 2026-09-27
---

# The operation header lists the detected project types in a different order on every run

`GetDetectedProjectTypes` collects the types of the cached project locations into a map and returns
the map's keys unsorted. The runner prints them as the dimmed line under `┏━ fix` / `┏━ lint`, so a
repository that detects two or more types shows them in Go's randomized map order: the same
command prints `┃ pa · pb` on one run and `┃ pb · pa` on the next. `datamitsu init` shows the same
effect in its own header.

Nothing depends on the order, which is why it has gone unnoticed, but it makes the output of an
unchanged repository differ between runs and keeps every golden of a multi-type run unstable.
Sorting the keys before returning them is the obvious shape of a fix; check the other callers of
`GetDetectedProjectTypes` for an order they rely on first.

Found while writing the execution characterization (`test/cli/execution_test.go`): a scenario with
two project types flipped its header line under `go test -count=6`, so the scenarios detect a
single type instead.
