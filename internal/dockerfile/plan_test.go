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
			m[l] = binmanager.BinaryOsArchInfo{Auth: nil, URL: "", Hash: "", ContentType: "", BinaryPath: nil, ExtractDir: false}
		}
		b[syslist.OsTypeLinux][arch] = m
	}
	return &binmanager.AppConfigBinary{Version: "", Binaries: b}
}

func sampleApps() binmanager.MapOfApps {
	return binmanager.MapOfApps{
		"biome":      {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Bun: &binmanager.AppConfigBun{PackageName: "", Version: "", BinPath: "", Runtime: "", LockFile: "", Dependencies: nil}},
		"shellcheck": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}},
		"prettier":   {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Node: &binmanager.AppConfigNode{PackageName: "", Version: "", BinPath: "", Runtime: "", LockFile: "", Dependencies: nil}},
		"ruff":       {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Uv: &binmanager.AppConfigUV{PackageName: "", Version: "", Runtime: "", LockFile: "", RequiresPython: ""}},
		"golangci":   {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Go: &binmanager.AppConfigGo{PackageName: "", Version: "", Runtime: "", LockFile: ""}},
		"ktlint":     {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Node: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Jvm: &binmanager.AppConfigJVM{JarURL: "", JarHash: "", Version: "", Runtime: "", MainClass: ""}},
		"localfmt":   {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Shell: &binmanager.AppConfigShell{Args: nil, Name: "fmt"}},
	}
}

func sampleRuntimes() config.MapOfRuntimes {
	return config.MapOfRuntimes{
		"bun":  {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindBun},
		"node": {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindNode},
		"uv":   {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindUV},
		"go":   {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindGo},
		"jvm":  {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindJVM},
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
		{App: "biome", Kind: config.RuntimeKindBun, Runtime: "bun"},
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
		{PNPMRuntime: "", Name: "bun", Kind: config.RuntimeKindBun},
		{PNPMRuntime: "", Name: "go", Kind: config.RuntimeKindGo},
		{PNPMRuntime: "", Name: "jvm", Kind: config.RuntimeKindJVM},
		{PNPMRuntime: "", Name: "node", Kind: config.RuntimeKindNode},
		{PNPMRuntime: "", Name: "uv", Kind: config.RuntimeKindUV},
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
		"prettier": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Node: &binmanager.AppConfigNode{PackageName: "", Version: "", BinPath: "", Runtime: "", LockFile: "", Dependencies: nil}},
		"eslint":   {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Node: &binmanager.AppConfigNode{PackageName: "", Version: "", BinPath: "", Runtime: "", LockFile: "", Dependencies: nil}},
	}
	plan := BuildPlan(apps, config.MapOfRuntimes{"node": {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindNode}})

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
		"prettier": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Node: &binmanager.AppConfigNode{PackageName: "", Version: "", BinPath: "", LockFile: "", Dependencies: nil, Runtime: "does-not-exist"}},
	}
	plan := BuildPlan(apps, config.MapOfRuntimes{"node": {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindNode}})

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
		"ruff": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: linuxBin(map[syslist.ArchType][]string{amd64: both, arm64: both})},
		"swag": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: linuxBin(map[syslist.ArchType][]string{amd64: {"glibc"}, arm64: {"glibc"}})},
		// musl present on amd64 but missing on arm64 -> not musl-complete.
		"snyk": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: linuxBin(map[syslist.ArchType][]string{amd64: both, arm64: {"glibc"}})},
		"task": {Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Binary: linuxBin(map[syslist.ArchType][]string{amd64: {"glibc"}, arm64: {"glibc"}})},
	}

	// musl target, force-including task (a universal tool under-declared as glibc).
	musl := BuildPlan(apps, config.MapOfRuntimes{}, PlanOptions{
		Parsers:      nil,
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
	glibc := BuildPlan(apps, config.MapOfRuntimes{}, PlanOptions{ForceInclude: nil, Parsers: nil, TargetLibc: "glibc"})
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
