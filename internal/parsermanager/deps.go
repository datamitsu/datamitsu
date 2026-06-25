package parsermanager

import (
	// wazero is the sandboxed, pure-Go WASM runtime that the parser-execution
	// wrapper (added next, see the plan's Task 7) instantiates from the modules
	// this manager downloads and verifies. It is pinned here now so the
	// dependency lands with the artifact manager and `go mod tidy` stays clean;
	// the blank import becomes a real import once the runtime wrapper consumes it.
	_ "github.com/tetratelabs/wazero"
)
