#!/usr/bin/env bash
# Combined coverage: merge in-process unit-test covdata with the blackbox CLI
# subprocess covdata into one text profile.
#
# The blackbox tests (test/cli) exercise a `go build -cover` binary in
# subprocesses; their counters flow through GOCOVERDIR. Unit tests are
# instrumented in-process and emit covdata via `-test.gocoverdir`. We route both
# into known directories, `go tool covdata merge` them, and `textfmt` the union
# into COVERAGE_OUT (default coverage.out) so the blackbox runs count toward the
# real coverage number.
#
# Usage:
#   scripts/coverage-all.sh            # writes coverage.out, prints total + lowest pkgs
#   COVERAGE_OUT=merged.out scripts/coverage-all.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

OUT="${COVERAGE_OUT:-coverage.out}"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

UNIT_DIR="$WORK/unit" # in-process unit-test covdata
CLI_DIR="$WORK/cli"   # blackbox subprocess covdata (via DATAMITSU_TEST_COVERDIR)
MERGED="$WORK/merged"
mkdir -p "$UNIT_DIR" "$CLI_DIR" "$MERGED"

echo "==> Running ./... with covdata (unit -> unit/, CLI subprocess -> cli/)"
# DATAMITSU_TEST_COVERDIR pins the blackbox harness's shared GOCOVERDIR; the same
# `go test ./...` run also collects in-process unit covdata via -test.gocoverdir.
# -covermode=atomic must match the blackbox binary's build mode, else covdata
# merge rejects the union with a "counter mode clash".
DATAMITSU_TEST_COVERDIR="$CLI_DIR" \
  go test -cover -covermode=atomic ./... -args -test.gocoverdir="$UNIT_DIR"

echo "==> Merging raw covdata (unit + CLI)"
go tool covdata merge -i="$UNIT_DIR,$CLI_DIR" -o="$MERGED"

echo "==> Writing text profile -> $OUT"
go tool covdata textfmt -i="$MERGED" -o="$OUT"

echo ""
echo "==> Combined coverage"
go tool cover -func="$OUT" | tail -1

echo ""
echo "==> Lowest-covered packages (statement-weighted)"
# Aggregate the profile's per-block statement counts by package directory so the
# percentages are statement-weighted (not a naive average of per-function lines).
awk '
	NR == 1 { next }                       # skip the "mode:" header
	{
		file = $1
		sub(/:[0-9].*$/, "", file)           # strip ":startline.col,endline.col"
		sub(/\/[^\/]*$/, "", file)           # strip the filename -> package dir
		stmts[file] += $2
		if ($3 > 0) covered[file] += $2
	}
	END {
		for (pkg in stmts)
			printf "%6.1f%%  %s\n", 100 * covered[pkg] / stmts[pkg], pkg
	}
' "$OUT" | sort -n | head -10
