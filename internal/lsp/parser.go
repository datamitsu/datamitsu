package lsp

import (
	"context"
	"errors"
	"fmt"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// errNoDeclaredParser is what a declared parser answers in the server: it
// loads no module a configuration declares.
var errNoDeclaredParser = errors.New("the language server runs no declared parser")

// fallbackParser is the server's parser: the fallback the binary embeds, and
// no declared module. What the server records in the execution cache the CLI
// reuses, so output the CLI's fallback would read has to be read here too:
// otherwise a finding it holds would stand behind a recorded pass. A tool
// with a declared parser stays parser-unavailable and records no pass.
type fallbackParser struct {
	mgr *parsermanager.Manager
}

// Parse runs a format key of the embedded module, which the executor asks for
// when the sniffer's line format named no file; a declared module is
// unavailable here.
func (p fallbackParser) Parse(ctx context.Context, module, parser, toolName string, stdout, stderr []byte, exitCode int32) (tooling.ParseAnswer, error) {
	if module != tooling.EmbeddedParserModule {
		return tooling.ParseAnswer{}, &tooling.ParserUnavailableError{Err: errNoDeclaredParser}
	}
	resp, err := p.mgr.ParseOutput(ctx, module, parser, stdout, stderr, exitCode)
	if err != nil {
		return tooling.ParseAnswer{}, fmt.Errorf("fallback parser: %w", err)
	}
	return answer(resp, toolName, exitCode), nil
}

func (p fallbackParser) Fallback(ctx context.Context, toolName string, stdout, stderr []byte, exitCode int32) (tooling.ParseAnswer, error) {
	resp, err := p.mgr.Fallback(ctx, stdout, stderr, exitCode)
	if err != nil {
		return tooling.ParseAnswer{}, fmt.Errorf("fallback parser: %w", err)
	}
	return answer(resp, toolName, exitCode), nil
}

func answer(resp parsermanager.Response, toolName string, exitCode int32) tooling.ParseAnswer {
	return tooling.ParseAnswer{
		Diagnostics:  diagnostic.ResolveAll(resp.Diagnostics, toolName, exitCode != 0),
		Recognized:   resp.Recognized,
		Format:       resp.Format,
		FormatParser: true,
		Partial:      resp.Partial,
	}
}

func (fallbackParser) FellBack(string, config.OutputParser, string) {}

func (fallbackParser) Unrecognized(string, config.OutputParser) {}
