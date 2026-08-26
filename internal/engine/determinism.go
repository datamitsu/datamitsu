package engine

import (
	"math/rand/v2"
	"time"
)

// Non-determinism sources, as recorded on the Engine.
const (
	sourceClock = "clock (Date)"
	sourceRand  = "Math.random"
)

// ObservedNonDeterminism reports whether config JS evaluated in this engine read
// the clock or the random source. An evaluated config that did is not a pure
// function of the cache key, so its result must never be stored: a cached
// artifact would serve one moment's answer forever, with no error and no
// external symptom.
func (e *Engine) ObservedNonDeterminism() bool {
	return e.nonDeterminism != ""
}

// NonDeterminismSource names the first non-deterministic source that was read,
// or "" if none was.
func (e *Engine) NonDeterminismSource() string {
	return e.nonDeterminism
}

// initNonDeterminismShims routes goja's two non-deterministic primitives through
// recorders. goja consults r.now() for `new Date()`, `Date()` and `Date.now()`,
// and r.rand() for `Math.random()` — those four call sites are the complete set
// of clock and entropy reads a config can make in this VM.
//
// The hooks are used in preference to wrapping the JS globals because they are
// transparent by construction: the values, types and prototypes config JS sees
// are goja's own. A JS-level wrapper cannot manage that — a Proxy around Date
// makes `x instanceof Date` throw in goja, and a plain wrapper function breaks
// Date.prototype and `class X extends Date`.
//
// A Date built from explicit arguments (`new Date(2020, 0, 1)`) never reaches
// the time source, so it is correctly not recorded: it is a pure function of
// its arguments and must not cost a config its cache entry.
func (e *Engine) initNonDeterminismShims() {
	e.vm.SetTimeSource(func() time.Time {
		e.observeNonDeterminism(sourceClock)
		return time.Now()
	})
	e.vm.SetRandSource(func() float64 {
		e.observeNonDeterminism(sourceRand)
		//nolint:gosec // config-visible Math.random, not a security primitive;
		// this reproduces goja's own default source.
		return rand.Float64()
	})
}

// observeNonDeterminism records the first source read; later reads do not
// overwrite it, so the reported source is the one that first cost the config its
// cache entry.
func (e *Engine) observeNonDeterminism(source string) {
	if e.nonDeterminism == "" {
		e.nonDeterminism = source
	}
}
