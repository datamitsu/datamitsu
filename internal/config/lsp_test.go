package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfig_LspJSONRoundTrip(t *testing.T) {
	jsonStr := `{
		"lsp": {
			"langserver": {"type": "proxy", "app": "langserver", "projectTypes": ["go"], "order": 10},
			"golangci": {"type": "derived", "tool": "golangci-lint"}
		}
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	proxy, ok := cfg.Lsp["langserver"]
	if !ok {
		t.Fatal("expected lsp \"langserver\" to be present")
	}
	if proxy.Type != LspTypeProxy || proxy.App != "langserver" || proxy.Order != 10 || len(proxy.ProjectTypes) != 1 {
		t.Errorf("unexpected proxy values: %+v", proxy)
	}
	derived, ok := cfg.Lsp["golangci"]
	if !ok {
		t.Fatal("expected lsp \"golangci\" to be present")
	}
	if derived.Type != LspTypeDerived || derived.Tool != "golangci-lint" {
		t.Errorf("unexpected derived values: %+v", derived)
	}
}

func TestConfig_LspOmitEmpty(t *testing.T) {
	data, err := json.Marshal(Config{})
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	if strings.Contains(string(data), "lsp") {
		t.Errorf("empty Config should omit lsp field; got: %s", data)
	}
}

func TestValidateLsp_NilAndEmptyAreValid(t *testing.T) {
	if err := ValidateLsp(nil, nil); err != nil {
		t.Errorf("ValidateLsp(nil) unexpected error: %v", err)
	}
	if err := ValidateLsp(MapOfLsp{}, nil); err != nil {
		t.Errorf("ValidateLsp(empty) unexpected error: %v", err)
	}
}

func TestValidateLsp_ValidProxyAndDerived(t *testing.T) {
	tools := MapOfTools{"golangci-lint": {Name: "golangci-lint"}}
	lsp := MapOfLsp{
		"langserver": {Type: LspTypeProxy, App: "langserver", ProjectTypes: []string{"go"}, Order: 10},
		"golangci":   {Type: LspTypeDerived, Tool: "golangci-lint"},
	}
	if err := ValidateLsp(lsp, tools); err != nil {
		t.Errorf("ValidateLsp() unexpected error: %v", err)
	}
}

func TestValidateLsp_MissingType(t *testing.T) {
	lsp := MapOfLsp{"x": {App: "langserver", ProjectTypes: []string{"go"}}}
	err := ValidateLsp(lsp, nil)
	if err == nil {
		t.Fatal("ValidateLsp() expected error for missing type, got nil")
	}
	if !strings.Contains(err.Error(), `lsp "x": type is required`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateLsp_InvalidType(t *testing.T) {
	lsp := MapOfLsp{"x": {Type: "bogus"}}
	err := ValidateLsp(lsp, nil)
	if err == nil {
		t.Fatal("ValidateLsp() expected error for invalid type, got nil")
	}
	if !strings.Contains(err.Error(), `lsp "x": type "bogus" is invalid`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateLsp_ProxyMissingApp(t *testing.T) {
	lsp := MapOfLsp{"x": {Type: LspTypeProxy, ProjectTypes: []string{"go"}}}
	err := ValidateLsp(lsp, nil)
	if err == nil {
		t.Fatal("ValidateLsp() expected error for proxy missing app, got nil")
	}
	if !strings.Contains(err.Error(), `lsp "x": proxy requires app`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateLsp_ProxyEmptyProjectTypes(t *testing.T) {
	lsp := MapOfLsp{"x": {Type: LspTypeProxy, App: "langserver"}}
	err := ValidateLsp(lsp, nil)
	if err == nil {
		t.Fatal("ValidateLsp() expected error for proxy with empty projectTypes, got nil")
	}
	if !strings.Contains(err.Error(), `lsp "x": proxy requires a non-empty projectTypes`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateLsp_DerivedMissingTool(t *testing.T) {
	lsp := MapOfLsp{"x": {Type: LspTypeDerived}}
	err := ValidateLsp(lsp, nil)
	if err == nil {
		t.Fatal("ValidateLsp() expected error for derived missing tool, got nil")
	}
	if !strings.Contains(err.Error(), `lsp "x": derived requires tool`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateLsp_DerivedDanglingTool(t *testing.T) {
	lsp := MapOfLsp{"x": {Type: LspTypeDerived, Tool: "nonexistent"}}
	err := ValidateLsp(lsp, MapOfTools{"golangci-lint": {Name: "golangci-lint"}})
	if err == nil {
		t.Fatal("ValidateLsp() expected error for dangling derived tool, got nil")
	}
	if !strings.Contains(err.Error(), `lsp "x": derived references unknown tool "nonexistent"`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateLsp_AggregatesAllErrors(t *testing.T) {
	lsp := MapOfLsp{
		"a": {Type: "bogus"},
		"b": {Type: LspTypeProxy},
		"c": {Type: LspTypeDerived, Tool: "missing"},
	}
	err := ValidateLsp(lsp, nil)
	if err == nil {
		t.Fatal("ValidateLsp() expected aggregated errors, got nil")
	}
	for _, want := range []string{`lsp "a"`, `lsp "b"`, `lsp "c"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %s; got: %v", want, err)
		}
	}
}
