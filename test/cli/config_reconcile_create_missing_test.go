package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// reconcileCreateMissingConfigJS is a synthetic monorepo config: two managed
// project-scoped configs over a "fixture" project type detected anywhere under
// the repository. ".variant" carries an otherFileNameList to exercise the
// never-touch-existing-variants rule.
const reconcileCreateMissingConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  version: "1.0.0",
  runtimes: {},
  projectTypes: { fixture: { markers: ["**/fixture.marker"] } },
  managedConfigs: {
    ".tool.config": {
      projectTypes: ["fixture"],
      scope: "project",
      content: function () { return "managed\n"; }
    },
    ".variant": {
      projectTypes: ["fixture"],
      scope: "project",
      content: function () { return "variant-main\n"; },
      otherFileNameList: [".variant.old"]
    }
  }
});
globalThis.getMinVersion = () => "0.0.0";
`

// newCreateMissingProject builds a monorepo with two subprojects. packages/b
// already carries its own customized .tool.config; its bytes must survive every run.
func newCreateMissingProject(t *testing.T) (cfg string, customToolConfig string) {
	t.Helper()
	p := clitest.NewProject(t)
	cfg = p.WriteFile("tools.config.js", reconcileCreateMissingConfigJS)
	p.WriteFile("packages/a/fixture.marker", "a\n")
	p.WriteFile("packages/b/fixture.marker", "b\n")
	customToolConfig = p.WriteFile("packages/b/.tool.config", "my own rules\n")
	return cfg, customToolConfig
}

func TestConfigReconcileCreateMissingCreatesOnlyMissing(t *testing.T) {
	cfg, customToolConfig := newCreateMissingProject(t)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: filepath.Dir(cfg)},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--create-missing")
	if res.ExitCode != 0 {
		t.Fatalf("`config reconcile --create-missing` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`config reconcile --create-missing` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "config_reconcile_create_missing", norm.Apply(res.Stdout))

	// The missing files were created with the managed content…
	data, err := os.ReadFile(filepath.Join(filepath.Dir(cfg), "packages/a/.tool.config"))
	if err != nil || string(data) != "managed\n" {
		t.Errorf("packages/a/.tool.config = %q, err %v; want managed content", data, err)
	}
	// …and the pre-existing customized file kept its bytes.
	data, err = os.ReadFile(customToolConfig)
	if err != nil || string(data) != "my own rules\n" {
		t.Errorf("customized .tool.config rewritten: %q, err %v", data, err)
	}

	// A second run changes nothing: every file is reported as existing.
	res2 := clitest.Run(t, clitest.RunOptions{Dir: filepath.Dir(cfg)},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--create-missing")
	if res2.ExitCode != 0 {
		t.Fatalf("second run exit = %d, want 0\nstderr:\n%s", res2.ExitCode, res2.Stderr)
	}
	if strings.Contains(norm.Apply(res2.Stdout), "+") {
		t.Errorf("second run still reports created files:\n%s", res2.Stdout)
	}
	if !strings.Contains(norm.Apply(res2.Stdout), "4 existing") {
		t.Errorf("second run footer should count 4 existing files:\n%s", res2.Stdout)
	}
	data, _ = os.ReadFile(customToolConfig)
	if string(data) != "my own rules\n" {
		t.Errorf("customized .tool.config rewritten on second run: %q", data)
	}
}

func TestConfigReconcileCreateMissingJSON(t *testing.T) {
	cfg, _ := newCreateMissingProject(t)

	// --json is a document and nothing else: stdout must parse as a whole.
	res := clitest.Run(t, clitest.RunOptions{Dir: filepath.Dir(cfg)},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--create-missing", "--json")
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	var doc struct {
		DryRun        bool `json:"dryRun"`
		CreateMissing bool `json:"createMissing"`
		Counts        struct {
			Created         int `json:"created"`
			SkippedExisting int `json:"skippedExisting"`
			Failed          int `json:"failed"`
		} `json:"counts"`
		Results []struct {
			Config string `json:"config"`
			Path   string `json:"path"`
			Action string `json:"action"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v\nstdout:\n%s", err, res.Stdout)
	}
	if doc.DryRun || !doc.CreateMissing {
		t.Errorf("document flags = (dryRun %v, createMissing %v)", doc.DryRun, doc.CreateMissing)
	}
	if doc.Counts.Created != 3 || doc.Counts.SkippedExisting != 1 || doc.Counts.Failed != 0 {
		t.Errorf("counts = %+v, want 3 created and 1 skipped-existing (b's custom .tool.config)", doc.Counts)
	}
	actions := map[string]bool{}
	for _, r := range doc.Results {
		actions[r.Action] = true
	}
	if !actions["created"] {
		t.Errorf("no created action in results: %+v", doc.Results)
	}
}

func TestConfigReconcileCreateMissingVariantPreserved(t *testing.T) {
	cfg, _ := newCreateMissingProject(t)
	root := filepath.Dir(cfg)
	// packages/a holds the project's own config under the old name only.
	oldPath := root + "/packages/a/.variant.old"
	if err := os.WriteFile(oldPath, []byte("user variant\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := clitest.Run(t, clitest.RunOptions{Dir: root},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--create-missing")
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	data, err := os.ReadFile(oldPath)
	if err != nil || string(data) != "user variant\n" {
		t.Errorf(".variant.old mutated: %q, err %v", data, err)
	}
	data, err = os.ReadFile(root + "/packages/a/.variant")
	if err != nil || string(data) != "variant-main\n" {
		t.Errorf(".variant = %q, err %v; want the missing main file created", data, err)
	}
}

func TestConfigReconcileCreateMissingCollision(t *testing.T) {
	cfg, _ := newCreateMissingProject(t)
	root := filepath.Dir(cfg)
	if err := os.MkdirAll(root+"/packages/a/.tool.config", 0o755); err != nil {
		t.Fatal(err)
	}

	res := clitest.Run(t, clitest.RunOptions{Dir: root},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--create-missing")
	if res.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "not a regular file") {
		t.Errorf("error section should name the collision:\n%s", res.Stdout)
	}
}

func TestConfigReconcileCreateMissingForceUsage(t *testing.T) {
	cfg, _ := newCreateMissingProject(t)

	res := clitest.Run(t, clitest.RunOptions{Dir: filepath.Dir(cfg)},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--create-missing", "--force")
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "create-missing") {
		t.Errorf("stderr should name the conflicting flags:\n%s", res.Stderr)
	}
}

func TestConfigReconcileCreateMissingFailingGenerator(t *testing.T) {
	// A generator that fails must fail the run: a result carrying an error but
	// no action used to be filtered out as a skip, reporting success with the
	// file never written.
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  version: "1.0.0",
  runtimes: {},
  projectTypes: { fixture: { markers: ["**/fixture.marker"] } },
  managedConfigs: {
    ".tool.config": {
      projectTypes: ["fixture"],
      scope: "project",
      content: function () { throw new Error("generator boom"); }
    }
  }
});
globalThis.getMinVersion = () => "0.0.0";
`)
	p.WriteFile("packages/a/fixture.marker", "a\n")

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--create-missing", "--json")
	if res.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	var doc struct {
		Counts struct {
			Created int `json:"created"`
			Failed  int `json:"failed"`
		} `json:"counts"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatalf("stdout is not a JSON document: %v\n%s", err, res.Stdout)
	}
	if doc.Counts.Failed != 1 || doc.Counts.Created != 0 {
		t.Errorf("counts = %+v, want 1 failed, 0 created", doc.Counts)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "packages", "a", ".tool.config")); !os.IsNotExist(err) {
		t.Error("failed generator must not leave a file behind")
	}
}

func TestInitWritesLauncherMarker(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	home := t.TempDir()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Env: []string{"HOME=" + home}},
		"--no-auto-config", "--config", cfg, "init")
	if res.ExitCode != 0 {
		t.Fatalf("`init` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	data, err := os.ReadFile(filepath.Join(p.Dir, ".datamitsu", "launcher.json"))
	if err != nil {
		t.Fatalf("init did not write the launcher marker: %v", err)
	}
	var marker struct {
		Command     string `json:"command"`
		BinaryPath  string `json:"binaryPath"`
		CoreVersion string `json:"coreVersion"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("launcher marker is not valid JSON: %v\n%s", err, data)
	}
	if marker.Command == "" || marker.BinaryPath == "" || marker.CoreVersion == "" {
		t.Errorf("launcher marker incomplete: %+v", marker)
	}
	if !filepath.IsAbs(marker.BinaryPath) {
		t.Errorf("binaryPath = %q, want an absolute path", marker.BinaryPath)
	}
}
