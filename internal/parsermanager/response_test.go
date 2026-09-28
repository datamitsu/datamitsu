package parsermanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeResponse(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		exit       int32
		abi        int
		recognized bool
		format     string
		messages   []string
	}{
		{name: "v1 findings", data: `[{"message":"a"}]`, exit: 1, abi: 1, recognized: true, messages: []string{"a"}},
		{name: "v1 empty on exit 0 is a clean run", data: `[]`, exit: 0, abi: 1, recognized: true},
		{name: "v1 empty on a failure recognized nothing", data: `[]`, exit: 2, abi: 1},
		{name: "v1 null reads as empty", data: `null`, exit: 1, abi: 1},
		{
			name: "v2 findings", data: `{"recognized":true,"format":"sarif","diagnostics":[{"message":"b","row":3}]}`,
			exit: 1, abi: 2, recognized: true, format: "sarif", messages: []string{"b"},
		},
		{
			name: "v2 recognized and clean under a failure", data: ` {"recognized":true,"format":"eslint","diagnostics":[]}`,
			exit: 1, abi: 2, recognized: true, format: "eslint",
		},
		{name: "v2 not recognized", data: `{"recognized":false,"format":"gcc","diagnostics":[]}`, exit: 0, abi: 2, format: "gcc"},
		{name: "v2 diagnostics absent", data: "\n{\"recognized\":false}", exit: 1, abi: 2},
		{
			name: "v2 fields a newer module adds are ignored",
			data: `{"recognized":true,"format":"sarif","confidence":0.9,"diagnostics":[{"message":"c","extra":{"x":1}}]}`,
			exit: 0, abi: 2, recognized: true, format: "sarif", messages: []string{"c"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeResponse([]byte(tt.data), tt.exit)
			if err != nil {
				t.Fatalf("DecodeResponse() error = %v", err)
			}
			if got.ABI != tt.abi || got.Recognized != tt.recognized || got.Format != tt.format {
				t.Errorf("DecodeResponse() = abi %d recognized %v format %q, want %d %v %q",
					got.ABI, got.Recognized, got.Format, tt.abi, tt.recognized, tt.format)
			}
			if got.Diagnostics == nil {
				t.Error("Diagnostics = nil, want a list")
			}
			if msgs := messages(got.Diagnostics); strings.Join(msgs, ",") != strings.Join(tt.messages, ",") {
				t.Errorf("messages = %v, want %v", msgs, tt.messages)
			}
		})
	}
}

func TestDecodeResponseRejects(t *testing.T) {
	for _, data := range []string{``, `{`, `"x"`, `{"format":"sarif","diagnostics":[]}`, `{"recognized":"yes"}`} {
		if _, err := DecodeResponse([]byte(data), 0); err == nil {
			t.Errorf("DecodeResponse(%q) error = nil, want one", data)
		}
	}
}

// TestDecodeResponseHandWrittenV2 reads the object form a module of ABI 2
// answers with, written by hand before any such module existed.
func TestDecodeResponseHandWrittenV2(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "response-v2.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := DecodeResponse(data, 1)
	if err != nil {
		t.Fatalf("DecodeResponse() error = %v", err)
	}
	if got.ABI != 2 || !got.Recognized || got.Format != "checkstyle-xml" || len(got.Diagnostics) != 2 {
		t.Fatalf("DecodeResponse() = %+v", got)
	}
	d := got.Diagnostics[1]
	if d.File == nil || *d.File != "src/b.sh" || d.Row == nil || *d.Row != 7 || d.Code == nil || *d.Code != "SC2086" {
		t.Errorf("second diagnostic = %+v", d)
	}
}

func TestNormalizeSchema(t *testing.T) {
	tests := []struct {
		name       string
		in         Capabilities
		wantSchema int
		wantABI    int
	}{
		{"schema 1 answers arrays", Capabilities{SchemaVersion: 1}, 1, 1},
		{"schema 2 answers arrays", Capabilities{SchemaVersion: 2}, 2, 1},
		{"schema 3 declares its abi", Capabilities{SchemaVersion: 3, ABI: 2}, 3, 2},
		{"a newer schema is read as the newest known", Capabilities{SchemaVersion: 99, ABI: 2}, SchemaNewest, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := tt.in
			normalizeSchema(&caps)
			if caps.SchemaVersion != tt.wantSchema || caps.ABI != tt.wantABI {
				t.Errorf("normalizeSchema() = schema %d abi %d, want %d %d", caps.SchemaVersion, caps.ABI, tt.wantSchema, tt.wantABI)
			}
		})
	}
}

// TestParseAnswersOfTheCommittedModules reads the ABI 1 answers of the module
// the core is tested against and of the released one.
func TestParseAnswersOfTheCommittedModules(t *testing.T) {
	ctx := context.Background()
	modules := map[string][]byte{"echo.wasm": echoWASM(t), "released v1": releasedV1(t)}
	for name, wasm := range modules {
		t.Run(name, func(t *testing.T) {
			clean, err := ParseLocal(ctx, wasm, "yamllint", nil, nil, 0)
			if err != nil {
				t.Fatalf("ParseLocal() error = %v", err)
			}
			if !clean.Recognized || len(clean.Diagnostics) != 0 {
				t.Errorf("clean run = %+v, want recognized and empty", clean)
			}
			failed, err := ParseLocal(ctx, wasm, "yamllint", []byte("not yamllint output"), nil, 1)
			if err != nil {
				t.Fatalf("ParseLocal() error = %v", err)
			}
			if failed.ABI == 1 && failed.Recognized {
				t.Errorf("an empty ABI 1 answer on a failure = %+v, want not recognized", failed)
			}
		})
	}
}
