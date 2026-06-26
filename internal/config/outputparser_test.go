package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOutputParser_UnmarshalObject(t *testing.T) {
	var tool Tool
	if err := json.Unmarshal([]byte(`{"operations":{},"outputParser":{"module":"core","parser":"eslint"}}`), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tool.OutputParser == nil || tool.OutputParser.Module != "core" || tool.OutputParser.Parser != "eslint" {
		t.Fatalf("object form: %+v", tool.OutputParser)
	}
}

func TestOutputParser_AbsentIsNil(t *testing.T) {
	var tool Tool
	if err := json.Unmarshal([]byte(`{"operations":{}}`), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tool.OutputParser != nil {
		t.Errorf("absent outputParser should be nil, got %+v", tool.OutputParser)
	}
}

func TestOutputParser_StringIsRejected(t *testing.T) {
	// outputParser is always an object — a plain string is a config error.
	var tool Tool
	err := json.Unmarshal([]byte(`{"operations":{},"outputParser":"hadolint"}`), &tool)
	if err == nil {
		t.Fatal("expected error unmarshaling a string outputParser, got nil")
	}
	if !strings.Contains(err.Error(), "outputParser") && !strings.Contains(err.Error(), "OutputParser") {
		t.Logf("note: error does not mention the field: %v", err)
	}
}
