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
		{wantErr: "", raw: "json=out/run.json", want: Spec{Options: nil, Format: "json", Path: "out/run.json"}},
		{wantErr: "", raw: " json=- ", want: Spec{Options: nil, Format: "json", Path: "-"}},
		{wantErr: "", raw: `json=C:\out\run.json`, want: Spec{Options: nil, Format: "json", Path: `C:\out\run.json`}},
		{wantErr: "", raw: "fake=r.sarif?category=ci", want: Spec{Format: "fake", Path: "r.sarif", Options: map[string]string{"category": "ci"}}},
		{wantErr: "", raw: "fake=r.sarif?category=", want: Spec{Format: "fake", Path: "r.sarif", Options: map[string]string{"category": ""}}},
		{want: Spec{Format: "", Path: "", Options: nil}, raw: "json", wantErr: `"json" is not <format>=<path>`},
		{want: Spec{Format: "", Path: "", Options: nil}, raw: "=out.json", wantErr: "is not <format>=<path>"},
		{want: Spec{Format: "", Path: "", Options: nil}, raw: "yaml=out.yaml", wantErr: `unknown report format "yaml" (must be checkstyle, codequality, fake, history, json, junit, markdown, patch, rdjsonl, sarif)`},
		{want: Spec{Format: "", Path: "", Options: nil}, raw: "json=", wantErr: "report json needs a path"},
		{want: Spec{Format: "", Path: "", Options: nil}, raw: "json=?x=1", wantErr: "report json needs a path"},
		{want: Spec{Format: "", Path: "", Options: nil}, raw: "json=a.json?x=1", wantErr: `report json takes no option, got "x"`},
		{want: Spec{Format: "", Path: "", Options: nil}, raw: "fake=a?x=1", wantErr: `report fake has no option "x" (must be category)`},
		{want: Spec{Format: "", Path: "", Options: nil}, raw: "fake=a?category=1&category=2", wantErr: `names option "category" twice`},
		{wantErr: "", raw: "sarif=out/", want: Spec{Options: nil, Format: "sarif", Path: "out/"}},
		{wantErr: "", raw: "sarif=out/?category=linux", want: Spec{Format: "sarif", Path: "out/", Options: map[string]string{"category": "linux"}}},
		{want: Spec{Format: "", Path: "", Options: nil}, raw: "sarif=a.sarif?category=", wantErr: "report sarif: category needs a name"},
		{want: Spec{Format: "", Path: "", Options: nil}, raw: "json=out/", wantErr: "report json is one file, and out/ names a directory"},
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
		{flags: nil, env: "", wantErr: "", name: "nothing", want: nil},
		{flags: nil, wantErr: "", name: "env", env: "json=a.json,fake=b", want: []Spec{{Options: nil, Format: "json", Path: "a.json"}, {Options: nil, Format: "fake", Path: "b"}}},
		{
			wantErr: "",
			name:    "a flag wins for its format", flags: []string{"json=flag.json"}, env: "fake=b,json=env.json",
			want: []Spec{{Options: nil, Format: "json", Path: "flag.json"}, {Options: nil, Format: "fake", Path: "b"}},
		},
		{flags: nil, wantErr: "", name: "blank env", env: "  ", want: nil},
		{env: "", want: nil, name: "a format twice in flags", flags: []string{"json=a", "json=b"}, wantErr: "invalid --report value: report json is named twice"},
		{flags: nil, want: nil, name: "a format twice in env", env: "json=a,json=b", wantErr: "invalid DATAMITSU_REPORT value: report json is named twice"},
		{flags: nil, want: nil, name: "a bad env entry", env: "json=a,", wantErr: "invalid DATAMITSU_REPORT value"},
		{want: nil, name: "two on stdout", flags: []string{"json=-"}, env: "fake=-", wantErr: "only one report can be written to stdout"},
		{env: "", want: nil, name: "one path twice", flags: []string{"json=out/a", "markdown=out/./a"}, wantErr: "reports json and markdown would both write"},
		{
			env:  "",
			want: nil,
			name: "a report where another's companion goes", flags: []string{"json=out/r.xml.completeness.json", "junit=out/r.xml"},
			wantErr: "reports json and junit (its completeness companion) would both write",
		},
		{env: "", want: nil, name: "a file a split format owns", flags: []string{"sarif=out/", "json=out/datamitsu-1.sarif"}, wantErr: "reports json and sarif would both write"},
		{
			env:     "",
			wantErr: "",
			name:    "a directory beside its own files", flags: []string{"sarif=out/", "json=out/run.json", "junit=out/junit.xml"},
			want: []Spec{{Options: nil, Format: "sarif", Path: "out/"}, {Options: nil, Format: "json", Path: "out/run.json"}, {Options: nil, Format: "junit", Path: "out/junit.xml"}},
		},
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
	want := []string{"checkstyle", "codequality", "history", "json", "junit", "markdown", "patch", "rdjsonl", "sarif"}
	if got := Names(); !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	r, ok := Lookup("json")
	if !ok || r.OmitsIncompleteTools() || len(r.Options()) != 0 {
		t.Errorf("Lookup(json) = %v, %v; want the own JSON, which lists every tool and takes no option", r, ok)
	}
	// SARIF leaves an incomplete tool out instead of listing it: a narrowed
	// run writes it.
	if got := Listing([]Spec{{Path: "", Options: nil, Format: "sarif"}}); len(got) != 0 {
		t.Errorf("Listing(sarif) = %v, want it written for a narrowed run", got)
	}
	// Markdown lists findings: a narrowed run refuses it as it refuses json.
	if got := Listing([]Spec{{Path: "", Options: nil, Format: "markdown"}}); !reflect.DeepEqual(got, []string{"markdown"}) {
		t.Errorf("Listing(markdown) = %v, want it listed", got)
	}
}

// TestListsFindings: a report of counts is written for a narrowed run and
// leaves fail-fast alone; one that lists findings, even one that omits an
// incomplete tool, turns fail-fast off.
func TestListsFindings(t *testing.T) {
	tests := []struct {
		formats []string
		want    bool
	}{
		{formats: []string{"history"}, want: false},
		{formats: []string{"sarif"}, want: true},
		{formats: []string{"history", "json"}, want: true},
		{formats: nil, want: false},
	}
	for _, tt := range tests {
		specs := make([]Spec, 0, len(tt.formats))
		for _, f := range tt.formats {
			specs = append(specs, Spec{Path: "", Options: nil, Format: f})
		}
		if got := ListsFindings(specs); got != tt.want {
			t.Errorf("ListsFindings(%v) = %v, want %v", tt.formats, got, tt.want)
		}
	}
	if got := Listing([]Spec{{Path: "", Options: nil, Format: "history"}}); len(got) != 0 {
		t.Errorf("Listing(history) = %v, want it written for a narrowed run", got)
	}
}
