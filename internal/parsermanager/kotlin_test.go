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

func TestKotlinRecordingsThroughWASM(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	wasm := echoWASM(t)
	server, _ := serveWASM(t, wasm)
	manager := New(config.MapOfParsers{"core": {URL: server.URL, Hash: sha256Hex(wasm)}})
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	ctx := context.Background()

	for _, parser := range []string{"ktfmt", "ktlint"} {
		t.Run(parser, func(t *testing.T) {
			ok, err := manager.HasParser(ctx, "core", parser)
			if err != nil || !ok {
				t.Fatalf("HasParser(%s) = %v, %v", parser, ok, err)
			}
			facts, ok := manager.DescribedParser("core", parser)
			if !ok || facts.Tool.ColumnUnit != "utf-16" {
				t.Fatalf("descriptor = %+v, want measured UTF-16 columns", facts)
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
				if len(answer.Diagnostics) != 2 {
					t.Fatalf("findings = %+v, want two", answer.Diagnostics)
				}
				for _, diagnostic := range answer.Diagnostics {
					if diagnostic.File == nil || diagnostic.Source == nil || *diagnostic.Source != parser {
						t.Fatalf("lost file or tool identity: %+v", diagnostic)
					}
				}
				if parser == "ktfmt" {
					difference := answer.Diagnostics[0]
					if difference.Row != nil || difference.Col != nil || difference.Severity != nil || *difference.File != "/work/Unformatted.kt" {
						t.Fatalf("formatting difference gained unprinted fields: %+v", difference)
					}
				} else {
					naming := answer.Diagnostics[1]
					if naming.Col == nil || *naming.Col != 23 || naming.Code == nil || *naming.Code != "standard:function-naming" {
						t.Fatalf("lost UTF-16 position or rule: %+v", naming)
					}
				}
			}
		})
	}
}
