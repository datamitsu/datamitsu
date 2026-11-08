package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

const runtimeProbeConfig = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({version:"1.0.0",apps:{},runtimes:{
  zed:{kind:"go",mode:"system",system:{command:%s,systemVersion:"1.23"},go:{goVersion:"1.23"}},
  alpha:{kind:"node",mode:"system",system:{command:%s,systemVersion:"22"},node:{nodeVersion:"22",pnpmRuntime:"pnpm"}},
  pnpm:{kind:"pnpm",mode:"system",system:{command:"missing-pnpm-command"},pnpm:{pnpmVersion:"12"}}
},managedConfigs:{},tools:{}});
globalThis.getMinVersion = () => "0.0.0";
`

func runtimeProbe(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "runtime-probe"+exeSuffix())
	cmd := exec.Command("go", "build", "-o", out, "./testdata/runtime-probe")
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build runtime probe: %v\n%s", err, data)
	}
	return out
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func runtimeProject(t *testing.T, command string) (*clitest.Project, string, string) {
	t.Helper()
	p := clitest.NewProject(t)
	probe := command
	cfg := p.WriteFile("runtimes.config.js", sprintfRuntimeConfig(command, probe))
	return p, cfg, probe
}

func sprintfRuntimeConfig(command, probe string) string {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	return strings.Replace(strings.Replace(runtimeProbeConfig, "%s", q(command), 1), "%s", q(probe), 1)
}

func runtimeArgs(p *clitest.Project, cfg string, args ...string) []string {
	return append([]string{"--no-auto-config", "--config", cfg, "runtime"}, args...)
}

func TestRuntimeListJSONInventoryAndEmpty(t *testing.T) {
	p, cfg, probe := runtimeProject(t, runtimeProbe(t))
	cacheRoot := t.TempDir()
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheRoot}, runtimeArgs(p, cfg, "list", "--json")...)
	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("runtime list: %d\n%s", res.ExitCode, res.Stderr)
	}
	type runtimeInventory struct {
		Name           string  `json:"name"`
		Kind           string  `json:"kind"`
		ConfiguredMode string  `json:"configuredMode"`
		EffectiveMode  string  `json:"effectiveMode"`
		Version        string  `json:"version"`
		Runnable       bool    `json:"runnable"`
		Status         string  `json:"status"`
		Executable     *string `json:"executable"`
	}
	var doc struct {
		Schema   string             `json:"schema"`
		Runtimes []runtimeInventory `json:"runtimes"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != "datamitsu.runtimes/1" || len(doc.Runtimes) != 3 {
		t.Fatalf("inventory: %+v", doc)
	}
	if got := []string{doc.Runtimes[0].Name, doc.Runtimes[1].Name, doc.Runtimes[2].Name}; !slices.Equal(got, []string{"alpha", "pnpm", "zed"}) {
		t.Fatalf("order %v", got)
	}
	if doc.Runtimes[0].Kind != "node" || doc.Runtimes[0].ConfiguredMode != "system" || doc.Runtimes[0].EffectiveMode != "system" || doc.Runtimes[0].Version != "22" || !doc.Runtimes[0].Runnable || doc.Runtimes[0].Status != "installed" || doc.Runtimes[0].Executable == nil || *doc.Runtimes[0].Executable != probe {
		t.Fatalf("node inventory: %+v", doc.Runtimes[0])
	}
	if doc.Runtimes[1].Kind != "pnpm" || doc.Runtimes[1].ConfiguredMode != "system" || doc.Runtimes[1].EffectiveMode != "system" || doc.Runtimes[1].Version != "12" || doc.Runtimes[1].Runnable || doc.Runtimes[1].Status != "missing" {
		t.Fatalf("pnpm inventory: %+v", doc.Runtimes[1])
	}
	if doc.Runtimes[2].Kind != "go" || doc.Runtimes[2].ConfiguredMode != "system" || doc.Runtimes[2].EffectiveMode != "system" || doc.Runtimes[2].Version != "1.23" || !doc.Runtimes[2].Runnable || doc.Runtimes[2].Status != "installed" || doc.Runtimes[2].Executable == nil || *doc.Runtimes[2].Executable != probe {
		t.Fatalf("go inventory: %+v", doc.Runtimes[2])
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".datamitsu")); !os.IsNotExist(err) {
		t.Fatalf("list materialized project state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, "store")); !os.IsNotExist(err) {
		t.Fatalf("list materialized store state: %v", err)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Fatal(err)
	}

	empty := clitest.NewProject(t)
	emptyCfg := clitest.WriteMinimalConfig(empty)
	emptyRes := clitest.Run(t, clitest.RunOptions{Dir: empty.Dir}, runtimeArgs(empty, emptyCfg, "list", "--json")...)
	var emptyDoc struct {
		Runtimes []json.RawMessage `json:"runtimes"`
	}
	if emptyRes.ExitCode != 0 || json.Unmarshal([]byte(emptyRes.Stdout), &emptyDoc) != nil || emptyDoc.Runtimes == nil || len(emptyDoc.Runtimes) != 0 {
		t.Fatalf("empty inventory: %#v, exit %d", emptyRes, emptyRes.ExitCode)
	}
}

func TestRuntimeRunDryRunAndExactExecution(t *testing.T) {
	p, cfg, _ := runtimeProject(t, runtimeProbe(t))
	args := []string{"", "two words", "$(literal); *"}
	cacheRoot := t.TempDir()
	dry := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheRoot}, runtimeArgs(p, cfg, append([]string{"run", "--dry-run", "zed", "--"}, args...)...)...)
	if dry.ExitCode != 0 {
		t.Fatalf("dry run: %d\n%s", dry.ExitCode, dry.Stderr)
	}
	var description struct {
		Name         string   `json:"name"`
		Args         []string `json:"args"`
		WouldInstall bool     `json:"wouldInstall"`
		Status       string   `json:"status"`
	}
	if err := json.Unmarshal([]byte(dry.Stdout), &description); err != nil {
		t.Fatal(err)
	}
	if description.Name != "zed" || !slices.Equal(description.Args, args) || description.WouldInstall || description.Status != "installed" {
		t.Fatalf("dry run description: %+v", description)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".datamitsu")); !os.IsNotExist(err) {
		t.Fatalf("dry-run materialized project state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, "store")); !os.IsNotExist(err) {
		t.Fatalf("dry-run materialized store state: %v", err)
	}

	record := filepath.Join(p.Dir, "probe.json")
	childArgs := []string{"--record", record, "", "space value", "';$(echo no)"}
	run := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Stdin: "stdin payload"}, runtimeArgs(p, cfg, append([]string{"run", "zed", "--"}, childArgs...)...)...)
	if run.ExitCode != 0 || run.Stdout != "probe stdout\n" || run.Stderr != "probe stderr\n" {
		t.Fatalf("execution: %#v", run)
	}
	var captured struct {
		Args  []string          `json:"args"`
		Cwd   string            `json:"cwd"`
		Stdin string            `json:"stdin"`
		Env   map[string]string `json:"env"`
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &captured); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(captured.Args, childArgs) || captured.Cwd != p.Dir || captured.Stdin != "stdin payload" {
		t.Fatalf("probe capture: %+v", captured)
	}
	if captured.Env["GOTOOLCHAIN"] != "local" {
		t.Fatalf("runtime-owned environment not propagated: %+v", captured.Env)
	}
}

func TestRuntimeRunChildExitAndUsageErrors(t *testing.T) {
	p, cfg, probe := runtimeProject(t, runtimeProbe(t))
	status := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, runtimeArgs(p, cfg, "run", "zed", "--", "--exit", "37")...)
	if status.ExitCode != 37 || status.Stdout != "probe stdout\n" || status.Stderr != "probe stderr\n" {
		t.Fatalf("child result = %#v, want exit 37 and separate streams", status)
	}
	for _, tc := range []struct {
		name     string
		args     []string
		extra    []string
		contains string
	}{
		{"missing separator", []string{"run", "zed", "arg"}, nil, "usage:"},
		{"missing name", []string{"run", "--", "arg"}, nil, "usage:"},
		{"missing args", []string{"run", "zed", "--"}, nil, "usage:"},
		{"unknown runtime", []string{"run", "missing-runtime", "--", "x"}, nil, "unknown runtime"},
		{"pnpm is not runnable", []string{"run", "--dry-run", "pnpm", "--", "x"}, nil, "cannot be run directly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string(nil), tc.args...)
			if len(tc.extra) > 0 {
				args = append(tc.extra, args...)
			}
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, runtimeArgs(p, cfg, args...)...)
			if res.ExitCode != 2 || !strings.Contains(res.Stderr, tc.contains) {
				t.Fatalf("got %d\n%s", res.ExitCode, res.Stderr)
			}
		})
	}
	jsonlRecord := filepath.Join(p.Dir, "jsonl-probe.json")
	jsonlArgs := []string{"--no-auto-config", "--config", cfg, "--log-format=jsonl", "runtime", "run", "zed", "--", "--record", jsonlRecord, "x"}
	jsonl := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, jsonlArgs...)
	if jsonl.ExitCode != 2 || !strings.Contains(jsonl.Stderr, "cannot use --log-format jsonl") {
		t.Fatalf("JSONL rejection: %d\n%s", jsonl.ExitCode, jsonl.Stderr)
	}
	if _, err := os.Stat(jsonlRecord); !os.IsNotExist(err) {
		t.Fatalf("JSONL rejection ran child: %v", err)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSystemExecutablePresentAndMissing(t *testing.T) {
	p, cfg, probe := runtimeProject(t, runtimeProbe(t))
	present := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, runtimeArgs(p, cfg, "run", "zed", "--", "--exit", "0")...)
	if present.ExitCode != 0 {
		t.Fatalf("present executable: %d\n%s", present.ExitCode, present.Stderr)
	}
	missingCfg := p.WriteFile("missing.config.js", sprintfRuntimeConfig(filepath.Join(p.Dir, "does-not-exist"), probe))
	missing := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, runtimeArgs(p, missingCfg, "run", "zed", "--", "x")...)
	if missing.ExitCode == 0 || !strings.Contains(missing.Stderr, filepath.Base(filepath.Join(p.Dir, "does-not-exist"))) {
		t.Fatalf("missing executable: %d\n%s", missing.ExitCode, missing.Stderr)
	}
}
