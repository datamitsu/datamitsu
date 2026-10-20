package appstate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/releaseprovider"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func TestLoadPlatformSelection(t *testing.T) {
	for _, tc := range []struct {
		field string
		valid bool
	}{
		{``, true},
		{`,"platforms":["darwin/arm64","linux/amd64/musl"]`, true},
		{`,"platforms":["darwin/arm64","darwin/arm64"]`, true},
		{`,"platforms":null`, false},
		{`,"platforms":[]`, false},
		{`,"platforms":"darwin/arm64"`, false},
		{`,"platforms":[1]`, false},
		{`,"platforms":[null]`, false},
		{`,"platforms":["Darwin/arm64"]`, false},
		{`,"platforms":["darwin/aarch64"]`, false},
		{`,"platforms":["linux/amd64"]`, false},
		{`,"platforms":[" darwin/arm64"]`, false},
	} {
		t.Run(tc.field, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "apps.json")
			if err := os.WriteFile(path, []byte(`{"apps":{},"binaries":{}`+tc.field+`}`), 0o600); err != nil {
				t.Fatal(err)
			}
			state, err := Load(path)
			if (err == nil) != tc.valid {
				t.Fatalf("Load = %v, valid=%v", err, tc.valid)
			}
			if tc.valid {
				if err := Save(path, state); err != nil {
					t.Fatal(err)
				}
				roundtrip, err := Load(path)
				if err != nil || !slices.Equal(state.Platforms, roundtrip.Platforms) {
					t.Fatalf("roundtrip = %+v, %v", roundtrip, err)
				}
			}
		})
	}
	if err := ValidatePlatforms([]string{"DARWIN/arm64"}); err == nil || !strings.Contains(err.Error(), "supported platforms:") {
		t.Fatalf("expected available values: %v", err)
	}
}

func TestPlatformSelectionHash(t *testing.T) {
	metadata := &AppMetadata{Source: "github", Repository: "o/r", Tag: "v1"}
	a := []string{"darwin/arm64", "linux/amd64/musl"}
	b := []string{"linux/amd64/musl", "darwin/arm64", "darwin/arm64"}
	if ComputeConfigHash(metadata, a) != ComputeConfigHash(metadata, b) {
		t.Fatal("order or duplicates changed hash")
	}
	if ComputeConfigHash(metadata, a) == ComputeConfigHash(metadata, nil) {
		t.Fatal("selection must differ from absent field")
	}
	if ComputeConfigHash(metadata, a) == ComputeConfigHash(metadata, []string{"darwin/arm64"}) {
		t.Fatal("different selection has same hash")
	}
	if !slices.Equal(a, []string{"darwin/arm64", "linux/amd64/musl"}) {
		t.Fatal("hash mutated selection")
	}
}

func TestFilterPlatforms(t *testing.T) {
	bins := binmanager.MapOfBinaries{
		"darwin":  {"arm64": {"unknown": {URL: "keep"}}, "amd64": {"unknown": {URL: "remove"}}},
		"linux":   {"amd64": {"glibc": {URL: "remove"}, "musl": {URL: "keep"}}},
		"windows": {"arm64": {"unknown": {URL: "remove"}}},
	}
	state := &State{Sources: map[string]releaseprovider.Source{"github": {Type: "github", URL: "https://github.com"}}, Binaries: map[string]*BinariesEntry{"orphan": {ConfigHash: "old", Binaries: bins}, "nil": nil}}
	if state.FilterPlatforms() {
		t.Fatal("absent selector filtered binaries")
	}
	state.Platforms = []string{"darwin/arm64", "linux/amd64/musl"}
	if !state.FilterPlatforms() {
		t.Fatal("expected pruning")
	}
	got := state.Binaries["orphan"]
	if len(got.Binaries) != 2 || len(got.Binaries["darwin"]) != 1 || len(got.Binaries["linux"]["amd64"]) != 1 {
		t.Fatalf("unexpected binaries: %+v", got.Binaries)
	}
	if got.ConfigHash != "" {
		t.Fatal("filter retained a stale hash")
	}
	if bins["linux"]["amd64"]["glibc"].URL != "remove" {
		t.Fatal("historical map mutated")
	}
	if state.FilterPlatforms() {
		t.Fatal("second filter should be idempotent")
	}
}
