package inspector

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

func TestSnapshotPortableMetadata(t *testing.T) {
	cfg := &config.Config{
		Apps:           binmanager.MapOfApps{"z": {Node: &binmanager.AppConfigNode{Version: "1.2.3", LockFile: "private-lock"}, Env: map[string]string{"TOKEN": "secret-env"}, Files: map[string]string{"private": "secret-file"}}, "a": {Shell: &binmanager.AppConfigShell{Name: "echo"}, DependsOn: []string{"z"}}},
		Tools:          config.MapOfTools{"check": {Name: "Check", Skip: true, SkipReason: "CI only", Operations: map[config.OperationType]config.ToolOperation{config.OpLint: {App: "z", Args: []string{"secret-argument"}, Env: map[string]string{"TOKEN": "secret-operation"}, Globs: []string{"**/*.ts"}, ExcludeGlobs: []string{"**/vendor/**"}, Priority: 9}}}},
		ManagedConfigs: config.MapOfManagedConfigs{"tool.js": {Tools: []string{"check"}, DeleteOnly: true, Content: "secret-content"}},
		ProjectTypes:   config.MapOfProjectTypes{"typescript": {Markers: []string{"tsconfig.json"}}},
	}
	m := Snapshot(cfg, "test")
	if m.Apps[0].Name != "a" || m.Apps[1].Runtime != "node" || m.Apps[1].Version != "1.2.3" {
		t.Fatalf("unexpected apps: %+v", m.Apps)
	}
	op := m.Tools[0].Operations[0]
	if !m.Tools[0].Skipped || op.Priority != 9 || op.Scope != config.ToolScopePerProject || len(op.ExcludeGlobs) != 1 {
		t.Fatalf("operation metadata lost: %+v", m.Tools)
	}
	if !m.ManagedConfigs[0].DeleteOnly || m.ManagedConfigs[0].Tools[0] != "check" {
		t.Fatal("managed ownership lost")
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-", "private-lock"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("export includes %q", secret)
		}
	}
	m.Apps[0].DependsOn[0] = "changed"
	if cfg.Apps["a"].DependsOn[0] != "z" {
		t.Fatal("snapshot aliases config data")
	}
}

func TestSnapshotCarriesTheConfigurationName(t *testing.T) {
	if got := Snapshot(&config.Config{}, "test").Name; got != config.DefaultName {
		t.Fatalf("unnamed config exported as %q, want %q", got, config.DefaultName)
	}
	if got := Snapshot(&config.Config{Name: "@acme/config"}, "test").Name; got != "@acme/config" {
		t.Fatalf("named config exported as %q", got)
	}
}

func TestRenderEscapesScriptTermination(t *testing.T) {
	m := Snapshot(&config.Config{}, "test")
	hostile := "</script><script>globalThis.pwned=true</script>\u2028&<>"
	m.Tools = append(m.Tools, Tool{ID: "hostile", Name: hostile})
	theme, err := DefaultTheme()
	if err != nil {
		t.Fatal(err)
	}
	html, err := Render(m, theme)
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	if strings.Contains(text, protocol.ThemePlaceholder) || !strings.Contains(text, "--surface-base:") {
		t.Fatal("theme was not baked into the artifact")
	}
	if !strings.Contains(text, "<style>") || strings.Contains(text, "<style></style>") {
		t.Fatal("embedded stylesheet is missing")
	}
	if strings.Contains(text, hostile) || strings.Contains(text, protocol.Placeholder) {
		t.Fatal("unsafe or unresolved manifest")
	}
	prefix := `<script type="application/json" id="inspector-manifest">`
	_, rest, ok := strings.Cut(text, prefix)
	if !ok {
		t.Fatal("missing manifest element")
	}
	raw, _, ok := strings.Cut(rest, "</script>")
	if !ok {
		t.Fatal("missing end element")
	}
	var decoded Manifest
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Tools[0].Name != hostile {
		t.Fatal("config text did not round trip")
	}
	if strings.Contains(text, `src="/src/`) || strings.Contains(text, `<link rel="stylesheet"`) {
		t.Fatal("artifact depends on external assets")
	}
}

func TestHandlerSnapshotOnly(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		status       int
		body         string
	}{{"GET", "/", 200, "snapshot"}, {"HEAD", "/index.html", 200, ""}, {"POST", "/", 405, "method not allowed\n"}, {"GET", "/config.json", 404, "404 page not found\n"}, {"GET", "/../go.mod", 404, "404 page not found\n"}} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			Handler([]byte("snapshot")).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status || w.Body.String() != tc.body {
				t.Fatalf("response = %d %q", w.Code, w.Body.String())
			}
			if tc.status == 200 && w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatal("incorrect content type")
			}
		})
	}
}

func TestServeCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listener, []byte("snapshot")) }()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "snapshot" {
		t.Fatalf("read: %s %v", body, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}
