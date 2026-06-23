package clitest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewProjectResolvesAsGitRoot(t *testing.T) {
	p := NewProject(t)

	// git must regard the project dir itself as the top-level work tree.
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "--show-toplevel")
	cmd.Dir = p.Dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse in project: %v", err)
	}
	toplevel := strings.TrimSpace(string(out))
	if toplevel != p.Dir {
		t.Fatalf("git top-level = %q, want project dir %q", toplevel, p.Dir)
	}
}

func TestProjectWriteFile(t *testing.T) {
	p := NewProject(t)
	abs := p.WriteFile("nested/dir/file.txt", "hello\n")
	if want := filepath.Join(p.Dir, "nested", "dir", "file.txt"); abs != want {
		t.Fatalf("WriteFile returned %q, want %q", abs, want)
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != "hello\n" {
		t.Fatalf("file content = %q, want %q", got, "hello\n")
	}
}

func TestConfigShowAgainstMinimalConfig(t *testing.T) {
	p := NewProject(t)
	cfgPath := WriteMinimalConfig(p)

	res := Run(t, RunOptions{Dir: p.Dir}, "config", "show", "--no-auto-config", "--config", cfgPath)
	if res.ExitCode != 0 {
		t.Fatalf("config show exit = %d, want 0\nstdout: %s\nstderr: %s", res.ExitCode, res.Stdout, res.Stderr)
	}

	// Output must be valid JSON with the empty collections the minimal config sets.
	var parsed struct {
		Apps     map[string]any `json:"apps"`
		Runtimes map[string]any `json:"runtimes"`
		Tools    map[string]any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &parsed); err != nil {
		t.Fatalf("config show output is not valid JSON: %v\noutput: %s", err, res.Stdout)
	}
	if len(parsed.Apps) != 0 {
		t.Errorf("minimal config apps = %v, want empty", parsed.Apps)
	}
	if len(parsed.Runtimes) != 0 {
		t.Errorf("minimal config runtimes = %v, want empty", parsed.Runtimes)
	}
	if len(parsed.Tools) != 0 {
		t.Errorf("minimal config tools = %v, want empty", parsed.Tools)
	}
}

func TestWriteOverlayAndIgnore(t *testing.T) {
	p := NewProject(t)
	overlay := WriteOverlayConfig(p, "/abs/before.js", "return { ...config, tools: {} };")
	if want := filepath.Join(p.Dir, "datamitsu.config.js"); overlay != want {
		t.Fatalf("overlay path = %q, want %q", overlay, want)
	}
	data, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	body := string(data)
	for _, want := range []string{`getBeforeConfigs`, `"/abs/before.js"`, `tools: {}`, `getMinVersion`} {
		if !strings.Contains(body, want) {
			t.Errorf("overlay config missing %q\n%s", want, body)
		}
	}

	ignore := WriteDatamitsuIgnore(p, []string{"**/*: *", "!eslint"})
	idata, err := os.ReadFile(ignore)
	if err != nil {
		t.Fatalf("read ignore: %v", err)
	}
	if got := string(idata); got != "**/*: *\n!eslint\n" {
		t.Fatalf(".datamitsuignore = %q, want %q", got, "**/*: *\n!eslint\n")
	}
}

func TestJSStringEscaping(t *testing.T) {
	cases := map[string]string{
		`plain`:         `"plain"`,
		`with"quote`:    `"with\"quote"`,
		`back\slash`:    `"back\\slash"`,
		"line\nbreak":   `"line\nbreak"`,
		`C:\tmp\cfg.js`: `"C:\\tmp\\cfg.js"`,
	}
	for in, want := range cases {
		if got := jsString(in); got != want {
			t.Errorf("jsString(%q) = %q, want %q", in, got, want)
		}
	}
}
