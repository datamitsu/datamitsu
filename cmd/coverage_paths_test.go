package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/dockerfile"
	"github.com/spf13/cobra"
)

// TestShortInitType verifies the redundant "-package"/"-project" suffix trim
// used in the compact init header.
func TestShortInitType(t *testing.T) {
	cases := []struct{ in, want string }{
		{"golang-package", "golang"},
		{"node-project", "node"},
		{"typescript", "typescript"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := shortInitType(tc.in); got != tc.want {
			t.Errorf("shortInitType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestIsApplicableInitCommand covers the three branches: no declared project
// types (applies to all), a matching type, and no match.
func TestIsApplicableInitCommand(t *testing.T) {
	cases := []struct {
		name     string
		cmdTypes []string
		project  []string
		want     bool
	}{
		{"no-types-applies-to-all", nil, []string{"go"}, true},
		{"matching-type", []string{"go", "node"}, []string{"node"}, true},
		{"no-match", []string{"go"}, []string{"node"}, false},
		{"no-match-empty-project", []string{"go"}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ic := config.InitCommand{ProjectTypes: tc.cmdTypes}
			if got := isApplicableInitCommand(ic, tc.project); got != tc.want {
				t.Errorf("isApplicableInitCommand = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFailRow covers both the err and nil-err branches of the failure row.
func TestFailRow(t *testing.T) {
	withErr := failRow("lefthook", os.ErrPermission)
	if withErr.name != "lefthook" {
		t.Errorf("name = %q, want lefthook", withErr.name)
	}
	if !strings.Contains(withErr.status, "failed:") || !strings.Contains(withErr.status, os.ErrPermission.Error()) {
		t.Errorf("status missing wrapped error: %q", withErr.status)
	}

	nilErr := failRow("tool", nil)
	if strings.Contains(nilErr.status, ":") {
		t.Errorf("nil-err status should be bare 'failed', got %q", nilErr.status)
	}
}

// TestSkipRowsWithReason covers the with-reason and empty-reason branches.
func TestSkipRowsWithReason(t *testing.T) {
	rows := skipRowsWithReason([]binmanager.SkippedBinary{
		{Name: "ruff", Reason: "no binary for linux/arm64/musl"},
		{Name: "task", Reason: ""},
	})
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if !strings.Contains(rows[0].status, "no binary for linux/arm64/musl") {
		t.Errorf("row 0 status missing reason: %q", rows[0].status)
	}
	if strings.Contains(rows[1].status, ":") {
		t.Errorf("row 1 (no reason) should be bare 'skipped', got %q", rows[1].status)
	}

	if got := skipRowsWithReason(nil); len(got) != 0 {
		t.Errorf("nil input rows = %d, want 0", len(got))
	}
}

// TestBuildConfigLinksRegistry maps link names to their owning app/bundle and
// skips nil bundle entries.
func TestBuildConfigLinksRegistry(t *testing.T) {
	apps := binmanager.MapOfApps{
		"ruff": {Links: map[string]string{"ruff": "bin/ruff"}},
		"node": {Links: map[string]string{"node": "bin/node", "npx": "bin/npx"}},
	}
	bundles := binmanager.MapOfBundles{
		"core": {Links: map[string]string{"task": "bin/task"}},
		"nil":  nil,
	}

	reg := buildConfigLinksRegistry(apps, bundles)
	want := map[string]string{
		"ruff": "ruff",
		"node": "node",
		"npx":  "node",
		"task": "core",
	}
	if len(reg) != len(want) {
		t.Fatalf("registry size = %d, want %d: %v", len(reg), len(want), reg)
	}
	for k, v := range want {
		if reg[k] != v {
			t.Errorf("registry[%q] = %q, want %q", k, reg[k], v)
		}
	}
}

// TestPluralWord covers the singular (n==1) and plural branches.
func TestPluralWord(t *testing.T) {
	if got := pluralWord(1, "tool", "tools"); got != "tool" {
		t.Errorf("pluralWord(1) = %q, want tool", got)
	}
	for _, n := range []int{0, 2, 5} {
		if got := pluralWord(n, "tool", "tools"); got != "tools" {
			t.Errorf("pluralWord(%d) = %q, want tools", n, got)
		}
	}
}

// TestWriteFileAtomic exercises the success (new + overwrite) and the
// mkdir-failure branch of the atomic writer.
func TestWriteFileAtomic(t *testing.T) {
	t.Run("new-and-overwrite", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "sub", "Dockerfile")

		if err := writeFileAtomic(path, []byte("first")); err != nil {
			t.Fatalf("writeFileAtomic new: %v", err)
		}
		if got, _ := os.ReadFile(path); string(got) != "first" {
			t.Errorf("content = %q, want first", got)
		}
		if err := writeFileAtomic(path, []byte("second")); err != nil {
			t.Fatalf("writeFileAtomic overwrite: %v", err)
		}
		if got, _ := os.ReadFile(path); string(got) != "second" {
			t.Errorf("content = %q, want second", got)
		}
	})

	t.Run("mkdir-failure", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: read-only dir is still writable")
		}
		dir := t.TempDir()
		sealed := filepath.Join(dir, "sealed")
		if err := os.Mkdir(sealed, 0o555); err != nil {
			t.Fatalf("mkdir sealed: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

		err := writeFileAtomic(filepath.Join(sealed, "nested", "Dockerfile"), []byte("x"))
		if err == nil {
			t.Fatal("expected error writing under read-only parent")
		}
		if !strings.Contains(err.Error(), "create output directory") {
			t.Errorf("error = %v, want create-output-directory wrapper", err)
		}
	})
}

// TestPrintDockerfileSummary covers the pinned, unpinned, and skipped-apps
// branches, capturing output via the command's writer.
func TestPrintDockerfileSummary(t *testing.T) {
	origOutput := dockerfileOutput
	t.Cleanup(func() { dockerfileOutput = origOutput })
	dockerfileOutput = "Dockerfile"
	plan := dockerfile.Plan{
		RuntimeStages:    []dockerfile.RuntimeStage{{Name: "node"}},
		RuntimeAppStages: []dockerfile.RuntimeAppStage{{App: "slidev"}},
		BinaryStages:     []dockerfile.BinaryStage{{App: "ruff"}},
	}

	t.Run("pinned", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&buf)
		printDockerfileSummary(cmd, plan, "sha256:abc", "")
		out := buf.String()
		if !strings.Contains(out, "3 build stages") || !strings.Contains(out, "pinned by digest") {
			t.Errorf("pinned summary unexpected: %q", out)
		}
	})

	t.Run("unpinned-with-skipped", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&buf)
		p := plan
		p.Skipped = []string{"prettier", "eslint"}
		printDockerfileSummary(cmd, p, "", "no digest available")
		out := buf.String()
		if !strings.Contains(out, "UNPINNED (no digest available)") {
			t.Errorf("unpinned summary missing reason: %q", out)
		}
		if !strings.Contains(out, "Skipped (no install footprint): prettier, eslint") {
			t.Errorf("skipped line missing: %q", out)
		}
	})
}

// TestClearAppLockFile verifies the lockfile-clearing copy clears Node/UV/Go
// LockFile fields without mutating the original, and handles a missing app.
func TestClearAppLockFile(t *testing.T) {
	apps := binmanager.MapOfApps{
		"n": {Node: &binmanager.AppConfigNode{LockFile: "keep-node"}},
		"u": {Uv: &binmanager.AppConfigUV{LockFile: "keep-uv"}},
		"g": {Go: &binmanager.AppConfigGo{LockFile: "keep-go"}},
	}

	for _, name := range []string{"n", "u", "g"} {
		fresh := clearAppLockFile(apps, name)
		switch name {
		case "n":
			if fresh["n"].Node.LockFile != "" {
				t.Errorf("node LockFile not cleared")
			}
			if apps["n"].Node.LockFile != "keep-node" {
				t.Errorf("original node LockFile mutated")
			}
		case "u":
			if fresh["u"].Uv.LockFile != "" {
				t.Errorf("uv LockFile not cleared")
			}
			if apps["u"].Uv.LockFile != "keep-uv" {
				t.Errorf("original uv LockFile mutated")
			}
		case "g":
			if fresh["g"].Go.LockFile != "" {
				t.Errorf("go LockFile not cleared")
			}
			if apps["g"].Go.LockFile != "keep-go" {
				t.Errorf("original go LockFile mutated")
			}
		}
	}

	// Missing app: returns a copy unchanged, no panic.
	if got := clearAppLockFile(apps, "absent"); len(got) != len(apps) {
		t.Errorf("missing-app copy size = %d, want %d", len(got), len(apps))
	}
}

// TestReadLockFile covers the node, uv, go (mod+sum), unsupported-type, and
// missing-file branches.
func TestReadLockFile(t *testing.T) {
	t.Run("node", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "pnpm-lock.yaml"), "lockfileVersion: 9\n")
		got, err := readLockFile(dir, binmanager.App{Node: &binmanager.AppConfigNode{}})
		if err != nil || !strings.Contains(got, "lockfileVersion") {
			t.Fatalf("node readLockFile: %q err=%v", got, err)
		}
	})

	t.Run("uv", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "uv.lock"), "version = 1\n")
		got, err := readLockFile(dir, binmanager.App{Uv: &binmanager.AppConfigUV{}})
		if err != nil || !strings.Contains(got, "version = 1") {
			t.Fatalf("uv readLockFile: %q err=%v", got, err)
		}
	})

	t.Run("go", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/x\n\ngo 1.26\n")
		mustWrite(t, filepath.Join(dir, "go.sum"), "")
		got, err := readLockFile(dir, binmanager.App{Go: &binmanager.AppConfigGo{}})
		if err != nil || got == "" {
			t.Fatalf("go readLockFile: %q err=%v", got, err)
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		_, err := readLockFile(t.TempDir(), binmanager.App{Binary: &binmanager.AppConfigBinary{}})
		if err == nil || !strings.Contains(err.Error(), "unsupported app type") {
			t.Fatalf("unsupported err = %v, want unsupported-app-type", err)
		}
	})

	t.Run("missing-node-lock", func(t *testing.T) {
		_, err := readLockFile(t.TempDir(), binmanager.App{Node: &binmanager.AppConfigNode{}})
		if err == nil || !strings.Contains(err.Error(), "failed to read lock file") {
			t.Fatalf("missing-lock err = %v", err)
		}
	})

	t.Run("missing-go-mod", func(t *testing.T) {
		_, err := readLockFile(t.TempDir(), binmanager.App{Go: &binmanager.AppConfigGo{}})
		if err == nil || !strings.Contains(err.Error(), "failed to read go.mod") {
			t.Fatalf("missing-go.mod err = %v", err)
		}
	})

	t.Run("missing-go-sum", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/x\n")
		_, err := readLockFile(dir, binmanager.App{Go: &binmanager.AppConfigGo{}})
		if err == nil || !strings.Contains(err.Error(), "failed to read go.sum") {
			t.Fatalf("missing-go.sum err = %v", err)
		}
	})
}

// TestDevtoolsAppsJSONTempFileErrors covers the CreateTemp-failure branches of
// the node/uv apps-JSON writers when their target lives in a directory that does
// not exist (os.CreateTemp on a missing dir fails), reaching the error wrappers
// the happy-path tests do not.
func TestDevtoolsAppsJSONTempFileErrors(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "absent")
	path := filepath.Join(missingDir, "apps.json")

	t.Run("ensureNode", func(t *testing.T) {
		if err := ensureNodeAppsJSONExists(path); err == nil ||
			!strings.Contains(err.Error(), "failed to create temp file") {
			t.Fatalf("ensureNodeAppsJSONExists err = %v", err)
		}
	})
	t.Run("ensureUV", func(t *testing.T) {
		if err := ensureUVAppsJSONExists(path); err == nil ||
			!strings.Contains(err.Error(), "failed to create temp file") {
			t.Fatalf("ensureUVAppsJSONExists err = %v", err)
		}
	})
	t.Run("writeNode", func(t *testing.T) {
		if err := writeNodeAppsJSON(path, nodeAppsJSON{"x": {PackageName: "x", Version: "1"}}); err == nil ||
			!strings.Contains(err.Error(), "creating temp file") {
			t.Fatalf("writeNodeAppsJSON err = %v", err)
		}
	})
	t.Run("writeUV", func(t *testing.T) {
		if err := writeUVAppsJSON(path, uvAppsJSON{"x": {PackageName: "x", Version: "1"}}); err == nil ||
			!strings.Contains(err.Error(), "creating temp file") {
			t.Fatalf("writeUVAppsJSON err = %v", err)
		}
	})
}

// TestEnsureAppsJSONExistsNoOp confirms the "file already exists" branch is a
// no-op that leaves existing content untouched.
func TestEnsureAppsJSONExistsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "apps.json")
	mustWrite(t, path, `{"keep": {"packageName":"keep","version":"1"}}`)

	if err := ensureNodeAppsJSONExists(path); err != nil {
		t.Fatalf("ensureNodeAppsJSONExists no-op: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "keep") {
		t.Errorf("existing content overwritten: %s", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
