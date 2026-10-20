// Package embedded holds the fallback parser module built into datamitsu: the
// format parsers and the sniffer of parsers/datamitsu-parsers, built without
// the tool parsers (`task build:parsers:embedded`). The core runs it on output
// no declared parser recognized.
//
// It is part of the binary, versioned with it, and not a distribution channel:
// a configuration's `parsers` pin its own module, which this one never
// replaces. The bytes are committed and guarded twice: a CI job rebuilds them
// in a digest-pinned container and compares, and a Go test compares the
// fingerprint of their sources with the one committed beside them.
package embedded

import (
	_ "embed"
	"sync"

	"github.com/datamitsu/datamitsu/internal/digest"
)

//go:embed fallback.wasm
var module []byte

// Module is the embedded module's bytes.
func Module() []byte {
	return module
}

var contentKey = sync.OnceValue(func() string {
	return digest.XXH3Multi([]byte("embedded-parser-v1"), module).Hex()
})

// ContentKey identifies the embedded module's bytes. Two builds that report
// the same version can embed different modules, so every cache whose entries
// depend on what the fallback parsed carries it. XXH3: it is compared only
// with keys this binary computed.
func ContentKey() string {
	return contentKey()
}
