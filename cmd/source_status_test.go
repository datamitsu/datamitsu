package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

// isolateCache points the cache root at a scratch directory.
//
// buildSourceStatus resolves the manifest path through env.GetCachePath and
// stats it, so without this the tests read whichever farms the developer running
// them happens to have baked — and Manifest.State would depend on the machine.
func isolateCache(t *testing.T) {
	t.Helper()
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
}

// statusPlan is a farm exercising every list the status document carries: an
// installed symlink entry, an uninstalled shim, two distinct exclusion reasons
// and a shadowed system binary.
func statusPlan(t *testing.T) sourcefarm.Plan {
	t.Helper()
	isolateCache(t)
	return sourcefarm.Plan{
		Root:    "/repo",
		FarmDir: "/cache/projects/abc/bin",
		Entries: []sourcefarm.Entry{
			{Name: "tflint", Kind: "binary", Strategy: sourcefarm.StrategyShim, Installed: false},
			{Name: "tofu", Kind: "binary", Strategy: sourcefarm.StrategySymlink, Command: "/store/.bin/tofu/deadbeef", Installed: true},
		},
		Excluded: []sourcefarm.Excluded{
			{Name: "echo", Reason: sourcefarm.ReasonShellApp},
			{Name: "sudo", Reason: sourcefarm.ReasonDenyListed},
		},
		Shadowed: []sourcefarm.Shadow{{Name: "tofu", Path: "/usr/local/bin/tofu"}},
	}
}

func marshalStatus(t *testing.T, s SourceStatus) string {
	t.Helper()
	var buf bytes.Buffer
	if err := writeJSONIndent(&buf, s); err != nil {
		t.Fatalf("writeJSONIndent() error = %v", err)
	}
	return buf.String()
}

// TestSourceStatusJSONIsStable is what lets the CLI goldens exist: the same farm
// must serialize byte-identically every time, with every list in the order
// BuildPlan sorted it into.
func TestSourceStatusJSONIsStable(t *testing.T) {
	s := buildSourceStatus(statusPlan(t))

	first, second := marshalStatus(t, s), marshalStatus(t, s)
	if first != second {
		t.Fatalf("status JSON is not byte-stable:\n%s\n%s", first, second)
	}

	// Key order follows the struct's field order, which is what makes a golden
	// diff readable rather than a reshuffle.
	for _, pair := range [][2]string{
		{`"root"`, `"farmDir"`},
		{`"farmDir"`, `"manifest"`},
		{`"manifest"`, `"entries"`},
		{`"entries"`, `"excluded"`},
		{`"excluded"`, `"shadowed"`},
	} {
		if strings.Index(first, pair[0]) > strings.Index(first, pair[1]) {
			t.Errorf("%s is serialized after %s:\n%s", pair[0], pair[1], first)
		}
	}
	if strings.Index(first, `"tflint"`) > strings.Index(first, `"tofu"`) {
		t.Errorf("entries are not sorted by name:\n%s", first)
	}
}

// TestSourceStatusJSONHasRequiredKeys asserts presence and values rather than a
// field count, so adding a field to the document never breaks this test.
func TestSourceStatusJSONHasRequiredKeys(t *testing.T) {
	out := marshalStatus(t, buildSourceStatus(statusPlan(t)))

	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("status JSON does not parse: %v\n%s", err, out)
	}

	for _, key := range []string{"root", "farmDir", "manifest", "entries", "excluded", "shadowed"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("status JSON is missing key %q:\n%s", key, out)
		}
	}
	if doc["root"] != "/repo" {
		t.Errorf("root = %v, want /repo", doc["root"])
	}
	if doc["farmDir"] != "/cache/projects/abc/bin" {
		t.Errorf("farmDir = %v, want /cache/projects/abc/bin", doc["farmDir"])
	}

	manifest, ok := doc["manifest"].(map[string]any)
	if !ok {
		t.Fatalf("manifest is not an object:\n%s", out)
	}
	for _, key := range []string{"path", "exists", "fresh", "state"} {
		if _, present := manifest[key]; !present {
			t.Errorf("manifest is missing key %q:\n%s", key, out)
		}
	}

	entries, ok := doc["entries"].([]any)
	if !ok || len(entries) != 2 {
		t.Fatalf("entries = %v, want 2 records:\n%s", doc["entries"], out)
	}
	first, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("entry is not an object:\n%s", out)
	}
	// The two facts the command exists to report: how a name runs, and whether
	// it is there yet.
	if first["strategy"] != string(sourcefarm.StrategyShim) {
		t.Errorf("entry strategy = %v, want shim", first["strategy"])
	}
	if first["installed"] != false {
		t.Errorf("entry installed = %v, want false", first["installed"])
	}
}

// TestSourceStatusShadowsOmittedWhenEmpty pins both halves of the shadow
// contract: silence when nothing is shadowed, and the absolute path when
// something is. Under D4 the absolute path is the whole value of the field — it
// is what tells the user which binary they stopped getting.
func TestSourceStatusShadowsOmittedWhenEmpty(t *testing.T) {
	clean := statusPlan(t)
	clean.Shadowed = nil
	if out := marshalStatus(t, buildSourceStatus(clean)); strings.Contains(out, "shadowed") {
		t.Errorf("empty shadow list was serialized:\n%s", out)
	}

	out := marshalStatus(t, buildSourceStatus(statusPlan(t)))
	var doc struct {
		Shadowed []sourcefarm.Shadow `json:"shadowed"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("status JSON does not parse: %v", err)
	}
	if len(doc.Shadowed) != 1 {
		t.Fatalf("shadowed = %v, want one record", doc.Shadowed)
	}
	if !filepath.IsAbs(doc.Shadowed[0].Path) {
		t.Errorf("shadow path %q is not absolute", doc.Shadowed[0].Path)
	}
}

// TestSourceStatusEveryExclusionHasAReason is the anti-silence invariant. A name
// that is refused without an explanation is indistinguishable from a name that
// was never declared, which is precisely the debugging session this command
// exists to prevent.
func TestSourceStatusEveryExclusionHasAReason(t *testing.T) {
	s := buildSourceStatus(statusPlan(t))
	if len(s.Excluded) == 0 {
		t.Fatal("fixture has no exclusions to check")
	}
	for _, x := range s.Excluded {
		if strings.TrimSpace(x.Reason) == "" {
			t.Errorf("exclusion %q carries no reason", x.Name)
		}
	}

	// The list is present even when empty: a caller distinguishing "nothing was
	// refused" from "the field is missing" should never have to.
	empty := statusPlan(t)
	empty.Excluded = nil
	if out := marshalStatus(t, buildSourceStatus(empty)); !strings.Contains(out, `"excluded": []`) {
		t.Errorf("empty exclusion list was omitted:\n%s", out)
	}
}

// TestSourceStatusJSONRoundTrips asserts the document decodes back into the same
// typed value, which is what makes it usable as the single serialization any
// future JSON surface reuses.
func TestSourceStatusJSONRoundTrips(t *testing.T) {
	want := buildSourceStatus(statusPlan(t))
	out := marshalStatus(t, want)

	var got SourceStatus
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("status JSON does not round-trip: %v\n%s", err, out)
	}
	if marshalStatus(t, got) != out {
		t.Fatalf("round-trip changed the document:\n%s\n%s", out, marshalStatus(t, got))
	}
	if got.Root != want.Root || got.FarmDir != want.FarmDir {
		t.Errorf("round-trip lost the paths: %+v", got)
	}
	if len(got.Entries) != len(want.Entries) || got.Entries[1].Command != want.Entries[1].Command {
		t.Errorf("round-trip lost an entry: %+v", got.Entries)
	}
}

// TestManifestStatusStates covers the three states a manifest file can be in
// from status's point of view. Freshness itself is sourcefarm's contract and is
// tested there; what matters here is that each state is reported distinctly and
// that only "fresh" sets Fresh.
func TestManifestStatusStates(t *testing.T) {
	dir := t.TempDir()

	missing := manifestStatus(filepath.Join(dir, "manifest.json"))
	if missing.State != ManifestMissing || missing.Exists || missing.Fresh {
		t.Errorf("missing manifest = %+v", missing)
	}

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write broken manifest: %v", err)
	}
	got := manifestStatus(broken)
	if got.State != ManifestUnreadable || !got.Exists || got.Fresh {
		t.Errorf("unreadable manifest = %+v", got)
	}
	if got.Error == "" {
		t.Error("unreadable manifest carries no error text")
	}

	// A manifest from a format version this build does not know is stale, not
	// an error: an old datamitsu meeting a newer farm must re-bake.
	future := filepath.Join(dir, "future.json")
	data, err := sourcefarm.Encode(sourcefarm.Manifest{FormatVersion: sourcefarm.ManifestFormatVersion + 1})
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(future, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if got := manifestStatus(future); got.State != ManifestStale || !got.Exists || got.Fresh {
		t.Errorf("future-version manifest = %+v", got)
	}
}

// TestManifestStatusDemotesAFarmThatIsNotThere pins the one state the watch set
// cannot see. Freshness compares stat tuples of the *tree*; the farm itself is
// never in it, so deleting an entry — or the whole farm directory — leaves a
// manifest that validates clean. In an activated shell that name then resolves
// through the rest of PATH to the system binary, silently, and no shim runs to
// report it. Activation (loadSourcePlan) and `source refresh` both already
// refuse such a manifest; status reporting it fresh would confirm the wrong
// conclusion in the command whose whole purpose is diagnosing this.
func TestManifestStatusDemotesAFarmThatIsNotThere(t *testing.T) {
	isolateCache(t)

	root := t.TempDir()
	farm, err := env.GetProjectBinPath(root)
	if err != nil {
		t.Fatalf("GetProjectBinPath() error = %v", err)
	}
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		t.Fatalf("GetProjectManifestPath() error = %v", err)
	}
	if err := os.MkdirAll(farm, 0o700); err != nil {
		t.Fatalf("create farm directory: %v", err)
	}
	writeFarmEntries(t, farm, "tofu")

	plan := sourcefarm.Plan{Root: root, FarmDir: farm, Entries: []sourcefarm.Entry{{Name: "tofu"}}}
	m := sourcefarm.BuildManifest(plan, sourcefarm.OriginGitRoot, sourcefarm.WatchSet(sourcefarm.WatchPaths(root, nil)))
	data, err := sourcefarm.Encode(m)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if got := manifestStatus(manifestPath); !got.Fresh || got.State != ManifestFresh {
		t.Fatalf("an intact farm is not reported fresh: %+v; the test no longer covers the silent case", got)
	}

	// One entry gone: the tree is untouched, so only a farm-side check catches it.
	if err := os.Remove(filepath.Join(farm, "tofu")); err != nil {
		t.Fatalf("remove farm entry: %v", err)
	}
	got := manifestStatus(manifestPath)
	if got.Fresh || got.State != ManifestStale {
		t.Errorf("a farm missing an entry = %+v, want stale", got)
	}
	if got.Error == "" {
		t.Error("a demoted manifest carries no explanation")
	}

	// The whole farm gone is the same answer, by the other branch.
	if err := os.RemoveAll(farm); err != nil {
		t.Fatalf("remove farm: %v", err)
	}
	if got := manifestStatus(manifestPath); got.Fresh || got.State != ManifestStale {
		t.Errorf("a missing farm directory = %+v, want stale", got)
	}
}

// TestRenderSourceStatusReportsEverything pins the human report's content: the
// paths, the freshness word, and every list with its reason or path. It is the
// half of D4's mitigation a person actually reads.
func TestRenderSourceStatusReportsEverything(t *testing.T) {
	s := buildSourceStatus(statusPlan(t))
	s.Manifest = SourceManifestStatus{Path: "/cache/projects/abc/manifest.json", Exists: true, Fresh: true, State: ManifestFresh}

	var buf bytes.Buffer
	if err := renderSourceStatus(&buf, s); err != nil {
		t.Fatalf("renderSourceStatus() error = %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"/repo",
		"/cache/projects/abc/bin",
		"/cache/projects/abc/manifest.json",
		ManifestFresh,
		"tofu", "symlink", "installed",
		"tflint", "shim", "not installed",
		"echo", sourcefarm.ReasonShellApp,
		"sudo", sourcefarm.ReasonDenyListed,
		"/usr/local/bin/tofu",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}

	second := new(bytes.Buffer)
	if err := renderSourceStatus(second, s); err != nil {
		t.Fatalf("renderSourceStatus() second call error = %v", err)
	}
	if second.String() != out {
		t.Errorf("report is not byte-stable:\n%s\n%s", out, second.String())
	}
}

// TestRenderSourceStatusEmptyFarm asserts the empty case still says so on every
// list rather than rendering three bare headers.
func TestRenderSourceStatusEmptyFarm(t *testing.T) {
	isolateCache(t)
	var buf bytes.Buffer
	if err := renderSourceStatus(&buf, buildSourceStatus(sourcefarm.Plan{Root: "/repo", FarmDir: "/farm"})); err != nil {
		t.Fatalf("renderSourceStatus() error = %v", err)
	}
	if n := strings.Count(buf.String(), "  none\n"); n != 3 {
		t.Errorf("empty farm reported %d empty lists, want 3:\n%s", n, buf.String())
	}
}
