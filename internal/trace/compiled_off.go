//go:build datamitsu_notrace

package trace

// compiledIn is a false constant under -tags datamitsu_notrace, which makes
// every `if !compiledIn || ...` guard a compile-time true and lets the compiler
// delete the recording bodies, the clock reads and the atomics behind them.
//
// The trace API stays present and callable, so no call site needs a build tag of
// its own — it just becomes dead code.
const compiledIn = false
