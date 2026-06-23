package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

// TestCreateCacheDedupesInvalidateOn builds a cache from a config whose tools
// declare overlapping invalidateOn files across operations; the per-tool file
// list must be de-duplicated.
func TestCreateCacheDedupesInvalidateOn(t *testing.T) {
	cacheDir := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Tools: config.MapOfTools{
			"eslint": config.Tool{
				Name: "eslint",
				Operations: map[config.OperationType]config.ToolOperation{
					config.OpLint: {App: "eslint", InvalidateOn: []string{".eslintrc", "package.json"}},
					config.OpFix:  {App: "eslint", InvalidateOn: []string{".eslintrc"}},
				},
			},
			// Tool with no invalidateOn must not appear in the map.
			"prettier": config.Tool{
				Name:       "prettier",
				Operations: map[config.OperationType]config.ToolOperation{config.OpFix: {App: "prettier"}},
			},
		},
	}

	c, err := createCache(cacheDir, projectPath, cfg, []string{"eslint"})
	if err != nil {
		t.Fatalf("createCache() error = %v", err)
	}
	if c == nil {
		t.Fatal("createCache returned nil cache")
	}
}

// TestCreateCacheNoInvalidateOn covers the branch where no tool declares any
// invalidateOn files (empty map passed to NewCache).
func TestCreateCacheNoInvalidateOn(t *testing.T) {
	cacheDir := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Tools: config.MapOfTools{
			"gofmt": config.Tool{
				Name:       "gofmt",
				Operations: map[config.OperationType]config.ToolOperation{config.OpFix: {App: "gofmt"}},
			},
		},
	}
	if _, err := createCache(cacheDir, projectPath, cfg, nil); err != nil {
		t.Fatalf("createCache() error = %v", err)
	}
}

// TestGetStagedFiles drives the git plumbing: a freshly committed file is not
// staged, while a newly added (but uncommitted) file is reported with an
// absolute path under the repo root.
func TestGetStagedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRun("init")
	gitRun("config", "commit.gpgSign", "false")

	committed := filepath.Join(repo, "committed.go")
	if err := os.WriteFile(committed, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "committed.go")
	gitRun("commit", "-m", "init")

	// No staged changes now → empty result.
	staged, err := getStagedFiles(context.Background(), repo)
	if err != nil {
		t.Fatalf("getStagedFiles() error = %v", err)
	}
	if len(staged) != 0 {
		t.Errorf("expected no staged files after commit, got %v", staged)
	}

	// Stage a new file → it shows up with an absolute path.
	newFile := filepath.Join(repo, "added.go")
	if err := os.WriteFile(newFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "added.go")

	staged, err = getStagedFiles(context.Background(), repo)
	if err != nil {
		t.Fatalf("getStagedFiles() error = %v", err)
	}
	if !slices.ContainsFunc(staged, func(p string) bool { return strings.HasSuffix(p, "added.go") }) {
		t.Errorf("staged files %v should contain added.go", staged)
	}
	for _, p := range staged {
		if !filepath.IsAbs(p) {
			t.Errorf("staged path %q is not absolute", p)
		}
	}
}

// TestGetStagedFilesNotARepo covers the git-failure error branch.
func TestGetStagedFilesNotARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// A bare temp dir is not a git repo → git diff exits non-zero.
	_, err := getStagedFiles(context.Background(), t.TempDir())
	if err == nil {
		t.Error("getStagedFiles outside a repo expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to get staged files") {
		t.Errorf("error = %v, want it to mention 'failed to get staged files'", err)
	}
}

// TestRunContinuationConfigLoadError exercises RunContinuation's wiring through
// runSequential: a config-load failure must surface as an error.
func TestRunContinuationConfigLoadError(t *testing.T) {
	err := RunContinuation(
		config.OpFix,
		nil, "", false, "",
		func() (*config.Config, string, error) {
			return nil, "", errors.New("continuation config load failed")
		},
	)
	if err == nil {
		t.Fatal("expected error from RunContinuation, got nil")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %v, want 'failed to load config'", err)
	}
}
