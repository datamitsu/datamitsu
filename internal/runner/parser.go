package runner

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/datamitsu/datamitsu/internal/config"
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
	Fallback(ctx context.Context, stdout, stderr []byte, exitCode int32) (parsermanager.Response, error)
	DescribedParser(module, parser string) (parsermanager.ParserFacts, bool)
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
) (tooling.ParseAnswer, error) {
	// A module answers a key it does not know with an empty result, which would
	// read as a clean run.
	known, err := p.mgr.HasParser(ctx, module, parser)
	if err != nil {
		p.problems.moduleUnavailable(module, toolName, err)
		return tooling.ParseAnswer{}, &tooling.ParserUnavailableError{Err: err}
	}
	if !known {
		p.problems.unknownParser(module, parser, toolName)
		return tooling.ParseAnswer{}, &tooling.ParserUnavailableError{
			Err: fmt.Errorf("parser module %q has no parser %q", module, parser),
		}
	}
	resp, err := p.mgr.ParseOutput(ctx, module, parser, stdout, stderr, exitCode)
	if err != nil {
		if errors.Is(err, parsermanager.ErrModuleUnavailable) {
			p.problems.moduleUnavailable(module, toolName, err)
			return tooling.ParseAnswer{}, &tooling.ParserUnavailableError{Err: err}
		}
		p.problems.parseFailed(toolName, err)
		return tooling.ParseAnswer{}, err
	}
	facts, _ := p.mgr.DescribedParser(module, parser)
	return answerOf(resp, toolName, exitCode, facts.Tool.Kind == "format"), nil
}

// Fallback runs the sniffer of the module the binary embeds. Its failure is
// the binary's, not a configuration's, so it is left to the caller's debug log.
func (p diagnosticParser) Fallback(ctx context.Context, toolName string, stdout, stderr []byte, exitCode int32) (tooling.ParseAnswer, error) {
	resp, err := p.mgr.Fallback(ctx, stdout, stderr, exitCode)
	if err != nil {
		return tooling.ParseAnswer{}, fmt.Errorf("fallback parser: %w", err)
	}
	return answerOf(resp, toolName, exitCode, true), nil
}

// FellBack records that the fallback read what a declared parser did not.
func (p diagnosticParser) FellBack(toolName string, declared config.OutputParser, format string) {
	p.problems.fellBack(toolName, declared, format)
}

// Unrecognized records a declared parser that read nothing of an output no
// standard format described either.
func (p diagnosticParser) Unrecognized(toolName string, declared config.OutputParser) {
	p.problems.unrecognized(toolName, declared)
}

func answerOf(resp parsermanager.Response, toolName string, exitCode int32, format bool) tooling.ParseAnswer {
	return tooling.ParseAnswer{
		Diagnostics:  diagnostic.ResolveAll(resp.Diagnostics, toolName, exitCode != 0),
		Recognized:   resp.Recognized,
		Format:       resp.Format,
		FormatParser: format,
	}
}

// parseProblems gathers, across the parses of a run, what could not be parsed:
// a module that did not load, a parser key its module does not list, a parse
// that failed, a declared parser the fallback stood in for, and one that
// recognized nothing no standard format described either. Each is reported
// once per run however many invocations hit it; the first seen is the one
// reported.
type parseProblems struct {
	mu          sync.Mutex
	modules     map[string]string             // module -> first load error
	moduleTools map[string]map[string]bool    // module -> tools whose output it did not parse
	unknown     map[[2]string]map[string]bool // (module, parser) -> tools naming it
	failed      map[string]string             // tool -> first parse error
	fellBackTo  map[string]string             // tool -> "<declared> ... <format>" of the first fallback
	readNothing map[string]string             // tool -> the declared parser that recognized nothing
	reported    map[string]bool               // problems already reported this run
}

func newParseProblems() *parseProblems {
	return &parseProblems{
		modules:     map[string]string{},
		moduleTools: map[string]map[string]bool{},
		unknown:     map[[2]string]map[string]bool{},
		failed:      map[string]string{},
		fellBackTo:  map[string]string{},
		readNothing: map[string]string{},
		reported:    map[string]bool{},
	}
}

func (p *parseProblems) fellBack(tool string, declared config.OutputParser, format string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, seen := p.fellBackTo[tool]; !seen {
		p.fellBackTo[tool] = fmt.Sprintf("declared parser %q of module %q did not recognize the output of %s; "+
			"the fallback parsed it as %s", declared.Parser, declared.Module, tool, format)
	}
}

func (p *parseProblems) unrecognized(tool string, declared config.OutputParser) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, seen := p.readNothing[tool]; !seen {
		p.readNothing[tool] = fmt.Sprintf("declared parser %q of module %q did not recognize the output of %s, "+
			"nor did any standard format, so its findings are unknown and its lint passes are not cached",
			declared.Parser, declared.Module, tool)
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
// that did not load, unknown parser keys, failed parses, then the outputs a
// declared parser did not recognize — read by the fallback, then read by
// nothing.
func (p *parseProblems) pending() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, module := range sortedKeys(p.modules) {
		if p.claim("module\x00" + module) {
			out = append(out, fmt.Sprintf(
				"parser module %q could not be loaded, so %d tool(s) that use it ran without it "+
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
			out = append(out, fmt.Sprintf("parser module %q has no parser %q, so the output of %s is not parsed by it "+
				"and its lint passes are not cached",
				key[0], key[1], strings.Join(sortedKeys(p.unknown[key]), ", ")))
		}
	}
	for _, tool := range sortedKeys(p.failed) {
		if p.claim("tool\x00" + tool) {
			out = append(out, fmt.Sprintf("output parser failed for %s: %s", tool, p.failed[tool]))
		}
	}
	for _, tool := range sortedKeys(p.fellBackTo) {
		if p.claim("fell-back\x00" + tool) {
			out = append(out, p.fellBackTo[tool])
		}
	}
	for _, tool := range sortedKeys(p.readNothing) {
		if p.claim("unrecognized\x00" + tool) {
			out = append(out, p.readNothing[tool])
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
