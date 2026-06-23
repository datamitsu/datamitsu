package clitest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizerStripsANSI(t *testing.T) {
	in := "\x1b[31m" + "red" + "\x1b[0m" + " and " + "\x1b[1m" + "bold" + "\x1b[0m"
	got := NewNormalizer().Apply(in)
	if got != "red and bold" {
		t.Fatalf("Apply = %q, want %q", got, "red and bold")
	}
}

func TestNormalizerMasksPathsLongestFirst(t *testing.T) {
	home := "/home/u"
	cache := "/home/u/.cache/datamitsu"
	store := "/home/u/.cache/datamitsu/store"
	// Register parent-first to prove Apply still masks the most specific path.
	norm := NewNormalizer().
		MaskPath(home, "<HOME>").
		MaskPath(cache, "<CACHE>").
		MaskPath(store, "<STORE>")

	in := store + "/bin and " + cache + "/x and " + home + "/file"
	got := norm.Apply(in)
	want := "<STORE>/bin and <CACHE>/x and <HOME>/file"
	if got != want {
		t.Fatalf("Apply = %q, want %q", got, want)
	}
}

func TestNormalizerMasksVersionTimestampDuration(t *testing.T) {
	// ldflags.Version is "dev" for the local test build.
	in := "datamitsu version dev built 2026-06-23T10:00:00Z in 1m30s (500ms cached)"
	got := NewNormalizer().Apply(in)
	want := "datamitsu version <VERSION> built <TS> in <DUR> (<DUR> cached)"
	if got != want {
		t.Fatalf("Apply = %q, want %q", got, want)
	}
}

func TestNormalizerIsIdempotent(t *testing.T) {
	norm := NewNormalizer().MaskPath("/tmp/proj", "<TMP>")
	in := "\x1b[32m" + "/tmp/proj/a" + "\x1b[0m" + " took 2.5s at 2026-06-23T00:00:00Z (dev)"
	once := norm.Apply(in)
	twice := norm.Apply(once)
	if once != twice {
		t.Fatalf("normalizer not idempotent:\n once = %q\ntwice = %q", once, twice)
	}
}

func TestNormalizerSortLines(t *testing.T) {
	in := "charlie\nalpha\nbravo\n"
	got := NewNormalizer().SortLines().Apply(in)
	want := "alpha\nbravo\ncharlie\n"
	if got != want {
		t.Fatalf("SortLines Apply = %q, want %q", got, want)
	}
}

func TestCompareGoldenUpdateThenMatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "golden")
	const name = "sample"
	content := "expected output\nline two\n"

	// -update writes the file and passes.
	compareGolden(t, dir, name, content, true)
	if _, err := os.Stat(filepath.Join(dir, name+".txt")); err != nil {
		t.Fatalf("golden not written: %v", err)
	}

	// A subsequent compare against the same content passes (no fatal).
	compareGolden(t, dir, name, content, false)
}

func TestCompareGoldenMismatchFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "golden")
	const name = "sample"
	compareGolden(t, dir, name, "original\n", true)

	failed, msg := captureFatal(func(tb testing.TB) {
		tb.Helper()
		compareGolden(tb, dir, name, "changed\n", false)
	})
	if !failed {
		t.Fatal("compareGolden did not fail on mismatch")
	}
	// The diff must be present and readable (show both sides).
	for _, want := range []string{"mismatch", "- original", "+ changed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diff message %q missing %q", msg, want)
		}
	}
}

func TestCompareGoldenMissingFileFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "golden")
	failed, msg := captureFatal(func(tb testing.TB) {
		tb.Helper()
		compareGolden(tb, dir, "does-not-exist", "anything\n", false)
	})
	if !failed {
		t.Fatal("compareGolden did not fail for a missing golden")
	}
	if !strings.Contains(msg, "-update") {
		t.Errorf("missing-golden message should hint at -update, got %q", msg)
	}
}

// recorderTB is a minimal testing.TB that records the first Fatalf and unwinds
// via runtime.Goexit (matching *testing.T semantics), so tests can assert that
// compareGolden fails without failing the enclosing test.
type recorderTB struct {
	testing.TB

	failed bool
	msg    string
}

func (r *recorderTB) Helper() {}

func (r *recorderTB) Fatalf(format string, args ...any) {
	r.failed = true
	r.msg = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// captureFatal runs fn with a recorderTB on its own goroutine and reports
// whether fn called Fatalf, plus the formatted message.
func captureFatal(fn func(tb testing.TB)) (bool, string) {
	r := &recorderTB{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(r)
	}()
	<-done
	return r.failed, r.msg
}
