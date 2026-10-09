package appstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/releaseprovider"
)

func TestReleaseManifestValidation(t *testing.T) {
	for _, body := range []string{
		`{"schemaVersion":1,"apps":{},"sources":{},"binaries":{}}`,
		`{"apps":{"x":{"owner":"o","repo":"r","tag":"v1"}},"binaries":{}}`,
		`{"sources":{"s":{"type":"unknown","url":"https://example.test"}},"apps":{},"binaries":{}}`,
		`{"sources":{},"apps":{"x":{"source":"missing","repository":"o/r","tag":"v1"}},"binaries":{}}`,
		`{"sources":{"s":{"type":"gitlab","url":"https://example.test"}},"apps":{"x":{"source":"s","repository":"o/r","tag":"v1","checksums":{"checksums.txt":""}}},"binaries":{}}`,
	} {
		path := filepath.Join(t.TempDir(), "binaryApps.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("invalid manifest accepted: %s", body)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != body {
			t.Fatal("invalid manifest modified")
		}
	}
}

func TestSourceAndIntegrityFingerprint(t *testing.T) {
	app := &AppMetadata{Source: "upstream", Repository: "group/tool", Tag: "v1"}
	source := releaseprovider.Source{Type: "github", URL: "https://github.com"}
	original := ComputeConfigHash(app, []string{"darwin/arm64"}, source)
	for _, changed := range []releaseprovider.Source{{Type: "gitea", URL: source.URL}, {Type: source.Type, URL: "https://elsewhere.test"}, {Type: source.Type, URL: source.URL, APIURL: "https://api.example.test"}, {Type: source.Type, URL: source.URL, TokenEnv: "MY_FORGE_TOKEN"}} {
		if ComputeConfigHash(app, []string{"darwin/arm64"}, changed) == original {
			t.Fatalf("source change did not invalidate hash: %+v", changed)
		}
	}
	app.Hashes = map[string]string{"tool": strings.Repeat("a", 64)}
	if ComputeConfigHash(app, []string{"darwin/arm64"}, source) == original {
		t.Fatal("hash pin change did not invalidate fingerprint")
	}
	source.TokenEnv = "MY_FORGE_TOKEN"
	t.Setenv(source.TokenEnv, "first")
	a := ComputeConfigHash(app, nil, source)
	t.Setenv(source.TokenEnv, "second")
	if ComputeConfigHash(app, nil, source) != a {
		t.Fatal("credential value entered fingerprint")
	}
}
