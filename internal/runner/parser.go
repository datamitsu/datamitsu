package runner

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// parsingDisabledByFlag is set from the --no-parse persistent CLI flag (the env
// twin is DATAMITSU_NO_PARSE). Either switches the failure frame to the tools'
// raw output; parsing itself still runs, because what a run records in the
// cache must not depend on how it is displayed.
var parsingDisabledByFlag atomic.Bool

// SetParsingDisabledByFlag wires the --no-parse CLI flag into the display
// decision.
func SetParsingDisabledByFlag(disabled bool) { parsingDisabledByFlag.Store(disabled) }

// parsingDisabled reports whether the raw output is shown instead of parsed
// findings (flag or env).
func parsingDisabled() bool { return parsingDisabledByFlag.Load() || env.NoParse() }

// parserModules is the part of *parsermanager.Manager a diagnosticParser uses.
type parserModules interface {
	HasParser(ctx context.Context, module, parser string) (bool, error)
	ParseOutput(ctx context.Context, module, parser string, stdout, stderr []byte, exitCode int32) (parsermanager.Response, error)
}

// diagnosticParser adapts the parser manager and the defaults-in-core resolution
// to the tooling.DiagnosticParser interface the executor calls: it runs the WASM
// module for a tool's output and resolves the nullable result into finalized
// diagnostics. What it could not parse it records in problems, for the run to
// report once.
type diagnosticParser struct {
	mgr      parserModules
	problems *parseProblems
}

// newDiagnosticParser adapts a parser Manager to the executor's parser. The
// runner owns the Manager's lifecycle (it must be Closed on shutdown) so the
// compile-once runtime is shared across every per-file parse.
func newDiagnosticParser(mgr parserModules, problems *parseProblems) diagnosticParser {
	return diagnosticParser{mgr: mgr, problems: problems}
}

func (p diagnosticParser) Parse(
	ctx context.Context,
	module, parser, toolName string,
	stdout, stderr []byte,
	exitCode int32,
) ([]diagnostic.Diagnostic, error) {
	// A module answers a key it does not know with an empty result, which would
	// read as a clean run.
	known, err := p.mgr.HasParser(ctx, module, parser)
	if err != nil {
		p.problems.moduleUnavailable(module, toolName, err)
		return nil, &tooling.ParserUnavailableError{Err: err}
	}
	if !known {
		p.problems.unknownParser(module, parser, toolName)
		return nil, &tooling.ParserUnavailableError{
			Err: fmt.Errorf("parser module %q has no parser %q", module, parser),
		}
	}
	resp, err := p.mgr.ParseOutput(ctx, module, parser, stdout, stderr, exitCode)
	if err != nil {
		if errors.Is(err, parsermanager.ErrModuleUnavailable) {
			p.problems.moduleUnavailable(module, toolName, err)
			return nil, &tooling.ParserUnavailableError{Err: err}
		}
		p.problems.parseFailed(toolName, err)
		return nil, err
	}
	return diagnostic.ResolveAll(resp.Diagnostics, toolName, exitCode != 0), nil
}

// parseProblems gathers, across the parses of a run, what could not be parsed:
// a module that did not load, a parser key its module does not list, and a
// parse that failed. Each is reported once per run however many invocations
// hit it; the first error seen is the one reported.
type parseProblems struct {
	mu          sync.Mutex
	modules     map[string]string             // module -> first load error
	moduleTools map[string]map[string]bool    // module -> tools whose output it did not parse
	unknown     map[[2]string]map[string]bool // (module, parser) -> tools naming it
	failed      map[string]string             // tool -> first parse error
	reported    map[string]bool               // problems already reported this run
}

func newParseProblems() *parseProblems {
	return &parseProblems{
		modules:     map[string]string{},
		moduleTools: map[string]map[string]bool{},
		unknown:     map[[2]string]map[string]bool{},
		failed:      map[string]string{},
		reported:    map[string]bool{},
	}
}

func (p *parseProblems) moduleUnavailable(module, tool string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, seen := p.modules[module]; !seen {
		p.modules[module] = err.Error()
		p.moduleTools[module] = map[string]bool{}
	}
	p.moduleTools[module][tool] = true
}

func (p *parseProblems) unknownParser(module, parser, tool string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := [2]string{module, parser}
	if p.unknown[key] == nil {
		p.unknown[key] = map[string]bool{}
	}
	p.unknown[key][tool] = true
}

func (p *parseProblems) parseFailed(tool string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, seen := p.failed[tool]; !seen {
		p.failed[tool] = err.Error()
	}
}

// pending returns the warnings not yet reported, in a stable order: modules
// that did not load, then unknown parser keys, then failed parses.
func (p *parseProblems) pending() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, module := range sortedKeys(p.modules) {
		if p.claim("module\x00" + module) {
			out = append(out, fmt.Sprintf(
				"parser module %q could not be loaded, so %d tool(s) that use it ran without parsing "+
					"and their lint passes are not cached; "+
					"\"datamitsu devtools parsers prefetch\" fetches it ahead of a run: %s",
				module, len(p.moduleTools[module]), p.modules[module]))
		}
	}
	keys := make([][2]string, 0, len(p.unknown))
	for key := range p.unknown {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	for _, key := range keys {
		if p.claim("parser\x00" + key[0] + "\x00" + key[1]) {
			out = append(out, fmt.Sprintf("parser module %q has no parser %q, so the output of %s is not parsed "+
				"and its lint passes are not cached",
				key[0], key[1], strings.Join(sortedKeys(p.unknown[key]), ", ")))
		}
	}
	for _, tool := range sortedKeys(p.failed) {
		if p.claim("tool\x00" + tool) {
			out = append(out, fmt.Sprintf("output parser failed for %s: %s", tool, p.failed[tool]))
		}
	}
	return out
}

// claim marks a problem reported and says whether it was new. Caller holds mu.
func (p *parseProblems) claim(key string) bool {
	if p.reported[key] {
		return false
	}
	p.reported[key] = true
	return true
}

// toolSet is a set of tool names concurrent tasks add to.
type toolSet struct {
	mu    sync.Mutex
	tools map[string]bool
}

func (s *toolSet) add(tool string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tools == nil {
		s.tools = map[string]bool{}
	}
	s.tools[tool] = true
}

// names returns the tools in order; none for a nil set.
func (s *toolSet) names() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedKeys(s.tools)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
