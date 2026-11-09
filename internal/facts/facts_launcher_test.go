package facts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/managedconfig"
)

func TestResolveBinaryCommandForInitIgnoresStaleMarker(t *testing.T) {
	t.Setenv("DATAMITSU_BINARY_COMMAND", "")
	root := t.TempDir()
	if err := managedconfig.WriteLauncherMarker(root, managedconfig.LauncherMarker{
		Command:    "pnpm datamitsu",
		BinaryPath: "/marker/bin/datamitsu",
	}, false); err != nil {
		t.Fatal(err)
	}

	// init records the current invocation, never the preserved marker: a
	// repository switched from the wrapper to a global binary must re-record
	// on the next init instead of looping the old wrapper command.
	command, _ := ResolveBinaryCommandForInit("")
	if command == "pnpm datamitsu" {
		t.Errorf("init command = %q, want the executable path (marker must not feed back)", command)
	}
	if command == "" {
		t.Error("init command = empty, want the executable path")
	}

	// Ordinary facts collection keeps the marker fallback.
	factsCommand, _ := ResolveBinaryCommand("", root)
	if factsCommand != "pnpm datamitsu" {
		t.Errorf("facts command = %q, want the marker command %q", factsCommand, "pnpm datamitsu")
	}
}

func TestResolveBinaryCommandPrecedence(t *testing.T) {
	root := t.TempDir()
	if err := managedconfig.WriteLauncherMarker(root, managedconfig.LauncherMarker{
		Command:    "pnpm datamitsu",
		BinaryPath: "/marker/bin/datamitsu",
	}, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATAMITSU_BINARY_COMMAND", "")

	command, binaryPath := ResolveBinaryCommand("", root)
	if command != "pnpm datamitsu" {
		t.Errorf("marker fallback command = %q, want %q", command, "pnpm datamitsu")
	}
	if binaryPath == "" || !filepath.IsAbs(binaryPath) {
		t.Errorf("binaryPath = %q, want an absolute path", binaryPath)
	}

	// DATAMITSU_BINARY_COMMAND beats the marker.
	t.Setenv("DATAMITSU_BINARY_COMMAND", "env datamitsu")
	command, _ = ResolveBinaryCommand("", root)
	if command != "env datamitsu" {
		t.Errorf("env command = %q, want %q", command, "env datamitsu")
	}

	// The --binary-command flag beats both.
	command, _ = ResolveBinaryCommand("flag datamitsu", root)
	if command != "flag datamitsu" {
		t.Errorf("flag command = %q, want %q", command, "flag datamitsu")
	}
}

func TestResolveBinaryCommandWithoutMarker(t *testing.T) {
	t.Setenv("DATAMITSU_BINARY_COMMAND", "")
	root := t.TempDir()

	command, binaryPath := ResolveBinaryCommand("", root)
	if command == "" || command != binaryPath {
		t.Errorf("command = %q, binaryPath = %q; want the executable path for both", command, binaryPath)
	}
}

func TestResolveBinaryCommandWithCorruptMarker(t *testing.T) {
	t.Setenv("DATAMITSU_BINARY_COMMAND", "")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".datamitsu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".datamitsu", "launcher.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	command, _ := ResolveBinaryCommand("", root)
	if command == "" {
		t.Error("corrupt marker must fall back to the executable path, got empty command")
	}
}

func TestResolveBinaryCommandOutsideGitRepo(t *testing.T) {
	t.Setenv("DATAMITSU_BINARY_COMMAND", "")

	command, _ := ResolveBinaryCommand("", "")
	if command == "" {
		t.Error("empty git root must fall back to the executable path, got empty command")
	}
}
