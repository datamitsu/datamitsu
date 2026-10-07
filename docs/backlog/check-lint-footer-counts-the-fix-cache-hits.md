---
worth: yes
where: internal/runner/runner.go:641
added: 2026-09-27
---

# The lint footer of check reports a cache rate that includes the fix operation's lookups

Each operation's footer ends with `· cache P%`, computed from `sc.projectCache.GetStats()`. The cache
belongs to the shared context of the whole process and its hit and miss counters are never reset
between operations, so in `check` the lint footer counts every lookup the fix operation made
before it. A check whose fix was fully cached and whose lint ran cold prints `cache 100%` under fix
and `cache 50%` under lint, although no lint result came from the cache.

The rate is display only and changes no verdict, which is why it is not being fixed as part of the
characterization. A per-operation rate needs either a counter snapshot taken when the operation
starts or counters owned by the operation; the verdict cache (`internal/cache/verdict.go`) feeds
the same counters and has to be covered too.

Recorded by `TestExecutionFixCache` in `test/cli/execution_test.go`, whose `check` golden
(`execution_s14_check_after_fix.txt`) shows the 50%.
