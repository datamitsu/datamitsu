package runner

import (
	"context"
	"sync/atomic"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
)

// parsingDisabledByFlag is set from the --no-parse persistent CLI flag (the env
// twin is DATAMITSU_NO_PARSE). When either is on, the executor is never given a
// parser, so tools show their raw output — handy for debugging a parser.
var parsingDisabledByFlag atomic.Bool

// SetParsingDisabledByFlag wires the --no-parse CLI flag into the parse decision.
func SetParsingDisabledByFlag(disabled bool) { parsingDisabledByFlag.Store(disabled) }

// parsingDisabled reports whether output parsing is off (flag or env).
func parsingDisabled() bool { return parsingDisabledByFlag.Load() || env.NoParse() }

// diagnosticParser adapts the parser manager and the defaults-in-core resolution
// to the tooling.DiagnosticParser interface the executor calls: it runs the WASM
// module for a tool's output and resolves the nullable result into finalized
// diagnostics.
type diagnosticParser struct {
	mgr *parsermanager.Manager
}

// newDiagnosticParser builds the executor's parser over the declared parsers.
func newDiagnosticParser(parsers config.MapOfParsers) diagnosticParser {
	return diagnosticParser{mgr: parsermanager.New(parsers)}
}

func (p diagnosticParser) Parse(
	ctx context.Context,
	parserName, toolName string,
	stdout, stderr []byte,
	exitCode int32,
) ([]diagnostic.Diagnostic, error) {
	// Dispatch on the parser name; the tool name only labels the diagnostic source.
	raws, err := p.mgr.ParseOutput(ctx, parserName, stdout, stderr, exitCode)
	if err != nil {
		return nil, err
	}
	return diagnostic.ResolveAll(raws, toolName), nil
}
