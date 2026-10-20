//go:build e2e_oci

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// reviewdogApp is reviewdog v0.21.2 as a binary app of the test's own config:
// the release archives, each pinned by the SHA-256 the release's
// checksums.txt publishes.
const reviewdogApp = `c.apps.reviewdog = {binary: {version: "0.21.2", binaries: {
  darwin: {
    amd64: {unknown: {contentType: "tar.gz", binaryPath: "reviewdog-0.21.2/reviewdog",
      hash: "sha256:3002ad37ee7d344de25e3c630a293aa322fc10d7b9c3fc6d4e836b616d084ae5",
      url: "https://github.com/reviewdog/reviewdog/releases/download/v0.21.2/reviewdog_0.21.2_Darwin_x86_64.tar.gz"}},
    arm64: {unknown: {contentType: "tar.gz", binaryPath: "reviewdog-0.21.2/reviewdog",
      hash: "sha256:54497b0f3378936caa22a3a11d796acefe4d5f35d86f5cc84c622f941c3c0c68",
      url: "https://github.com/reviewdog/reviewdog/releases/download/v0.21.2/reviewdog_0.21.2_Darwin_arm64.tar.gz"}},
  },
  linux: {
    amd64: {glibc: {contentType: "tar.gz", binaryPath: "reviewdog-0.21.2/reviewdog",
      hash: "sha256:30413aa3c7443e9c3c157fe5766cad40e3bb39a32e210ee69b710a8d5c4b8e51",
      url: "https://github.com/reviewdog/reviewdog/releases/download/v0.21.2/reviewdog_0.21.2_Linux_x86_64.tar.gz"}},
    arm64: {glibc: {contentType: "tar.gz", binaryPath: "reviewdog-0.21.2/reviewdog",
      hash: "sha256:56854c078853ea53c73e1e4d627e6a90d6b68185c46edd46aad8beb94564b948",
      url: "https://github.com/reviewdog/reviewdog/releases/download/v0.21.2/reviewdog_0.21.2_Linux_arm64.tar.gz"}},
  },
}}};
`

// TestReviewdogReadsRdjsonl: reviewdog itself reads the rdjsonl a run writes,
// finding by finding, with the level, rule and position the report holds. Its
// SARIF reporter writes back what it read.
func TestReviewdogReadsRdjsonl(t *testing.T) {
	RequireOCIE2E(t)
	clitest.RequireShell(t, "reviewdog reading --report rdjsonl")

	cacheDir := t.TempDir()
	p := clitest.NewProject(t)
	clitest.MarkerDir(p)
	p.WriteFile("fixture.marker", "")
	p.WriteFile("Dockerfile", "FROM debian\nWORKDIR app\n")
	module, err := filepath.Abs(filepath.Join("..", "..", "internal", "parsermanager", "testdata", "echo.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	hadolint := clitest.ShellTool("hadolint",
		clitest.RecordRun+`; echo '[{"file":"Dockerfile","line":1,"column":1,"level":"warning","code":"DL3006","message":"Always tag the version of an image explicitly"},`+
			`{"file":"Dockerfile","line":2,"column":1,"level":"error","code":"DL3000","message":"Use absolute WORKDIR"}]'`,
		clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/Dockerfile"}, Args: []string{"{file}"}, Parser: "hadolint"})
	cfg := p.WriteFile("e2e.config.js", clitest.ShellConfig(clitest.ShellConfigSpec{
		ProjectTypes: map[string][]string{"fixture": {"fixture.marker"}},
		Parsers:      clitest.SeedParserModule(t, cacheDir, module),
		Extra:        reviewdogApp,
	}, hadolint))
	base := []string{"--no-auto-config", "--config", cfg}

	lint := runOnline(t, p.Dir, cacheDir, append(base, "lint", "--report", "rdjsonl=out/rd.jsonl")...)
	if lint.ExitCode != 1 {
		t.Fatalf("lint exit = %d, want 1 on DL3000\nstdout:\n%s\nstderr:\n%s", lint.ExitCode, lint.Stdout, lint.Stderr)
	}
	stream, err := os.ReadFile(filepath.Join(p.Dir, "out", "rd.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	res := runOnlineStdin(t, p.Dir, cacheDir, string(stream), append(base,
		"exec", "reviewdog", "--", "-f=rdjsonl", "-reporter=sarif", "-filter-mode=nofilter", "-fail-level=none")...)
	if res.ExitCode != 0 {
		t.Fatalf("reviewdog exit = %d\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	// The download progress shares stdout with the document.
	start := strings.Index(res.Stdout, "{")
	if start < 0 {
		t.Fatalf("reviewdog wrote no SARIF:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	}
	var read struct {
		Runs []struct {
			Results []struct {
				RuleID  string `json:"ruleId"`
				Level   string `json:"level"`
				Message struct {
					Text string `json:"text"`
				} `json:"message"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine   int `json:"startLine"`
							StartColumn int `json:"startColumn"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(res.Stdout[start:]), &read); err != nil || len(read.Runs) != 1 {
		t.Fatalf("reviewdog's SARIF = %v\n%s", err, res.Stdout)
	}
	got := make([]string, 0, len(read.Runs[0].Results))
	for _, r := range read.Runs[0].Results {
		loc := r.Locations[0].PhysicalLocation
		got = append(got, fmt.Sprintf("%s:%d:%d %s %s %s", loc.ArtifactLocation.URI, loc.Region.StartLine, loc.Region.StartColumn, r.Level, r.RuleID, r.Message.Text))
	}
	want := []string{
		"Dockerfile:1:1 warning DL3006 Always tag the version of an image explicitly",
		"Dockerfile:2:1 error DL3000 Use absolute WORKDIR",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("reviewdog read\n%s\nwant\n%s\nrdjsonl:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), stream)
	}
}

// runOnlineStdin is runOnline with stdin.
func runOnlineStdin(t *testing.T, dir, cacheDir, stdin string, args ...string) clitest.Result {
	t.Helper()
	bin := clitest.BuildOnce(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// bin is the harness-built binary and args come from test code.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = onlineEnv(cacheDir)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return clitest.Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: clitest.ExitCodeOf(err), Err: err}
}
