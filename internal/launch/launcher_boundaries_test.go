package launch

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathDoesNotPublishLauncher(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	planned, err := Path(executable, []string{"--probe"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(planned)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only resolution created launcher state: %v", err)
	}
	installed, err := Ensure(executable, []string{"--probe"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if installed != planned {
		t.Fatalf("installed launcher %q differs from resolved path %q", installed, planned)
	}
}

func TestEnsureRejectsRelativeExecutable(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	if _, err := Ensure("relative-program", nil, nil); err == nil {
		t.Fatal("accepted a target whose executable depends on the caller's working directory")
	}
}

func TestEnsurePreservesConflictingCommandRecord(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher, err := Ensure(executable, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(filepath.Dir(launcher), configName)
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var record command
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record.Executable = filepath.Join(t.TempDir(), "substituted-program")
	tampered, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, tampered, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(executable, nil, nil); err == nil {
		t.Fatal("reused a content-addressed command record with a substituted executable")
	}
	preserved, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(preserved, tampered) {
		t.Fatal("silently replaced the conflicting command record")
	}
}

func TestDispatchRejectsInvalidLauncherNamespace(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)
	root := filepath.Join(cache, "store", ".app-launchers")
	canonical := filepath.Join(root, strings.Repeat("a", 32), launcherName)
	cases := map[string]string{
		"short identity":    filepath.Join(root, strings.Repeat("a", 31), launcherName),
		"invalid identity":  filepath.Join(root, strings.Repeat("a", 31)+"g", launcherName),
		"missing namespace": canonical,
		"noncanonical path": filepath.Dir(canonical) + string(os.PathSeparator) + "." + string(os.PathSeparator) + launcherName,
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			code, handled := DispatchWith([]string{path}, nil, path, func(string, []string, []string) int {
				t.Fatal("executed a command from an invalid launcher namespace")
				return 0
			})
			if !handled || code != 126 {
				t.Fatalf("invalid namespace: handled=%v exit=%d", handled, code)
			}
		})
	}
}

func TestEnsurePreservesNonFileCommandRecord(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher, err := Path(executable, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(filepath.Dir(launcher), configName)
	if err := os.MkdirAll(record, 0o755); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(record, "owned")
	if err := os.WriteFile(owned, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(executable, nil, nil); err == nil {
		t.Fatal("accepted a directory as a command record")
	}
	data, err := os.ReadFile(owned)
	if err != nil || string(data) != "preserved" {
		t.Fatalf("existing metadata was replaced: %q, %v", data, err)
	}
}

func TestEnsurePreservesBlockedLauncherNamespace(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)
	store := filepath.Join(cache, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	namespace := filepath.Join(store, ".app-launchers")
	if err := os.WriteFile(namespace, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(executable, nil, nil); err == nil {
		t.Fatal("installed a launcher through a non-directory namespace")
	}
	data, err := os.ReadFile(namespace)
	if err != nil || string(data) != "preserved" {
		t.Fatalf("blocked namespace was overwritten: %q, %v", data, err)
	}
}
