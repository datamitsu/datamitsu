package prepare

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeStep builds a small Go program as the step executable and returns its
// path plus a marker of what it writes. Go programs run on every platform,
// so these tests need no POSIX shell fixtures.
func fakeStep(t *testing.T, dir, source string) string {
	t.Helper()
	src := filepath.Join(dir, "step.go")
	if err := os.WriteFile(src, []byte("package main\n"+source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "step.bin")
	out, err := execBuild(src, bin)
	if err != nil {
		t.Fatalf("build fake step: %v\n%s", err, out)
	}
	return bin
}

func execBuild(src, bin string) (string, error) {
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Stderr = os.Stderr
	return "", cmd.Run()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunPlacesMarkerAndOutput(t *testing.T) {
	store := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", store)
	step := fakeStep(t, t.TempDir(), `
import "os"
import "strings"

func main() {
	os.MkdirAll(os.Getenv("STEP_OUT")+"/nested", 0o755)
	os.WriteFile(os.Getenv("STEP_OUT")+"/nested/data.txt", []byte("payload"), 0o644)
	// The argv-bound layout token: os.Args[1] is -argdir=${APP_PREPARED}/argmade,
	// bound by the engine to the staging out/ directory.
	argDir := strings.TrimPrefix(os.Args[1], "-argdir=")
	os.MkdirAll(argDir, 0o755)
	os.WriteFile(argDir+"/from-arg.txt", []byte("argv"), 0o644)
}
`)
	SweepStale(storeRoot(t, store))

	key := "aaaa1111"
	err := Run(context.Background(), Request{
		App:          "app",
		Key:          key,
		Network:      false,
		PreparedRoot: filepath.Join(store, ".prepared"),
		Protocol:     "test-protocol",
		Store:        store,
		Command:      []string{step, "-argdir=${APP_PREPARED}/argmade"},
		Env:          []string{"STEP_OUT=${APP_PREPARED}"},
		Timeout:      30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	state := StateDir(filepath.Join(store, ".prepared"), "app", key)
	if !Ready(state) {
		t.Fatal("state not ready after Run")
	}
	data, err := os.ReadFile(filepath.Join(state, "out", "nested", "data.txt"))
	if err != nil || string(data) != "payload" {
		t.Fatalf("output = %q, %v; want payload", data, err)
	}
	// The argv-bound ${APP_PREPARED} landed in the same placed tree.
	argData, err := os.ReadFile(filepath.Join(state, "out", "argmade", "from-arg.txt"))
	if err != nil || string(argData) != "argv" {
		t.Fatalf("argv-bound output = %q, %v; want argv", argData, err)
	}
	// No staging remains: the empty preparing/ root is kept (another
	// builder may be allocating inside it), but no staging directory.
	entries, _ := os.ReadDir(filepath.Join(store, "tmp", "preparing"))
	if len(entries) != 0 {
		t.Fatalf("staging left behind: %v", entries)
	}
}

func TestRunTwiceRunsStepOnce(t *testing.T) {
	store := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", store)
	counter := filepath.Join(t.TempDir(), "count")
	step := fakeStep(t, t.TempDir(), `
import "os"
func main() {
	f, _ := os.OpenFile(os.Getenv("COUNTER"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	f.WriteString("x")
	f.Close()
	os.MkdirAll(os.Getenv("STEP_OUT"), 0o755)
}
`)
	req := Request{
		App:          "app",
		Key:          "bbbb2222",
		Network:      false,
		PreparedRoot: filepath.Join(store, ".prepared"),
		Protocol:     "test-protocol",
		Store:        store,
		Command:      []string{step},
		Env: []string{
			"COUNTER=" + counter,
			"STEP_OUT=${APP_PREPARED}",
		},
		Timeout: 30 * time.Second,
	}
	if err := Run(context.Background(), req); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := Run(context.Background(), req); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	data, _ := os.ReadFile(counter)
	if string(data) != "x" {
		t.Fatalf("step ran %d times, want 1", len(data))
	}
}

func TestRunFailurePlacesNothing(t *testing.T) {
	store := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", store)
	step := fakeStep(t, t.TempDir(), `
import "os"
func main() {
	os.MkdirAll(os.Getenv("STEP_OUT"), 0o755)
	os.Exit(4)
}
`)
	err := Run(context.Background(), Request{
		App:          "app",
		Key:          "cccc3333",
		Network:      false,
		PreparedRoot: filepath.Join(store, ".prepared"),
		Protocol:     "test-protocol",
		Store:        store,
		Command:      []string{step},
		Env:          []string{"STEP_OUT=${APP_PREPARED}"},
		Timeout:      30 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "prepare of \"app\"") {
		t.Fatalf("err = %v, want step failure", err)
	}
	if Ready(StateDir(filepath.Join(store, ".prepared"), "app", "cccc3333")) {
		t.Fatal("failed step left a ready state")
	}
	entries, _ := os.ReadDir(filepath.Join(store, "tmp", "preparing"))
	if len(entries) != 0 {
		t.Fatalf("failed step left staging: %v", entries)
	}
}

func TestRunTimeoutKillsStep(t *testing.T) {
	store := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", store)
	// The step sleeps far longer than the timeout.
	step := fakeStep(t, t.TempDir(), `
import ("os"; "time")
func main() {
	os.MkdirAll(os.Getenv("STEP_OUT"), 0o755)
	time.Sleep(30 * time.Second)
}
`)
	start := time.Now()
	err := Run(context.Background(), Request{
		App:          "app",
		Key:          "dddd4444",
		Network:      false,
		PreparedRoot: filepath.Join(store, ".prepared"),
		Protocol:     "test-protocol",
		Store:        store,
		Command:      []string{step},
		Env:          []string{"STEP_OUT=${APP_PREPARED}"},
		Timeout:      300 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout not enforced: %s", elapsed)
	}
	if Ready(StateDir(filepath.Join(store, ".prepared"), "app", "dddd4444")) {
		t.Fatal("timed-out step left a ready state")
	}
}

func TestRunOfflineRefusal(t *testing.T) {
	store := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", store)
	t.Setenv("DATAMITSU_OFFLINE", "1")

	err := Run(context.Background(), Request{
		App:          "app",
		Key:          "eeee5555",
		Network:      true,
		PreparedRoot: filepath.Join(store, ".prepared"),
		Protocol:     "test-protocol",
		Store:        store,
		Command:      []string{"irrelevant"},
	})
	if err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("err = %v, want offline refusal", err)
	}
}

func TestValidateOutput(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "ok.txt"), "fine")
	if err := validateOutput(out); err != nil {
		t.Fatalf("clean tree rejected: %v", err)
	}

	// Empty out is legitimate.
	if err := validateOutput(t.TempDir()); err != nil {
		t.Fatalf("empty tree rejected: %v", err)
	}

	// Absolute symlink.
	abs := t.TempDir()
	link := filepath.Join(out, "abs-link")
	if err := os.Symlink(abs, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := validateOutput(out); err == nil || !strings.Contains(err.Error(), "absolute symlink") {
		t.Fatalf("err = %v, want absolute symlink rejection", err)
	}
	_ = os.Remove(link)

	// Escaping relative symlink.
	if err := os.Symlink("../../escape", link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := validateOutput(out); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("err = %v, want escape rejection", err)
	}
}

func TestPlaceStateMarkerlessTargetMovedAside(t *testing.T) {
	store := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", store)

	// A markerless directory at the destination: an interrupted earlier
	// attempt, or a manually deleted marker (the documented repair).
	state := StateDir(filepath.Join(store, ".prepared"), "app", "ffff6666")
	writeFile(t, filepath.Join(state, "junk"), "stale")
	if Ready(state) {
		t.Fatal("markerless dir must not be ready")
	}

	staged := t.TempDir()
	writeFile(t, filepath.Join(staged, MarkerName), `{"format":1}`)
	writeFile(t, filepath.Join(staged, "out", "real"), "value")

	if err := placeState(staged, state, store); err != nil {
		t.Fatalf("placeState: %v", err)
	}
	if !Ready(state) {
		t.Fatal("placement did not produce readiness")
	}
	data, err := os.ReadFile(filepath.Join(state, "out", "real"))
	if err != nil || string(data) != "value" {
		t.Fatalf("placed content = %q, %v", data, err)
	}
}

func TestSweepStaleKeepsLiveLease(t *testing.T) {
	store := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", store)
	tmp := filepath.Join(store, "tmp")

	// A live layout: lease holds this process's PID.
	live := filepath.Join(tmp, "prepare-live.tmp")
	writeFile(t, filepath.Join(live, "lease"), strconv.Itoa(os.Getpid()))

	// An abandoned layout: lease of a PID that cannot exist.
	dead := filepath.Join(tmp, "prepare-dead.tmp")
	writeFile(t, filepath.Join(dead, "lease"), "-1")

	// A stale aside directory, older than the cutoff.
	aside := filepath.Join(tmp, "stale-prepared-old.tmp")
	writeFile(t, filepath.Join(aside, "state", "x"), "y")
	past := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(aside, past, past)

	SweepStale(store)

	if _, err := os.Stat(live); err != nil {
		t.Fatal("sweep removed a live layout")
	}
	if _, err := os.Stat(dead); err == nil {
		t.Fatal("sweep kept an abandoned layout")
	}
	if _, err := os.Stat(aside); err == nil {
		t.Fatal("sweep kept a stale aside directory")
	}
}

func storeRoot(t *testing.T, store string) string {
	t.Helper()
	return store
}
