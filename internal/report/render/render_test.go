package render

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
)

// fakeRenderer stands in for the formats later plans add, so the rules that
// only bite with two formats are tested now.
type fakeRenderer struct{ name string }

func (f fakeRenderer) Name() string                                         { return f.name }
func (fakeRenderer) Options() []string                                      { return []string{"category"} }
func (fakeRenderer) Render(io.Writer, *report.Run, map[string]string) error { return nil }
func (fakeRenderer) OmitsIncompleteTools() bool                             { return true }

func withFake(t *testing.T) {
	t.Helper()
	prev := renderers
	renderers = append(append([]Renderer{}, prev...), fakeRenderer{name: "fake"})
	t.Cleanup(func() { renderers = prev })
}

func TestParseSpec(t *testing.T) {
	withFake(t)
	tests := []struct {
		raw     string
		want    Spec
		wantErr string
	}{
		{raw: "json=out/run.json", want: Spec{Format: "json", Path: "out/run.json"}},
		{raw: " json=- ", want: Spec{Format: "json", Path: "-"}},
		{raw: `json=C:\out\run.json`, want: Spec{Format: "json", Path: `C:\out\run.json`}},
		{raw: "fake=r.sarif?category=ci", want: Spec{Format: "fake", Path: "r.sarif", Options: map[string]string{"category": "ci"}}},
		{raw: "fake=r.sarif?category=", want: Spec{Format: "fake", Path: "r.sarif", Options: map[string]string{"category": ""}}},
		{raw: "json", wantErr: `"json" is not <format>=<path>`},
		{raw: "=out.json", wantErr: "is not <format>=<path>"},
		{raw: "yaml=out.yaml", wantErr: `unknown report format "yaml" (must be fake, json, junit, markdown, sarif)`},
		{raw: "json=", wantErr: "report json needs a path"},
		{raw: "json=?x=1", wantErr: "report json needs a path"},
		{raw: "json=a.json?x=1", wantErr: `report json takes no option, got "x"`},
		{raw: "fake=a?x=1", wantErr: `report fake has no option "x" (must be category)`},
		{raw: "fake=a?category=1&category=2", wantErr: `names option "category" twice`},
		{raw: "sarif=out/", want: Spec{Format: "sarif", Path: "out/"}},
		{raw: "sarif=out/?category=linux", want: Spec{Format: "sarif", Path: "out/", Options: map[string]string{"category": "linux"}}},
		{raw: "sarif=a.sarif?category=", wantErr: "report sarif: category needs a name"},
		{raw: "json=out/", wantErr: "report json is one file, and out/ names a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := ParseSpec(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseSpec(%q) error = %v, want one containing %q", tt.raw, err, tt.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseSpec(%q) = %+v, %v; want %+v", tt.raw, got, err, tt.want)
			}
		})
	}
}

func TestParseSpecs(t *testing.T) {
	withFake(t)
	tests := []struct {
		name    string
		flags   []string
		env     string
		want    []Spec
		wantErr string
	}{
		{name: "nothing", want: nil},
		{name: "env", env: "json=a.json,fake=b", want: []Spec{{Format: "json", Path: "a.json"}, {Format: "fake", Path: "b"}}},
		{
			name: "a flag wins for its format", flags: []string{"json=flag.json"}, env: "fake=b,json=env.json",
			want: []Spec{{Format: "json", Path: "flag.json"}, {Format: "fake", Path: "b"}},
		},
		{name: "blank env", env: "  ", want: nil},
		{name: "a format twice in flags", flags: []string{"json=a", "json=b"}, wantErr: "invalid --report value: report json is named twice"},
		{name: "a format twice in env", env: "json=a,json=b", wantErr: "invalid DATAMITSU_REPORT value: report json is named twice"},
		{name: "a bad env entry", env: "json=a,", wantErr: "invalid DATAMITSU_REPORT value"},
		{name: "two on stdout", flags: []string{"json=-"}, env: "fake=-", wantErr: "only one report can be written to stdout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSpecs(tt.flags, tt.env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseSpecs() error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseSpecs() = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestNamesAndLookup(t *testing.T) {
	if got := Names(); !reflect.DeepEqual(got, []string{"json", "junit", "markdown", "sarif"}) {
		t.Errorf("Names() = %v, want [json junit markdown sarif]", got)
	}
	r, ok := Lookup("json")
	if !ok || r.OmitsIncompleteTools() || len(r.Options()) != 0 {
		t.Errorf("Lookup(json) = %v, %v; want the own JSON, which lists every tool and takes no option", r, ok)
	}
	// SARIF leaves an incomplete tool out instead of listing it: a narrowed
	// run writes it.
	if got := Listing([]Spec{{Format: "sarif"}}); len(got) != 0 {
		t.Errorf("Listing(sarif) = %v, want it written for a narrowed run", got)
	}
	// Markdown lists findings: a narrowed run refuses it as it refuses json.
	if got := Listing([]Spec{{Format: "markdown"}}); !reflect.DeepEqual(got, []string{"markdown"}) {
		t.Errorf("Listing(markdown) = %v, want it listed", got)
	}
}
