package sourcefarm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/env"
)

// stubResolver answers from a fixed table, so a plan test never depends on a
// store, a config, or the network.
type stubResolver struct {
	infos     map[string]*binmanager.CommandInfo
	installed map[string]bool
	errs      map[string]error
	calls     []string
}

func (s *stubResolver) ResolveCommandInfo(appName string) (*binmanager.CommandInfo, bool, error) {
	s.calls = append(s.calls, appName)
	if err, ok := s.errs[appName]; ok {
		return nil, false, err
	}
	info, ok := s.infos[appName]
	if !ok {
		return nil, false, errors.New("app not found")
	}
	return info, s.installed[appName], nil
}

func binaryApp() binmanager.App {
	return binmanager.App{Binary: &binmanager.AppConfigBinary{}}
}

func goApp() binmanager.App {
	return binmanager.App{Go: &binmanager.AppConfigGo{PackageName: "example.com/x", Version: "1.0.0"}}
}

func nodeApp() binmanager.App {
	return binmanager.App{Node: &binmanager.AppConfigNode{PackageName: "prettier", Version: "3.0.0", BinPath: "bin/prettier.cjs"}}
}

func shellApp(name string) binmanager.App {
	return binmanager.App{Shell: &binmanager.AppConfigShell{Name: name}}
}

func entryByName(t *testing.T, plan Plan, name string) Entry {
	t.Helper()
	for _, e := range plan.Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no entry named %q in %+v", name, plan.Entries)
	return Entry{}
}

func excludedReason(plan Plan, name string) (string, bool) {
	for _, x := range plan.Excluded {
		if x.Name == name {
			return x.Reason, true
		}
	}
	return "", false
}

// TestBuildPlan_ShellAppAlwaysExcluded pins the fork-bomb rule: a shell app
// resolves its command through the inherited PATH, which the farm is prepended
// to, so a farm entry for one re-enters datamitsu without bound. The shipped
// default config declares `echo` as a shell app, so this is the common case,
// not a hypothetical.
func TestBuildPlan_ShellAppAlwaysExcluded(t *testing.T) {
	apps := binmanager.MapOfApps{"echo": shellApp("echo")}
	resolver := &stubResolver{
		infos:     map[string]*binmanager.CommandInfo{"echo": {Type: "shell", Command: "echo"}},
		installed: map[string]bool{"echo": true},
	}

	plan := BuildPlan("/repo", "/farm", apps, resolver, nil)

	if len(plan.Entries) != 0 {
		t.Fatalf("Entries = %+v, want none", plan.Entries)
	}
	reason, ok := excludedReason(plan, "echo")
	if !ok {
		t.Fatal("echo missing from Excluded")
	}
	if reason != ReasonShellApp {
		t.Errorf("reason = %q, want %q", reason, ReasonShellApp)
	}
}

// TestBuildPlan_DenyListedExcluded asserts the deny-list wins over a perfectly
// resolvable binary app: interposing on `sudo` or on datamitsu's own name is
// refused mechanically, not by asking whether the app looks usable.
func TestBuildPlan_DenyListedExcluded(t *testing.T) {
	// The mixed-case spellings are the case-insensitive-filesystem hazard: on
	// macOS and Windows a farm entry named `Git` is what the shell's PATH search
	// finds for a plain `git`, and the shim — which looks the manifest up
	// exactly — would exit 127 for it, breaking git in every activated shell.
	for _, name := range []string{"sudo", "git", "datamitsu", "sh", "ssh", "Git", "SUDO", "Env", "Datamitsu"} {
		t.Run(name, func(t *testing.T) {
			apps := binmanager.MapOfApps{name: binaryApp()}
			resolver := &stubResolver{
				infos:     map[string]*binmanager.CommandInfo{name: {Type: "binary", Command: "/store/.bin/" + name + "/abc"}},
				installed: map[string]bool{name: true},
			}

			plan := BuildPlan("/repo", "/farm", apps, resolver, nil)

			if len(plan.Entries) != 0 {
				t.Fatalf("Entries = %+v, want none", plan.Entries)
			}
			reason, ok := excludedReason(plan, name)
			if !ok {
				t.Fatalf("%s missing from Excluded", name)
			}
			if reason != ReasonDenyListed {
				t.Errorf("reason = %q, want %q", reason, ReasonDenyListed)
			}
			if !DenyListed(name) {
				t.Errorf("DenyListed(%q) = false, want true", name)
			}
		})
	}
}

// TestBuildPlan_UninstalledIsEntryNotExclusion is the D4 consequence and the
// single most important property in this package: a declared name that is not
// downloaded yet must still occupy the farm, or PATH falls through to a stale
// system binary that exits 0 with plausible output.
func TestBuildPlan_UninstalledIsEntryNotExclusion(t *testing.T) {
	apps := binmanager.MapOfApps{"tflint": binaryApp()}
	resolver := &stubResolver{
		infos:     map[string]*binmanager.CommandInfo{"tflint": {Type: "binary", Command: "/store/.bin/tflint/abc"}},
		installed: map[string]bool{"tflint": false},
	}

	plan := BuildPlan("/repo", "/farm", apps, resolver, nil)

	if _, ok := excludedReason(plan, "tflint"); ok {
		t.Fatal("tflint was excluded, want an entry")
	}
	entry := entryByName(t, plan, "tflint")
	if entry.Installed {
		t.Error("Installed = true, want false")
	}
	if entry.Strategy != StrategyShim {
		t.Errorf("Strategy = %q, want %q", entry.Strategy, StrategyShim)
	}
}

// TestBuildPlan_UnresolvableIsStillAnEntry covers the same D4 rule for an app
// the resolver cannot answer for at all. Losing the name from the farm would
// be the silent-wrong-binary failure; an entry with no Command lets the shim
// fail loudly instead.
func TestBuildPlan_UnresolvableIsStillAnEntry(t *testing.T) {
	apps := binmanager.MapOfApps{"broken": nodeApp()}
	resolver := &stubResolver{errs: map[string]error{"broken": errors.New("no runtime manager configured")}}

	plan := BuildPlan("/repo", "/farm", apps, resolver, nil)

	entry := entryByName(t, plan, "broken")
	if entry.Strategy != StrategyShim {
		t.Errorf("Strategy = %q, want %q", entry.Strategy, StrategyShim)
	}
	if entry.Command != "" || entry.Installed {
		t.Errorf("entry = %+v, want empty Command and Installed=false", entry)
	}
	if entry.Kind != "node" {
		t.Errorf("Kind = %q, want the declared kind even without a resolution", entry.Kind)
	}
}

// TestBuildPlan_EveryEntryIsAShim walks the cases a symlink was once chosen for
// alongside the ones it never could be. They all come out shims, and the first
// two are the point: an installed binary app with no args and no env is exactly
// what a store symlink would be cheapest for, and exactly what silently keeps
// running the previous branch's version, because nothing of datamitsu's runs
// when the kernel follows a symlink.
func TestBuildPlan_EveryEntryIsAShim(t *testing.T) {
	tests := []struct {
		name      string
		app       binmanager.App
		info      *binmanager.CommandInfo
		installed bool
		want      Strategy
	}{
		{
			name:      "all conditions met",
			app:       binaryApp(),
			info:      &binmanager.CommandInfo{Type: "binary", Command: "/store/.bin/tofu/abc"},
			installed: true,
			want:      StrategyShim,
		},
		{
			name:      "go kind, the other symlink candidate",
			app:       goApp(),
			info:      &binmanager.CommandInfo{Type: "go", Command: "/store/.apps/go/task/abc/bin/task"},
			installed: true,
			want:      StrategyShim,
		},
		{
			name:      "wrong kind",
			app:       nodeApp(),
			info:      &binmanager.CommandInfo{Type: "node", Command: "/store/.apps/node/prettier/abc/bin"},
			installed: true,
			want:      StrategyShim,
		},
		{
			name:      "not installed",
			app:       binaryApp(),
			info:      &binmanager.CommandInfo{Type: "binary", Command: "/store/.bin/tofu/abc"},
			installed: false,
			want:      StrategyShim,
		},
		{
			name:      "carries args",
			app:       binaryApp(),
			info:      &binmanager.CommandInfo{Type: "binary", Command: "/store/.bin/tofu/abc", Args: []string{"eval"}},
			installed: true,
			want:      StrategyShim,
		},
		{
			name:      "carries env",
			app:       binaryApp(),
			info:      &binmanager.CommandInfo{Type: "binary", Command: "/store/.bin/tofu/abc", Env: map[string]string{"TF_LOG": "debug"}},
			installed: true,
			want:      StrategyShim,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apps := binmanager.MapOfApps{"tool": tt.app}
			resolver := &stubResolver{
				infos:     map[string]*binmanager.CommandInfo{"tool": tt.info},
				installed: map[string]bool{"tool": tt.installed},
			}

			plan := BuildPlan("/repo", "/farm", apps, resolver, nil)

			if got := entryByName(t, plan, "tool").Strategy; got != tt.want {
				t.Errorf("Strategy = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildPlan_ShadowDetection asserts a shadowed name stays in the farm — the
// farm wins on PATH — and that the pre-activation location is recorded so
// `source status` can name it.
func TestBuildPlan_ShadowDetection(t *testing.T) {
	apps := binmanager.MapOfApps{"tofu": binaryApp(), "tflint": binaryApp()}
	resolver := &stubResolver{
		infos: map[string]*binmanager.CommandInfo{
			"tofu":   {Type: "binary", Command: "/store/.bin/tofu/abc"},
			"tflint": {Type: "binary", Command: "/store/.bin/tflint/abc"},
		},
		installed: map[string]bool{"tofu": true, "tflint": true},
	}
	lookPath := func(name string) (string, error) {
		if name == "tofu" {
			return "/usr/local/bin/tofu", nil
		}
		return "", errors.New("not found")
	}

	plan := BuildPlan("/repo", "/farm", apps, resolver, lookPath)

	if len(plan.Shadowed) != 1 {
		t.Fatalf("Shadowed = %+v, want exactly one", plan.Shadowed)
	}
	if plan.Shadowed[0].Name != "tofu" || plan.Shadowed[0].Path != "/usr/local/bin/tofu" {
		t.Errorf("Shadowed[0] = %+v, want tofu at /usr/local/bin/tofu", plan.Shadowed[0])
	}
	if _, ok := excludedReason(plan, "tofu"); ok {
		t.Error("a shadowed app was excluded, want it included and winning")
	}
	entryByName(t, plan, "tofu")
}

// TestBuildPlan_ExcludedAppsAreNotShadowChecked asserts lookPath is only asked
// about names the farm actually claims: a shell or deny-listed app keeps
// resolving through the system PATH by design, so reporting it as "shadowed"
// would be backwards.
func TestBuildPlan_ExcludedAppsAreNotShadowChecked(t *testing.T) {
	apps := binmanager.MapOfApps{"echo": shellApp("echo"), "sudo": binaryApp()}
	var asked []string
	lookPath := func(name string) (string, error) {
		asked = append(asked, name)
		return "/usr/bin/" + name, nil
	}

	plan := BuildPlan("/repo", "/farm", apps, nil, lookPath)

	if len(asked) != 0 {
		t.Errorf("lookPath asked about %v, want nothing", asked)
	}
	if len(plan.Shadowed) != 0 {
		t.Errorf("Shadowed = %+v, want none", plan.Shadowed)
	}
}

// TestBuildPlan_EveryExclusionCarriesAReason guards the debuggability rule: a
// name that vanishes without an explanation is undebuggable from the outside.
func TestBuildPlan_EveryExclusionCarriesAReason(t *testing.T) {
	apps := binmanager.MapOfApps{
		"echo":    shellApp("echo"),
		"sudo":    binaryApp(),
		"nothing": {},
	}

	plan := BuildPlan("/repo", "/farm", apps, nil, nil)

	if len(plan.Excluded) != 3 {
		t.Fatalf("Excluded = %+v, want three", plan.Excluded)
	}
	for _, x := range plan.Excluded {
		if x.Reason == "" {
			t.Errorf("exclusion %q has an empty reason", x.Name)
		}
	}
	if reason, _ := excludedReason(plan, "nothing"); reason != ReasonNoConfiguration {
		t.Errorf("reason for a kindless app = %q, want %q", reason, ReasonNoConfiguration)
	}
}

// TestBuildPlan_NilResolver asserts the degraded path: with nothing known
// about the store, every app is an uninstalled shim rather than a dropped name.
func TestBuildPlan_NilResolver(t *testing.T) {
	apps := binmanager.MapOfApps{"tofu": binaryApp()}

	plan := BuildPlan("/repo", "/farm", apps, nil, nil)

	entry := entryByName(t, plan, "tofu")
	if entry.Strategy != StrategyShim || entry.Installed {
		t.Errorf("entry = %+v, want an uninstalled shim", entry)
	}
}

// TestBuildPlan_Deterministic proves the output does not depend on Go's
// randomized map iteration order: the farm, the manifest and the CLI goldens
// all derive from this value, so instability here is instability everywhere.
func TestBuildPlan_Deterministic(t *testing.T) {
	apps := binmanager.MapOfApps{}
	for _, n := range []string{"zzz", "aaa", "mmm", "bbb", "yyy", "ccc", "nnn", "ddd"} {
		apps[n] = binaryApp()
	}
	apps["echo"] = shellApp("echo")
	apps["sudo"] = binaryApp()
	apps["prettier"] = nodeApp()

	build := func() string {
		t.Helper()
		resolver := &stubResolver{
			infos:     map[string]*binmanager.CommandInfo{},
			installed: map[string]bool{},
		}
		for name := range apps {
			resolver.infos[name] = &binmanager.CommandInfo{Type: "binary", Command: "/store/.bin/" + name + "/abc"}
			resolver.installed[name] = true
		}
		out, err := json.Marshal(BuildPlan("/repo", "/farm", apps, resolver, nil))
		if err != nil {
			t.Fatalf("marshal plan: %v", err)
		}
		return string(out)
	}

	first, second := build(), build()
	if first != second {
		t.Errorf("BuildPlan is not deterministic:\nfirst  = %s\nsecond = %s", first, second)
	}

	plan := BuildPlan("/repo", "/farm", apps, nil, nil)
	for i := 1; i < len(plan.Entries); i++ {
		if plan.Entries[i-1].Name >= plan.Entries[i].Name {
			t.Fatalf("Entries not sorted: %+v", plan.Entries)
		}
	}
	for i := 1; i < len(plan.Excluded); i++ {
		if plan.Excluded[i-1].Name >= plan.Excluded[i].Name {
			t.Fatalf("Excluded not sorted: %+v", plan.Excluded)
		}
	}
}

// TestBuildPlan_CarriesRootAndFarmDir pins the two fields the manifest and the
// shell renderers read straight back out.
func TestBuildPlan_CarriesRootAndFarmDir(t *testing.T) {
	plan := BuildPlan("/repo", "/cache/projects/abc/bin", binmanager.MapOfApps{}, nil, nil)

	if plan.Root != "/repo" || plan.FarmDir != "/cache/projects/abc/bin" {
		t.Errorf("plan = %+v, want the root and farm dir echoed back", plan)
	}
	if plan.Entries == nil || plan.Excluded == nil {
		t.Error("Entries/Excluded are nil, want empty slices so JSON renders [] not null")
	}
}

// TestBuildPlan_ResolvedFieldsCopied asserts the entry carries everything the
// shim needs to exec without re-resolving.
func TestBuildPlan_ResolvedFieldsCopied(t *testing.T) {
	apps := binmanager.MapOfApps{"spectral": {Jvm: &binmanager.AppConfigJVM{JarURL: "https://example.com/x.jar"}}}
	resolver := &stubResolver{
		infos: map[string]*binmanager.CommandInfo{"spectral": {
			Type:    "jvm",
			Command: "/store/.runtimes/java/abc/bin/java",
			Args:    []string{"-jar", "/store/.apps/jvm/spectral/abc/spectral.jar"},
			Env:     map[string]string{"JAVA_HOME": "/store/.runtimes/java/abc"},
		}},
		installed: map[string]bool{"spectral": true},
	}

	entry := entryByName(t, BuildPlan("/repo", "/farm", apps, resolver, nil), "spectral")

	if entry.Kind != "jvm" || entry.Provider != "jvm" {
		t.Errorf("Kind/Provider = %q/%q, want jvm/jvm", entry.Kind, entry.Provider)
	}
	if entry.Strategy != StrategyShim {
		t.Errorf("Strategy = %q, want shim: a symlink cannot carry `-jar`", entry.Strategy)
	}
	if len(entry.Args) != 2 || entry.Args[0] != "-jar" {
		t.Errorf("Args = %v, want the -jar prefix preserved", entry.Args)
	}
	if entry.Env["JAVA_HOME"] == "" {
		t.Errorf("Env = %v, want JAVA_HOME preserved", entry.Env)
	}
}

// TestSystemLookPathSkipsTheFarm is the regression test for shadow detection
// going blind in an activated shell. exec.LookPath would stop at the farm's own
// entry, which BuildPlan then drops, so re-running `datamitsu source` from an
// activated shell reported no shadows at all — the state the user is most
// likely to be in when they read the list.
func TestSystemLookPathSkipsTheFarm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH executability is decided by PATHEXT on Windows; the unix mode bits below do not apply")
	}
	farm := t.TempDir()
	system := t.TempDir()
	for _, dir := range []string{farm, system} {
		if err := os.WriteFile(filepath.Join(dir, "tofu"), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatalf("write executable in %s: %v", dir, err)
		}
	}
	// The farm is first, exactly as an activated shell has it.
	t.Setenv("PATH", farm+string(os.PathListSeparator)+system)

	got, err := SystemLookPath(farm)("tofu")
	if err != nil {
		t.Fatalf("SystemLookPath()(\"tofu\") error = %v, want the system copy", err)
	}
	if want := filepath.Join(system, "tofu"); got != want {
		t.Errorf("SystemLookPath()(\"tofu\") = %q, want %q", got, want)
	}

	// With nothing outside the farm there is no shadow to report, and the farm's
	// own entry must not be offered as one.
	t.Setenv("PATH", farm)
	if got, err := SystemLookPath(farm)("tofu"); err == nil {
		t.Errorf("SystemLookPath()(\"tofu\") = %q, want a not-found error", got)
	}

	// An empty PATH element means the working directory, and it must not turn
	// the candidate back into a bare name: exec.LookPath would then run a full
	// PATH search, hit the farm's entry first and return it, ending the walk
	// before the system copy below is ever considered.
	t.Chdir(t.TempDir())
	t.Setenv("PATH", farm+string(os.PathListSeparator)+string(os.PathListSeparator)+system)
	if got, err := SystemLookPath(farm)("tofu"); err != nil || got != filepath.Join(system, "tofu") {
		t.Errorf("SystemLookPath()(\"tofu\") with an empty PATH element = %q, %v, want the system copy", got, err)
	}

	// A non-executable file is skipped the way a shell skips it.
	t.Setenv("PATH", system)
	if err := os.WriteFile(filepath.Join(system, "tflint"), []byte("data\n"), 0o600); err != nil {
		t.Fatalf("write non-executable: %v", err)
	}
	if got, err := SystemLookPath(farm)("tflint"); err == nil {
		t.Errorf("SystemLookPath()(\"tflint\") = %q, want a not-found error", got)
	}
}

// TestSystemLookPathSkipsTheOtherFarm covers the two-farm layering machine-level
// config farms make normal: baking a project farm from a shell activated against
// a --config chain must not report that config farm's shim as the system binary
// the project farm shadows.
func TestSystemLookPathSkipsTheOtherFarm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH executability is decided by PATHEXT on Windows; the unix mode bits below do not apply")
	}
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)

	configFarm := filepath.Join(env.GetCachePath(), env.ConfigFarmsDirName, "deadbeef", "bin")
	projectFarm := filepath.Join(env.GetCachePath(), env.ProjectFarmsDirName, "cafebabe", "bin")
	system := t.TempDir()
	for _, dir := range []string{configFarm, projectFarm} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for _, dir := range []string{configFarm, projectFarm, system} {
		if err := os.WriteFile(filepath.Join(dir, "jq"), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatalf("write executable in %s: %v", dir, err)
		}
	}

	// The activated config farm sits ahead of the system, exactly as an activated
	// machine-level shell has it.
	t.Setenv("PATH", strings.Join([]string{configFarm, system}, string(os.PathListSeparator)))

	got, err := SystemLookPath(projectFarm)("jq")
	if err != nil {
		t.Fatalf("SystemLookPath()(\"jq\") error = %v, want the system copy", err)
	}
	if want := filepath.Join(system, "jq"); got != want {
		t.Errorf("SystemLookPath()(\"jq\") = %q, want %q: another farm is not the shadowed system binary", got, want)
	}

	// With only farms on PATH there is no system binary to report.
	t.Setenv("PATH", strings.Join([]string{configFarm, projectFarm}, string(os.PathListSeparator)))
	if got, err := SystemLookPath(projectFarm)("jq"); err == nil {
		t.Errorf("SystemLookPath()(\"jq\") = %q, want a not-found error", got)
	}
}
