//go:build !datamitsu_notrace

package trace

// compiledIn is a true constant in an ordinary build, so the recording bodies
// are kept and the runtime switch (DATAMITSU_TRACE) decides whether they run.
//
// It is a constant rather than a linker-injected variable on purpose: only a
// constant lets the compiler fold the guard away in the notrace build. An
// -ldflags -X value is a variable the compiler must read at run time, so it can
// never remove the code — see the package comment.
const compiledIn = true
