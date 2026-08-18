package clitest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/gitenv"
)

// Project is an isolated, git-initialized working tree that serves as the CWD
// for a CLI invocation. Dir is the symlink-evaluated absolute path so it matches
// what the binary observes as the git root (e.g. macOS /var → /private/var),
// keeping golden comparisons stable.
type Project struct {
	tb  testing.TB
	Dir string
}

// NewProject creates a fresh temp directory, resolves its symlinks, runs
// `git init` and sets a minimal local git identity, then returns it as a
// Project. It fails the test on any setup error.
func NewProject(tb testing.TB) *Project {
	tb.Helper()

	dir, err := filepath.EvalSymlinks(tb.TempDir())
	if err != nil {
		tb.Fatalf("clitest: eval symlinks for project dir: %v", err)
	}

	p := &Project{tb: tb, Dir: dir}
	// `git init` plus a local identity is enough for rev-parse/show-toplevel and
	// for any command that needs a clean, isolated git root. We never commit, so
	// no signing config is required.
	p.git("init")
	p.git("config", "user.name", "datamitsu-clitest")
	p.git("config", "user.email", "clitest@datamitsu.invalid")
	return p
}

// WriteFile writes content to rel (relative to the project root), creating any
// parent directories, and returns the absolute path written. It fails the test
// on error.
func (p *Project) WriteFile(rel, content string) string {
	p.tb.Helper()
	abs := filepath.Join(p.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		p.tb.Fatalf("clitest: mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		p.tb.Fatalf("clitest: write %s: %v", rel, err)
	}
	return abs
}

// Chdir switches the test's working directory to the project root for the
// duration of the test (restored automatically via testing.TB.Chdir). Subprocess
// runs should prefer RunOptions.Dir; Chdir is for in-process helpers that read
// the CWD.
func (p *Project) Chdir() {
	p.tb.Helper()
	p.tb.Chdir(p.Dir)
}

// git runs a git subcommand in the project directory, failing the test on error.
func (p *Project) git(args ...string) {
	p.tb.Helper()
	// G204: args are fixed test-controlled git subcommands, not untrusted input.
	cmd := exec.CommandContext(context.Background(), "git", args...) //nolint:gosec
	cmd.Dir = p.Dir
	cmd.Env = gitenv.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		p.tb.Fatalf("clitest: git %v failed: %v\n%s", args, err, out)
	}
}

// minimalConfigJS is a no-op config: getConfig discards the inherited (default)
// config and returns empty collections, so `config show` is deterministic and
// minimal regardless of what the embedded default contributes. getMinVersion is
// pinned low so any build satisfies it.
const minimalConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({ apps: {}, runtimes: {}, setup: {}, tools: {} });
globalThis.getMinVersion = () => "0.0.0";
`

// WriteMinimalConfig writes the no-op minimal config into the project and returns
// its absolute path, suitable for passing via `--no-auto-config --config <path>`.
// It is named so it is NOT auto-discovered (avoids accidental double-loading when
// a test also relies on auto-discovery).
func WriteMinimalConfig(p *Project) string {
	p.tb.Helper()
	return p.WriteFile("minimal.config.js", minimalConfigJS)
}

// WriteOverlayConfig writes an auto-discoverable datamitsu.config.js that inherits
// beforeConfigPath via getBeforeConfigs() and applies mutateJS inside getConfig.
// mutateJS is the body of `getConfig(config)` and must return a config object
// (e.g. "return { ...config, tools: {} };" to disable all tools). Because
// getBeforeConfigs() is only honored for the auto-discovered config, this file is
// written as datamitsu.config.js at the project root and is meant to be loaded via
// auto-discovery (do NOT pass --no-auto-config for the overlay to take effect).
func WriteOverlayConfig(p *Project, beforeConfigPath, mutateJS string) string {
	p.tb.Helper()
	js := "globalThis.getBeforeConfigs = () => [{ path: " + jsString(beforeConfigPath) + " }];\n" +
		"globalThis.getConfig = (config) => { " + mutateJS + " };\n" +
		"globalThis.getMinVersion = () => \"0.0.0\";\n"
	return p.WriteFile("datamitsu.config.js", js)
}

// WriteDatamitsuIgnore writes a .datamitsuignore file with the given lines into
// the project root and returns its absolute path.
func WriteDatamitsuIgnore(p *Project, lines []string) string {
	p.tb.Helper()
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	return p.WriteFile(".datamitsuignore", content)
}

// jsString renders s as a double-quoted JS/JSON string literal, escaping
// backslashes and quotes so Windows paths and special characters survive.
func jsString(s string) string {
	var b []byte
	b = append(b, '"')
	for i := range len(s) {
		switch c := s[i]; c {
		case '\\', '"':
			b = append(b, '\\', c)
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		default:
			b = append(b, c)
		}
	}
	b = append(b, '"')
	return string(b)
}
