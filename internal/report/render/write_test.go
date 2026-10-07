package render

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
)

func TestTargetWritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out", "run.json")
	target := Open(Spec{Format: "json", Path: path}, nil)
	if target.Err != nil {
		t.Fatal(target.Err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the report exists before it is written: %v", err)
	}
	if err := target.Write(&report.Run{Schema: report.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("report not written: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("the directory holds %d entries, want the report alone", len(entries))
	}
}

type failingRenderer struct{ fakeRenderer }

func (failingRenderer) Render(w io.Writer, _ *report.Run, _ map[string]string) error {
	_, _ = io.WriteString(w, "half a report")
	return errors.New("render failed")
}

// A report that fails to render leaves the one it would have replaced as it
// was, and no temporary file behind.
func TestTargetFailureKeepsTheOldReport(t *testing.T) {
	prev := renderers
	renderers = append(append([]Renderer{}, prev...), failingRenderer{fakeRenderer{name: "failing"}})
	t.Cleanup(func() { renderers = prev })

	dir := t.TempDir()
	path := filepath.Join(dir, "run.json")
	if err := os.WriteFile(path, []byte("the old report"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := Open(Spec{Format: "failing", Path: path}, nil)
	if target.Err != nil {
		t.Fatal(target.Err)
	}
	if err := target.Write(&report.Run{}); err == nil {
		t.Fatal("a failed render wrote the report")
	}
	if got, _ := os.ReadFile(path); string(got) != "the old report" {
		t.Errorf("the old report became %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the directory holds %d entries, want the old report alone", len(entries))
	}
}

func TestTargetStdout(t *testing.T) {
	var out bytes.Buffer
	target := Open(Spec{Format: "json", Path: Stdout}, &out)
	if err := target.Write(&report.Run{Schema: report.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"schema": "datamitsu.report/1"`) {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestTargetCannotOpen(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// The system's message for a file in the way differs between platforms;
	// the one for a directory at the path is datamitsu's own.
	tests := []struct {
		name, path, want string
	}{
		{name: "a file in the way", path: filepath.Join(blocker, "run.json")},
		{name: "a directory at the path", path: dir, want: "is a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := Open(Spec{Format: "json", Path: tt.path}, nil)
			if target.Err == nil || !strings.Contains(target.Err.Error(), tt.want) {
				t.Fatalf("Open error = %v, want one containing %q", target.Err, tt.want)
			}
			if err := target.Write(&report.Run{}); !errors.Is(err, target.Err) {
				t.Errorf("Write error = %v, want the open error", err)
			}
		})
	}
	if target := Open(Spec{Format: "yaml", Path: "x"}, nil); target.Err == nil {
		t.Error("an unknown format opened")
	}
}
