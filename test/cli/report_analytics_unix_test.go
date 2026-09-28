//go:build !windows

package cli_test

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// TestReportDiffCancelled: a finding of the first run whose tool the second
// run cancelled is unknown, never fixed: the second run did not look.
func TestReportDiffCancelled(t *testing.T) {
	e := diffProject(t, hadolintFinding)
	e.wantExit(e.run("", nil, "lint", "--report", "json=before.json"), 0)

	// The second run's hadolint waits to be interrupted.
	e.p.WriteFile(filepath.Join(clitest.MarkerDirName, "findings"), "[]")
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	waits := clitest.ShellTool("hadolint", `echo started >> "$MARKERS/started"; sleep 30`,
		clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/Dockerfile"}, Args: []string{"{file}"}, Parser: "hadolint"})
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, waits))

	proc := clitest.Start(t, clitest.RunOptions{Dir: e.p.Dir, CacheDir: e.cache},
		"--no-auto-config", "--config", e.cfg, "lint", "--report", "json=after.json")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if ran, _ := e.p.Marker("started"); ran {
			break
		}
		if time.Now().After(deadline) {
			_ = proc.Signal(syscall.SIGKILL)
			t.Fatalf("hadolint never started:\n%+v", proc.Wait())
		}
	}
	if err := proc.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	proc.Wait()

	res := e.run("", nil, "report", "diff", "before.json", "after.json")
	e.wantExit(res, 0)
	if !strings.Contains(res.Stdout, `"unknown": 1`) || !strings.Contains(res.Stdout, `"cancelled"`) || strings.Contains(res.Stdout, `"fixed": 1`) {
		t.Errorf("the cancelled tool's finding should be unknown, with the reason:\n%s", res.Stdout)
	}
}
