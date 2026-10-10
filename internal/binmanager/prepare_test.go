package binmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/target"
)

func prepareTestManager(t *testing.T) *BinManager {
	t.Helper()
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	pin := "sha256:" + strings.Repeat("0", 64)
	apps := MapOfApps{
		"root": {
			Binary:    testBinaryApp("https://example.invalid/root", pin),
			DependsOn: edges("dep"),
			Prepare: &AppPrepare{
				Args:    []string{"--cache-dir", "${APP_PREPARED}", "materialize"},
				Env:     map[string]string{"CACHE": "${APP_PREPARED}/sub"},
				Network: new(true),
			},
		},
		"dep": {
			Binary: testBinaryApp("https://example.invalid/dep", pin),
		},
	}
	// The fixtures pin linux/amd64/glibc binaries; resolve against that
	// platform explicitly so the test does not depend on the host.
	resolver := target.NewResolver(target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcGlibc})
	return NewWithResolver(apps, nil, nil, resolver)
}

func testBinaryApp(url, pin string) *AppConfigBinary {
	return &AppConfigBinary{Binaries: MapOfBinaries{
		"linux": {"amd64": {"glibc": {URL: url, Hash: pin, ContentType: BinContentTypeBinary}}},
	}}
}

func TestComputePreparedKeyStableAcrossStoreRoots(t *testing.T) {
	bm := prepareTestManager(t)
	first, err := bm.ComputePreparedKey("root")
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	// A different store root (and HOME) must not move the key: bundle builders
	// and consumers hash the same configuration to the same subtree path.
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	second, err := bm.ComputePreparedKey("root")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if first != second {
		t.Fatalf("key changed with store root: %s vs %s", first, second)
	}
	if len(first) != 32 {
		t.Fatalf("key = %q, want 32 hex chars", first)
	}
}

func TestComputePreparedKeyInputs(t *testing.T) {
	bm := prepareTestManager(t)
	base, err := bm.ComputePreparedKey("root")
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	change := func(name string, mutate func()) {
		t.Run(name, func(t *testing.T) {
			mutate()
			key, err := bm.ComputePreparedKey("root")
			if err != nil {
				t.Fatalf("key: %v", err)
			}
			if key == base {
				t.Fatalf("key unchanged after %s", name)
			}
		})
	}

	change("args", func() {
		app := bm.mapOfApps["root"]
		app.Prepare.Args = append(append([]string(nil), app.Prepare.Args...), "--extra")
		bm.mapOfApps["root"] = app
	})
	change("prepare env", func() {
		app := bm.mapOfApps["root"]
		app.Prepare.Env["OTHER"] = "value"
		bm.mapOfApps["root"] = app
	})
	change("app env", func() {
		app := bm.mapOfApps["root"]
		app.Env = map[string]string{"KEY": "value"}
		bm.mapOfApps["root"] = app
	})
	change("alias map", func() {
		app := bm.mapOfApps["root"]
		app.DependsOn = []AppDependency{{App: "dep", Alias: "validator"}}
		bm.mapOfApps["root"] = app
	})
	change("path mode", func() {
		app := bm.mapOfApps["root"]
		app.Prepare.Path = PreparePathInherit
		bm.mapOfApps["root"] = app
	})
	change("dependency identity", func() {
		dep := bm.mapOfApps["dep"]
		dep.Binary = testBinaryApp("https://example.invalid/dep2", "sha256:"+strings.Repeat("1", 64))
		bm.mapOfApps["dep"] = dep
	})
}

func TestComputePreparedKeyIgnores(t *testing.T) {
	bm := prepareTestManager(t)
	base, err := bm.ComputePreparedKey("root")
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	// runtimeEnv, the network declaration and the inherited environment are
	// policy, not identity: changing them must not re-prepare anything.
	app := bm.mapOfApps["root"]
	app.RuntimeEnv = map[string]string{"CACHE": "${APP_PREPARED}"}
	app.Prepare.Network = new(false)
	bm.mapOfApps["root"] = app

	key, err := bm.ComputePreparedKey("root")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if key != base {
		t.Fatalf("key moved by runtimeEnv/network: %s vs %s", key, base)
	}
}

func TestComputePreparedKeyDependencyStep(t *testing.T) {
	bm := prepareTestManager(t)
	base, err := bm.ComputePreparedKey("root")
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	// A dependency declaring its own step changes the dependent's key: the
	// dependency's prepared state is part of what the dependent builds on.
	dep := bm.mapOfApps["dep"]
	dep.Prepare = &AppPrepare{Args: []string{"fill"}, Network: new(false)}
	bm.mapOfApps["dep"] = dep

	key, err := bm.ComputePreparedKey("root")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if key == base {
		t.Fatal("dependency prepare step did not change the dependent's key")
	}
}

func TestComputePreparedKeyErrors(t *testing.T) {
	bm := prepareTestManager(t)
	if _, err := bm.ComputePreparedKey("missing"); err == nil {
		t.Fatal("missing app accepted")
	}
	if _, err := bm.ComputePreparedKey("dep"); err == nil {
		t.Fatal("app without prepare accepted")
	}
}

func TestComputePreparedKeyDoesNotMutateEdges(t *testing.T) {
	bm := prepareTestManager(t)
	before := slices.Clone(bm.mapOfApps["root"].DependsOn)
	if _, err := bm.ComputePreparedKey("root"); err != nil {
		t.Fatalf("key: %v", err)
	}
	after := bm.mapOfApps["root"].DependsOn
	if !slices.Equal(before, after) {
		t.Fatalf("ComputePreparedKey mutated DependsOn: %v -> %v", before, after)
	}
}

func TestComputePreparedKeyEffectivePathDefault(t *testing.T) {
	bm := prepareTestManager(t)
	base, err := bm.ComputePreparedKey("root")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	// An explicit "declared" is the documented default; identical behavior
	// must hash identically.
	app := bm.mapOfApps["root"]
	app.Prepare.Path = PreparePathDeclared
	bm.mapOfApps["root"] = app
	key, err := bm.ComputePreparedKey("root")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if key != base {
		t.Fatalf("explicit declared changed the key: %s vs %s", key, base)
	}
}

func TestComputePreparedKeyNoCollisions(t *testing.T) {
	bm := prepareTestManager(t)
	base, err := bm.ComputePreparedKey("root")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	// Moving a pair between prepare.env and app.env changes the key: the two
	// maps are framed separately.
	app := bm.mapOfApps["root"]
	app.Prepare.Env = nil
	app.Env = map[string]string{"CACHE": "${APP_PREPARED}/sub"}
	bm.mapOfApps["root"] = app
	moved, err := bm.ComputePreparedKey("root")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if moved == base {
		t.Fatal("moving a pair between prepare.env and app.env kept the key")
	}
}

func TestResolveBindingPathAliasDirection(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	apps := binaryAppFixture(t, "dep")
	apps["root"] = App{
		Shell:     &AppConfigShell{Name: "echo"},
		DependsOn: []AppDependency{{App: "dep", Alias: "validator"}},
		RuntimeEnv: map[string]string{
			"BIN": "${APP_BIN:validator}",
		},
	}
	bm := New(apps, nil, nil)
	// ResolveCommandInfo is the read-only resolution: it must expand the
	// alias to the dependency's store path without downloading anything.
	info, _, err := bm.ResolveCommandInfo("root")
	if err != nil {
		t.Fatalf("ResolveCommandInfo: %v", err)
	}
	depPath, err := bm.getBinaryPath("dep")
	if err != nil {
		t.Fatalf("dep path: %v", err)
	}
	if info.Env["BIN"] != depPath {
		t.Fatalf("BIN = %q, want the dependency path %q", info.Env["BIN"], depPath)
	}

	// The app name alone is not the alias when the edge renamed it.
	root := apps["root"]
	root.RuntimeEnv = map[string]string{"BIN": "${APP_BIN:dep}"}
	apps["root"] = root
	bm2 := New(apps, nil, nil)
	if _, _, err := bm2.ResolveCommandInfo("root"); err == nil || !strings.Contains(err.Error(), "must be listed in direct dependsOn") {
		t.Fatalf("error = %v, want direct dependsOn rejection", err)
	}
}

func TestPreparedPathUnderStore(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	got := PreparePath()
	want := filepath.Join(env.GetStorePath(), ".prepared")
	if got != want {
		t.Fatalf("PreparePath() = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, env.GetStorePath()) {
		t.Fatalf("PreparePath() = %q is outside the store", got)
	}
}

func TestAppDependencyUnmarshal(t *testing.T) {
	for _, tt := range []struct {
		name    string
		json    string
		want    AppDependency
		wantErr string
	}{
		{name: "plain name", json: `"shellcheck"`, want: AppDependency{App: "shellcheck", Alias: "shellcheck"}},
		{name: "object with alias", json: `{"app":"jsonschema","as":"validator"}`, want: AppDependency{App: "jsonschema", Alias: "validator"}},
		{name: "object without as", json: `{"app":"jsonschema"}`, want: AppDependency{App: "jsonschema", Alias: "jsonschema"}},
		{name: "empty name", json: `""`, wantErr: "empty dependency name"},
		{name: "object without app", json: `{"as":"validator"}`, wantErr: "app is required"},
		{name: "number", json: `5`, wantErr: "must be an app name or {app, as}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got AppDependency
			err := json.Unmarshal([]byte(tt.json), &got)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestAppDependencyMarshalRoundTrip(t *testing.T) {
	edge := AppDependency{App: "jsonschema", Alias: "validator"}
	data, err := json.Marshal(edge)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back AppDependency
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != edge {
		t.Fatalf("round trip: got %+v, want %+v", back, edge)
	}
}

func TestDependencyNames(t *testing.T) {
	app := App{DependsOn: []AppDependency{
		{App: "a", Alias: "a"},
		{App: "b", Alias: "beta"},
	}}
	names := app.DependencyNames()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("DependencyNames() = %v", names)
	}
	if alias, ok := dependencyAlias(app.DependsOn, "b"); !ok || alias != "beta" {
		t.Fatalf("dependencyAlias(b) = %q, %v", alias, ok)
	}
	if _, ok := dependencyAlias(app.DependsOn, "missing"); ok {
		t.Fatal("dependencyAlias found a missing edge")
	}
}

// Compile-time guard: the closure is unchanged for callers that only need names.
var _ = func() { _, _ = AppDependencyClosure(nil, nil); _ = os.Getenv; _ = context.Background }
