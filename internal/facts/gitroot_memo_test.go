package facts

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// stubGitRootLookup replaces the uncached resolver for the duration of the test
// and returns a counter of how many times it actually ran. Counting here rather
// than counting forked processes keeps the assertion about the memo itself.
func stubGitRootLookup(t *testing.T, fn func(cwd string) (string, error)) *atomic.Int64 {
	t.Helper()

	var calls atomic.Int64
	prev := gitRootLookup
	gitRootLookup = func(_ context.Context, cwd string) (string, error) {
		calls.Add(1)
		return fn(cwd)
	}
	resetGitRootCache()
	t.Cleanup(func() {
		gitRootLookup = prev
		resetGitRootCache()
	})

	return &calls
}

func TestGetGitRootMemoizesSameWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	calls := stubGitRootLookup(t, func(cwd string) (string, error) { return cwd, nil })

	ctx := context.Background()

	first, err := GetGitRoot(ctx)
	if err != nil {
		t.Fatalf("first GetGitRoot() error = %v", err)
	}
	second, err := GetGitRoot(ctx)
	if err != nil {
		t.Fatalf("second GetGitRoot() error = %v", err)
	}

	if first != second {
		t.Errorf("GetGitRoot() = %q then %q, want identical results", first, second)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("resolver ran %d times, want 1", got)
	}
}

func TestGetGitRootResolvesEachWorkingDirectoryIndependently(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()

	calls := stubGitRootLookup(t, func(cwd string) (string, error) {
		return filepath.Join(cwd, "root"), nil
	})

	ctx := context.Background()

	t.Chdir(first)
	gotFirst, err := GetGitRoot(ctx)
	if err != nil {
		t.Fatalf("GetGitRoot() in first dir error = %v", err)
	}

	t.Chdir(second)
	gotSecond, err := GetGitRoot(ctx)
	if err != nil {
		t.Fatalf("GetGitRoot() in second dir error = %v", err)
	}

	if gotFirst == gotSecond {
		t.Errorf("both directories resolved to %q, want independent results", gotFirst)
	}
	if want := filepath.Join(first, "root"); gotFirst != want {
		t.Errorf("first dir resolved to %q, want %q", gotFirst, want)
	}
	if want := filepath.Join(second, "root"); gotSecond != want {
		t.Errorf("second dir resolved to %q, want %q", gotSecond, want)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("resolver ran %d times, want 2", got)
	}
}

func TestGetGitRootMemoizesFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	sentinel := errors.New("no repository here")
	calls := stubGitRootLookup(t, func(string) (string, error) { return "", sentinel })

	ctx := context.Background()

	_, firstErr := GetGitRoot(ctx)
	_, secondErr := GetGitRoot(ctx)

	if !errors.Is(firstErr, sentinel) {
		t.Fatalf("first GetGitRoot() error = %v, want %v", firstErr, sentinel)
	}
	if !errors.Is(secondErr, sentinel) {
		t.Fatalf("second GetGitRoot() error = %v, want %v", secondErr, sentinel)
	}
	if firstErr != secondErr { //nolint:errorlint // identity is the assertion
		t.Errorf("errors differ across calls: %v vs %v", firstErr, secondErr)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("resolver ran %d times, want 1", got)
	}
}

// A failure caused by a dead context says nothing about the repository layout,
// so it must not stick: the next call has to re-resolve.
func TestGetGitRootDoesNotMemoizeContextFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	calls := stubGitRootLookup(t, func(string) (string, error) { return "", context.Canceled })

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := GetGitRoot(cancelled); err == nil {
		t.Fatal("GetGitRoot() with a cancelled context should fail")
	}
	if _, err := GetGitRoot(cancelled); err == nil {
		t.Fatal("GetGitRoot() with a cancelled context should fail")
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("resolver ran %d times, want 2 (a context failure must not be cached)", got)
	}
}

func TestGetGitRootMemoIsConcurrencySafe(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	calls := stubGitRootLookup(t, func(cwd string) (string, error) { return cwd, nil })

	ctx := context.Background()

	const goroutines = 16
	results := make([]string, goroutines)
	errs := make([]error, goroutines)

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for i := range goroutines {
		done.Go(func() {
			start.Wait()
			results[i], errs[i] = GetGitRoot(ctx)
		})
	}
	start.Done()
	done.Wait()

	for i := range goroutines {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: GetGitRoot() error = %v", i, errs[i])
		}
		if results[i] != results[0] {
			t.Errorf("goroutine %d got %q, want %q", i, results[i], results[0])
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("resolver ran %d times, want 1 (concurrent callers must collapse)", got)
	}
}

// The memo must not change the answer for a real repository.
func TestGetGitRootMemoMatchesRealGit(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	resetGitRootCache()
	t.Cleanup(resetGitRootCache)

	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to initialize git repo: %v", err)
	}
	t.Chdir(dir)

	ctx := context.Background()

	first, err := GetGitRoot(ctx)
	if err != nil {
		t.Fatalf("GetGitRoot() error = %v", err)
	}
	second, err := GetGitRoot(ctx)
	if err != nil {
		t.Fatalf("memoized GetGitRoot() error = %v", err)
	}
	if first != second {
		t.Errorf("memoized root %q differs from resolved root %q", second, first)
	}

	direct, err := resolveGitRoot(ctx, dir)
	if err != nil {
		t.Fatalf("resolveGitRoot() error = %v", err)
	}
	if direct != first {
		t.Errorf("GetGitRoot() = %q, uncached resolver = %q", first, direct)
	}
}
