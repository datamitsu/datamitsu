package parsermanager

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func TestToolingStrategyRecordingsThroughWASM(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	wasm := echoWASM(t)
	server, _ := serveWASM(t, wasm)
	manager := New(config.MapOfParsers{"core": {URL: server.URL, Hash: sha256Hex(wasm)}})
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	ctx := context.Background()
	for _, parser := range []string{"ty", "sqruff", "vacuum"} {
		t.Run(parser, func(t *testing.T) {
			if ok, err := manager.HasParser(ctx, "core", parser); err != nil || !ok {
				t.Fatalf("HasParser(%s) = %v, %v", parser, ok, err)
			}
			facts, ok := manager.DescribedParser("core", parser)
			if !ok || facts.Tool.ColumnUnit != "utf-32" {
				t.Fatalf("descriptor = %+v, want measured Unicode scalar columns", facts)
			}
			for _, name := range []string{"clean", "findings"} {
				read := func(extension string) []byte {
					t.Helper()
					data, err := os.ReadFile(filepath.Join("..", "..", "parsers", "datamitsu-parsers", "fixtures", parser, name+"."+extension))
					if err != nil {
						t.Fatal(err)
					}
					return data
				}
				exit, err := strconv.ParseInt(strings.TrimSpace(string(read("exit"))), 10, 32)
				if err != nil {
					t.Fatal(err)
				}
				answer, err := manager.ParseOutput(ctx, "core", parser, read("stdout"), read("stderr"), int32(exit))
				if err != nil || !answer.Recognized || answer.Format != parser {
					t.Fatalf("ParseOutput(%s, %s) = %+v, %v", parser, name, answer, err)
				}
				if name == "clean" {
					if len(answer.Diagnostics) != 0 {
						t.Fatalf("clean run has findings: %+v", answer.Diagnostics)
					}
					continue
				}
				count := 1
				if parser == "ty" {
					count = 2
				}
				if len(answer.Diagnostics) != count {
					t.Fatalf("findings = %+v, want %d", answer.Diagnostics, count)
				}
				if parser == "ty" && (answer.Diagnostics[0].Col == nil || *answer.Diagnostics[0].Col != 26) {
					t.Fatalf("lost the measured Unicode scalar position: %+v", answer.Diagnostics[0])
				}
				if parser == "sqruff" && (answer.Diagnostics[0].Col == nil || *answer.Diagnostics[0].Col != 20) {
					t.Fatalf("lost the measured Unicode scalar position: %+v", answer.Diagnostics[0])
				}
				for _, diagnostic := range answer.Diagnostics {
					if diagnostic.File == nil || diagnostic.Row == nil || diagnostic.Col == nil ||
						diagnostic.Code == nil || diagnostic.Severity == nil || diagnostic.Source == nil || *diagnostic.Source != parser {
						t.Fatalf("lost file, position, rule, level or source: %+v", diagnostic)
					}
				}
			}
		})
	}
}
