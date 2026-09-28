package render

import (
	"bytes"
	"errors"
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
	tests := []struct {
		name, path, want string
	}{
		{name: "a file in the way", path: filepath.Join(blocker, "run.json"), want: "not a directory"},
		{name: "a directory at the path", path: dir, want: "is a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := Open(Spec{Format: "json", Path: tt.path}, nil)
			if target.Err == nil || !strings.Contains(target.Err.Error(), tt.want) {
				t.Fatalf("Open error = %v, want %q", target.Err, tt.want)
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
