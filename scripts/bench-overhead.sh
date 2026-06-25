#!/usr/bin/env bash
#
# bench-overhead.sh — measure datamitsu's own overhead, separated from the work
# of the tools it runs. Pairs with the DATAMITSU_BENCH-gated `tsbench` app/tool
# in datamitsu.config.ts.
#
# What it measures
#   1. exec   — per-launch startup overhead: time from invoking the binary until
#               the spawned program actually starts running. Compared against a
#               bare `bash -c` spawn so we can attribute the datamitsu-specific part.
#   2. lint   — planner + discovery overhead: time to walk the tree, plan, and
#      /fix    dispatch a ~zero-work tool across every file. Reports total wall
#               time, files dispatched, time-to-first-dispatch (pure planning),
#               and amortized per-file cost.
#
# Usage:
#   bash scripts/bench-overhead.sh            # default 30 exec iterations
#   ITERS=50 DM=./datamitsu bash scripts/bench-overhead.sh
#
# Notes:
#   - The tsbench tool only exists when DATAMITSU_BENCH=1 (set here), so this
#     never pollutes a normal `datamitsu lint`/`fix`/CI run.
#   - LC_ALL=C keeps EPOCHREALTIME / awk on a '.' decimal separator.
set -euo pipefail

DM="${DM:-./datamitsu}"
ITERS="${ITERS:-50}"

export LC_ALL=C
export DATAMITSU_BENCH=1
export DATAMITSU_OFFLINE="${DATAMITSU_OFFLINE:-1}"
export NO_COLOR=1

if [[ ! -x "$DM" ]] && ! command -v "$DM" > /dev/null 2>&1; then
  echo "error: datamitsu binary not found at '$DM' (set DM=...)" >&2
  exit 1
fi

now() { printf '%s\n' "${EPOCHREALTIME:-$(date +%s.%N)}"; }

# stats <label> — read "t0 t_inner t1" triples on stdin, print mean/median/min
# of total (t1-t0) and pre-run (t_inner-t0) in milliseconds.
stats() {
  awk -v label="$1" '
    { d_total[NR]=($3-$1)*1000; d_pre[NR]=($2-$1)*1000; n=NR }
    function summarize(arr, name,    i, s, srt, mean, med, mn) {
      s=0; mn=arr[1]
      for (i=1;i<=n;i++){ s+=arr[i]; if(arr[i]<mn)mn=arr[i]; srt[i]=arr[i] }
      mean=s/n
      for (i=1;i<=n;i++) for (j=i+1;j<=n;j++) if (srt[j]<srt[i]){t=srt[i];srt[i]=srt[j];srt[j]=t}
      med = (n%2) ? srt[(n+1)/2] : (srt[n/2]+srt[n/2+1])/2
      printf "    %-9s mean %7.2f ms   median %7.2f ms   min %7.2f ms\n", name, mean, med, mn
    }
    END {
      printf "  %s  (n=%d)\n", label, n
      summarize(d_total, "total")
      summarize(d_pre,   "startup")
    }
  '
}

echo "=================================================================="
echo " datamitsu overhead bench   (DM=$DM, iters=$ITERS)"
echo " $("$DM" version 2> /dev/null | head -n1)"
echo "=================================================================="

# ── 1. exec / launch overhead ────────────────────────────────────────────
# "startup" = t_inner - t0 = time from our call until the program prints its
# first timestamp. For the datamitsu path that includes config load, engine
# init, app resolution and the child spawn. The bare-bash path is the
# irreducible fork/exec/shell-init baseline; the delta is datamitsu's share.

echo
echo "[1] launch overhead — bare bash spawn (baseline)"
{
  for _ in $(seq 1 "$ITERS"); do
    t0=$(now)
    inner=$(bash -c 'printf "%s\n" "${EPOCHREALTIME:-$(date +%s.%N)}"')
    t1=$(now)
    printf '%s %s %s\n' "$t0" "$inner" "$t1"
  done
} | stats "bare bash -c"

echo
echo "[2] launch overhead — datamitsu exec tsbench"
{
  for _ in $(seq 1 "$ITERS"); do
    t0=$(now)
    inner=$("$DM" exec tsbench 2> /dev/null | grep -m1 -E '^[0-9]+\.[0-9]+$' || echo "$t0")
    t1=$(now)
    printf '%s %s %s\n' "$t0" "$inner" "$t1"
  done
} | stats "datamitsu exec"

echo
echo "  => datamitsu-attributable launch overhead = [2].startup - [1].startup"

# ── 2. lint / fix planner + discovery overhead ───────────────────────────
# A per-file tool matching **/* drives the planner over the whole tree and
# dispatches a ~zero-work bash per file. We log every dispatch timestamp.

run_planner_op() { # $1 = op (lint|fix)
  local op="$1" log t0 t1 files first total ttf perfile
  log=$(mktemp)
  export DATAMITSU_BENCH_LOG="$log"
  : > "$log"

  t0=$(now)
  "$DM" "$op" --tools tsbench > /dev/null 2>&1 || true
  t1=$(now)

  files=$(wc -l < "$log" | tr -d ' ')
  if [[ "$files" -eq 0 ]]; then
    echo "  $op: no files dispatched (tool not active? rebuild not needed — config is runtime-loaded)"
    rm -f "$log"
    unset DATAMITSU_BENCH_LOG
    return
  fi
  first=$(awk 'NR==1{print $1; exit}' "$log")
  total=$(awk -v a="$t0" -v b="$t1" 'BEGIN{printf "%.1f", (b-a)*1000}')
  ttf=$(awk -v a="$t0" -v b="$first" 'BEGIN{printf "%.1f", (b-a)*1000}')
  perfile=$(awk -v t="$t1" -v a="$t0" -v n="$files" 'BEGIN{printf "%.2f", (t-a)*1000/n}')

  printf '  %s --tools tsbench\n' "$op"
  printf '    files dispatched     %s\n' "$files"
  printf '    total wall           %s ms\n' "$total"
  printf '    time-to-first-disp.  %s ms   (discovery + planning)\n' "$ttf"
  printf '    amortized per file   %s ms   (planning + spawn)\n' "$perfile"

  rm -f "$log"
  unset DATAMITSU_BENCH_LOG
}

echo
echo "[3] planner + discovery overhead (whole tree, ~zero-work tool)"
run_planner_op lint
echo
run_planner_op fix

echo
echo "done."
