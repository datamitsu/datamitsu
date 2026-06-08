package dockerfile

import (
	"reflect"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

// linuxBin builds a binary app whose linux binaries are present for the given
// arch->libc layout (BinaryOsArchInfo contents are irrelevant to planning).
func linuxBin(arches map[syslist.ArchType][]string) *binmanager.AppConfigBinary {
	b := binmanager.MapOfBinaries{syslist.OsTypeLinux: {}}
	for arch, libcs := range arches {
		m := make(map[string]binmanager.BinaryOsArchInfo, len(libcs))
		for _, l := range libcs {
			m[l] = binmanager.BinaryOsArchInfo{}
		}
		b[syslist.OsTypeLinux][arch] = m
	}
	return &binmanager.AppConfigBinary{Binaries: b}
}

func sampleApps() binmanager.MapOfApps {
	return binmanager.MapOfApps{
		"shellcheck": {Binary: &binmanager.AppConfigBinary{}},
		"prettier":   {Node: &binmanager.AppConfigNode{}},
		"ruff":       {Uv: &binmanager.AppConfigUV{}},
		"golangci":   {Go: &binmanager.AppConfigGo{}},
		"ktlint":     {Jvm: &binmanager.AppConfigJVM{}},
		"localfmt":   {Shell: &binmanager.AppConfigShell{Name: "fmt"}},
	}
}

func sampleRuntimes() config.MapOfRuntimes {
	return config.MapOfRuntimes{
		"node": {Kind: config.RuntimeKindNode},
		"uv":   {Kind: config.RuntimeKindUV},
		"go":   {Kind: config.RuntimeKindGo},
		"jvm":  {Kind: config.RuntimeKindJVM},
	}
}

func TestBuildPlan_Classification(t *testing.T) {
	plan := BuildPlan(sampleApps(), sampleRuntimes())

	wantBinary := []BinaryStage{{App: "shellcheck"}}
	if !reflect.DeepEqual(plan.BinaryStages, wantBinary) {
		t.Errorf("BinaryStages = %+v, want %+v", plan.BinaryStages, wantBinary)
	}

	// App stages are name-sorted.
	wantApps := []RuntimeAppStage{
		{App: "golangci", Kind: config.RuntimeKindGo, Runtime: "go"},
		{App: "ktlint", Kind: config.RuntimeKindJVM, Runtime: "jvm"},
		{App: "prettier", Kind: config.RuntimeKindNode, Runtime: "node"},
		{App: "ruff", Kind: config.RuntimeKindUV, Runtime: "uv"},
	}
	if !reflect.DeepEqual(plan.RuntimeAppStages, wantApps) {
		t.Errorf("RuntimeAppStages = %+v, want %+v", plan.RuntimeAppStages, wantApps)
	}

	// Runtime stages are name sorted.
	wantRuntimes := []RuntimeStage{
		{Name: "go", Kind: config.RuntimeKindGo},
		{Name: "jvm", Kind: config.RuntimeKindJVM},
		{Name: "node", Kind: config.RuntimeKindNode},
		{Name: "uv", Kind: config.RuntimeKindUV},
	}
	if !reflect.DeepEqual(plan.RuntimeStages, wantRuntimes) {
		t.Errorf("RuntimeStages = %+v, want %+v", plan.RuntimeStages, wantRuntimes)
	}

	if !reflect.DeepEqual(plan.Skipped, []string{"localfmt"}) {
		t.Errorf("Skipped = %+v, want [localfmt]", plan.Skipped)
	}
}

func TestBuildPlan_Deterministic(t *testing.T) {
	a := BuildPlan(sampleApps(), sampleRuntimes())
	b := BuildPlan(sampleApps(), sampleRuntimes())
	if !reflect.DeepEqual(a, b) {
		t.Error("BuildPlan is not deterministic across runs")
	}
}

func TestBuildPlan_SharedRuntime(t *testing.T) {
	apps := binmanager.MapOfApps{
		"prettier": {Node: &binmanager.AppConfigNode{}},
		"eslint":   {Node: &binmanager.AppConfigNode{}},
	}
	plan := BuildPlan(apps, config.MapOfRuntimes{"node": {Kind: config.RuntimeKindNode}})

	if len(plan.RuntimeStages) != 1 {
		t.Fatalf("expected 1 shared runtime stage, got %d", len(plan.RuntimeStages))
	}
	if len(plan.RuntimeAppStages) != 2 {
		t.Fatalf("expected 2 app stages, got %d", len(plan.RuntimeAppStages))
	}
	for _, ts := range plan.RuntimeAppStages {
		if ts.Runtime != "node" {
			t.Errorf("app %s runtime = %q, want node", ts.App, ts.Runtime)
		}
	}
}

func TestBuildPlan_DanglingRuntimeRefSkipped(t *testing.T) {
	apps := binmanager.MapOfApps{
		"prettier": {Node: &binmanager.AppConfigNode{Runtime: "does-not-exist"}},
	}
	plan := BuildPlan(apps, config.MapOfRuntimes{"node": {Kind: config.RuntimeKindNode}})

	if len(plan.RuntimeAppStages) != 0 {
		t.Errorf("expected dangling-ref app to be skipped, got app stages %+v", plan.RuntimeAppStages)
	}
	if !reflect.DeepEqual(plan.Skipped, []string{"prettier"}) {
		t.Errorf("Skipped = %+v, want [prettier]", plan.Skipped)
	}
}

func TestBuildPlan_LibcFilter(t *testing.T) {
	both := []string{"glibc", "musl"}
	amd64, arm64 := syslist.ArchTypeAmd64, syslist.ArchTypeArm64
	apps := binmanager.MapOfApps{
		"ruff": {Binary: linuxBin(map[syslist.ArchType][]string{amd64: both, arm64: both})},
		"swag": {Binary: linuxBin(map[syslist.ArchType][]string{amd64: {"glibc"}, arm64: {"glibc"}})},
		// musl present on amd64 but missing on arm64 -> not musl-complete.
		"snyk": {Binary: linuxBin(map[syslist.ArchType][]string{amd64: both, arm64: {"glibc"}})},
		"task": {Binary: linuxBin(map[syslist.ArchType][]string{amd64: {"glibc"}, arm64: {"glibc"}})},
	}

	// musl target, force-including task (a universal tool under-declared as glibc).
	musl := BuildPlan(apps, config.MapOfRuntimes{}, PlanOptions{
		TargetLibc:   "musl",
		ForceInclude: map[string]bool{"task": true},
	})
	if want := []BinaryStage{{App: "ruff"}, {App: "task"}}; !reflect.DeepEqual(musl.BinaryStages, want) {
		t.Errorf("musl BinaryStages = %+v, want %+v", musl.BinaryStages, want)
	}
	if want := []string{"snyk", "swag"}; !reflect.DeepEqual(musl.LibcExcluded, want) {
		t.Errorf("musl LibcExcluded = %+v, want %+v", musl.LibcExcluded, want)
	}

	// glibc target: every app has glibc on both arches -> all kept, none excluded.
	glibc := BuildPlan(apps, config.MapOfRuntimes{}, PlanOptions{TargetLibc: "glibc"})
	if len(glibc.BinaryStages) != 4 || len(glibc.LibcExcluded) != 0 {
		t.Errorf("glibc: bins=%d excluded=%v, want 4 bins / 0 excluded", len(glibc.BinaryStages), glibc.LibcExcluded)
	}

	// No filter (zero options): every binary app is kept regardless of libc.
	none := BuildPlan(apps, config.MapOfRuntimes{})
	if len(none.BinaryStages) != 4 || len(none.LibcExcluded) != 0 {
		t.Errorf("no filter: bins=%d excluded=%v, want 4 bins / 0 excluded", len(none.BinaryStages), none.LibcExcluded)
	}
}

func TestBuildPlan_Empty(t *testing.T) {
	plan := BuildPlan(binmanager.MapOfApps{}, config.MapOfRuntimes{})
	if len(plan.RuntimeStages)+len(plan.RuntimeAppStages)+len(plan.BinaryStages) != 0 {
		t.Errorf("expected empty plan, got %+v", plan)
	}
}
