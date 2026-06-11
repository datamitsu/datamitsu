package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"
)

// brokenGitDir creates a directory that looks like a repo (.git present) in a
// process environment where git itself cannot run (PATH without a git binary)
// — the dubious-ownership / no-git-binary container case store commands must
// survive.
func brokenGitDir(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tmpDir)
	t.Setenv("PATH", tmpDir)
}

func TestLoadConfigGitRootFailureIsFatalForProjectCommands(t *testing.T) {
	brokenGitDir(t)

	_, _, _, err := loadConfigImpl(context.Background(), nil, false, nil, loadConfigOptions{})
	if err == nil {
		t.Fatal("expected a git-root failure, got nil")
	}
	if !strings.Contains(err.Error(), "failed to determine git root") {
		t.Errorf("err = %v, want a git-root failure", err)
	}
}

func TestLoadConfigGitRootFailureToleratedForStoreCommands(t *testing.T) {
	brokenGitDir(t)
	logs := swapLoggerWithObserver(t, zapcore.WarnLevel)

	cfg, _, _, err := loadConfigImpl(context.Background(), nil, false, nil,
		loadConfigOptions{tolerateGitRootFailure: true})
	if err != nil {
		t.Fatalf("tolerant load failed: %v", err)
	}
	if cfg == nil {
		t.Fatal("tolerant load returned nil config")
	}

	warned := false
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "cannot determine the git root") {
			warned = true
		}
	}
	if !warned {
		t.Error("expected a warning about skipping the project config")
	}
}

func TestLoadConfigForStoreSurvivesBrokenGit(t *testing.T) {
	brokenGitDir(t)
	swapLoggerWithObserver(t, zapcore.WarnLevel)

	cfg, err := loadConfigForStore(context.Background())
	if err != nil {
		t.Fatalf("loadConfigForStore() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("loadConfigForStore() returned nil config")
	}
}
