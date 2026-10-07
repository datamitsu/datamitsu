package report

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func baselineRun() *Run {
	f := func(fp string, kind string) Finding { return Finding{Fingerprint: fp, Kind: kind, Severity: "error"} }
	return &Run{
		Schema:     SchemaVersion,
		Datamitsu:  Producer{Version: "1.0.0", Configuration: "cfg"},
		StartedAt:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		CI:         CIEnvironment{Vendor: "github", SHA: "abc", Ref: "refs/heads/main"},
		Incomplete: []Reason{ReasonToolsFilter},
		Operations: []Operation{
			{Name: "fix", Ran: true, Tools: []ToolRun{{Name: "prettier", Invocations: []Invocation{{Findings: []Finding{
				f(strings.Repeat("b", 64), kindIssue),
			}}}}}},
			{Name: "lint", Ran: true, Tools: []ToolRun{{Name: "eslint", Incomplete: []Reason{ReasonCancelled}, Invocations: []Invocation{{Findings: []Finding{
				f(strings.Repeat("a", 64), kindIssue),
				f(strings.Repeat("b", 64), kindIssue),
				f(strings.Repeat("c", 64), kindSynthetic),
			}}}}}},
		},
	}
}

func TestNewBaseline(t *testing.T) {
	created := time.Date(2026, 9, 28, 12, 0, 0, 0, time.FixedZone("x", 3600))
	b := NewBaseline(baselineRun(), created)
	if b.Schema != BaselineSchema || b.Fingerprint != FingerprintVersion || !b.CreatedAt.Equal(created) || b.CreatedAt.Location() != time.UTC {
		t.Errorf("header = %+v", b)
	}
	if want := []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}; !reflect.DeepEqual(b.Fingerprints, want) {
		t.Errorf("fingerprints = %v, want %v: sorted, unique, no synthetic", b.Fingerprints, want)
	}
	if want := []Reason{ReasonCancelled, ReasonToolsFilter}; !reflect.DeepEqual(b.Source.Incomplete, want) {
		t.Errorf("source.incomplete = %v, want %v", b.Source.Incomplete, want)
	}
	if b.Source.CI.SHA != "abc" || b.Datamitsu.Configuration != "cfg" {
		t.Errorf("source = %+v, datamitsu = %+v", b.Source, b.Datamitsu)
	}

	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema", "fingerprint", "createdAt", "datamitsu", "source", "fingerprints"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("baseline lacks %q", key)
		}
	}
}

func TestLoadBaseline(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, v any) string {
		path := filepath.Join(dir, name)
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	want := BaselineSet{strings.Repeat("a", 64): true, strings.Repeat("b", 64): true}

	t.Run("baseline", func(t *testing.T) {
		b, err := LoadBaseline(write("b.json", NewBaseline(baselineRun(), time.Now())))
		if err != nil || b.Version != FingerprintVersion || !reflect.DeepEqual(b.Set, want) || b.Complete || b.FromReport {
			t.Errorf("LoadBaseline = %+v, %v", b, err)
		}
	})
	t.Run("report", func(t *testing.T) {
		b, err := LoadBaseline(write("r.json", baselineRun()))
		if err != nil || b.Version != FingerprintVersion || !reflect.DeepEqual(b.Set, want) || !b.FromReport ||
			!reflect.DeepEqual(b.Incomplete, []Reason{ReasonCancelled, ReasonToolsFilter}) {
			t.Errorf("LoadBaseline = %+v, %v", b, err)
		}
	})

	refused := map[string]any{
		"schema":      map[string]any{"schema": "datamitsu.baseline/2", "fingerprint": "dmfp1"},
		"fingerprint": map[string]any{"schema": BaselineSchema, "fingerprint": "dmfp2", "fingerprints": []string{}},
		"not_hex":     map[string]any{"schema": BaselineSchema, "fingerprint": "dmfp1", "fingerprints": []string{"xyz"}},
		"history":     map[string]any{"schema": HistorySchema},
		"report_v2":   map[string]any{"schema": SchemaVersion, "fingerprint": "dmfp2"},
	}
	for name, doc := range refused {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadBaseline(write(name+".json", doc)); !errors.Is(err, ErrBaseline) {
				t.Errorf("LoadBaseline error = %v, want ErrBaseline", err)
			}
		})
	}
	t.Run("not_json", func(t *testing.T) {
		path := filepath.Join(dir, "x.json")
		if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBaseline(path); !errors.Is(err, ErrBaseline) {
			t.Errorf("LoadBaseline error = %v, want ErrBaseline", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		if _, err := LoadBaseline(filepath.Join(dir, "none.json")); err == nil {
			t.Error("a missing file loaded")
		}
	})
}
