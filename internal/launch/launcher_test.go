package launch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDistinctBuildsGetDistinctLauncherIdentity(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	first := filepath.Join(root, "first"+suffix)
	second := filepath.Join(root, "second"+suffix)
	binaries := []string{first, second}
	markers := []string{"first-build", "second-build"}
	launchers := make([]string, len(binaries))
	for i, binary := range binaries {
		buildLauncherFixture(t, binary, markers[i])
		create := exec.Command(binary, "make", first)
		create.Env = append(os.Environ(), "DATAMITSU_CACHE_DIR="+cache)
		output, err := create.CombinedOutput()
		if err != nil {
			t.Fatalf("create %s launcher: %v\n%s", markers[i], err, output)
		}
		parts := strings.SplitN(strings.TrimSpace(string(output)), "\n", 2)
		if len(parts) != 2 || parts[0] != "dev" {
			t.Fatalf("builder version and launcher path = %q", output)
		}
		launchers[i] = parts[1]
	}
	if launchers[0] == launchers[1] {
		t.Fatalf("two dev builds reused the same launcher: %q", launchers[0])
	}
	for i, launcher := range launchers {
		run := exec.Command(launcher, "payload")
		run.Env = append(os.Environ(), "DATAMITSU_CACHE_DIR="+cache)
		output, err := run.CombinedOutput()
		want := markers[i] + "\nargument with spaces|bound|payload\n"
		if err != nil || string(output) != want {
			t.Fatalf("launcher %d executed wrong build or command: output=%q err=%v, want %q", i, output, err, want)
		}
	}
	createAgain := exec.Command(second, "make", first)
	createAgain.Env = append(os.Environ(), "DATAMITSU_CACHE_DIR="+cache)
	output, err := createAgain.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "dev\n"+launchers[1] {
		t.Fatalf("unchanged executable did not reuse its launcher: output=%q err=%v", output, err)
	}
}

func buildLauncherFixture(t *testing.T, binary, marker string) {
	t.Helper()
	flags := "-X github.com/datamitsu/datamitsu/internal/ldflags.Version=dev -X main.marker=" + marker
	build := exec.Command("go", "build", "-buildvcs=false", "-ldflags", flags, "-o", binary, "./testdata/identity")
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", marker, err, output)
	}
}

func TestDispatchWithRejectsInvalidRecordsBeforeCallback(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)
	store := filepath.Join(cache, "store")
	if err := os.MkdirAll(filepath.Join(store, ".app-launchers"), 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	valid := command{Executable: executable, Identity: strings.Repeat("a", 32)}
	validData, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	wrong := valid
	wrong.Executable = filepath.Join(t.TempDir(), "unexpected")
	wrongData, err := json.Marshal(wrong)
	if err != nil {
		t.Fatal(err)
	}
	validKey := commandKey(validData)
	cases := []struct {
		name, key string
		data      []byte
	}{
		{name: "missing record", key: validKey},
		{name: "malformed record", key: strings.Repeat("b", 32), data: []byte("{")},
		{name: "tampered record", key: validKey, data: wrongData},
	}
	for _, tc := range cases {
		if tc.data != nil {
			dir := filepath.Join(store, ".app-launchers", tc.key)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, configName), tc.data, 0o640); err != nil {
				t.Fatal(err)
			}
		}
		launcher := filepath.Join(store, ".app-launchers", tc.key, launcherName)
		code, handled := DispatchWith([]string{launcher}, nil, launcher, func(string, []string, []string) int { t.Fatalf("%s record executed", tc.name); return 0 })
		if !handled || code != 126 {
			t.Fatalf("%s: handled=%v code=%d", tc.name, handled, code)
		}
	}
	outside := filepath.Join(t.TempDir(), launcherName)
	code, handled := DispatchWith([]string{outside}, nil, outside, func(string, []string, []string) int { t.Fatal("outside record executed"); return 0 })
	if !handled || code != 126 {
		t.Fatalf("outside store: handled=%v code=%d", handled, code)
	}
}

func TestDispatchWithLeavesOrdinaryCommandsAlone(t *testing.T) {
	called := false
	code, handled := DispatchWith([]string{"tool"}, nil, "/usr/bin/tool", func(string, []string, []string) int { called = true; return 0 })
	if handled || called || code != 0 {
		t.Fatalf("handled=%v called=%v code=%d", handled, called, code)
	}
}
