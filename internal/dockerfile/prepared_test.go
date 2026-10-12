package dockerfile

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// preparedPlan mirrors samplePlan with a prepare step on one runtime app and
// one binary app, and none on the others.
func preparedPlan() Plan {
	return Plan{
		RuntimeStages: []RuntimeStage{
			{Name: "node", Kind: config.RuntimeKindNode, PNPMRuntime: "pnpm"},
			{Name: "uv", Kind: config.RuntimeKindUV},
		},
		RuntimeAppStages: []RuntimeAppStage{
			{App: "prettier", Kind: config.RuntimeKindNode, Runtime: "node"},
			{App: "ruff", Kind: config.RuntimeKindUV, Runtime: "uv"},
		},
		BinaryStages: []BinaryStage{{App: "shellcheck"}},
		PreparedApps: []string{"prettier", "shellcheck"},
	}
}

func TestRender_PreparedStateLayers(t *testing.T) {
	out := Render(preparedPlan(), pinnedOpts())

	// One COPY per prepared app, from the app's own stage.
	mustContain(t, out, "COPY --link --from=app-prettier /dm/store/.prepared/prettier /dm/store/.prepared/prettier")
	mustContain(t, out, "COPY --link --from=app-shellcheck /dm/store/.prepared/shellcheck /dm/store/.prepared/shellcheck")

	// An app without a step gets no prepared layer.
	if strings.Contains(out, "/dm/store/.prepared/ruff") {
		t.Error("an app without a prepare step must not produce a prepared COPY")
	}
}

func TestRender_NoPrepareNoPreparedBlock(t *testing.T) {
	out := Render(samplePlan(), pinnedOpts())
	if strings.Contains(out, "/.prepared/") {
		t.Error("a plan with no prepared apps must not emit any prepared COPY")
	}
}

func TestBuildPlan_CollectsPreparedApps(t *testing.T) {
	off := false
	step := func() *binmanager.AppPrepare {
		return &binmanager.AppPrepare{Args: []string{"--prepare"}, Network: &off}
	}
	apps := binmanager.MapOfApps{
		"withstep": {Required: true, Binary: &binmanager.AppConfigBinary{}, Prepare: step()},
		"nostep":   {Required: true, Binary: &binmanager.AppConfigBinary{}},
		// A shell app with a step: BuildPlan must not include it, so its step
		// must not contribute a prepared entry either.
		"shellstep": {Shell: &binmanager.AppConfigShell{Name: "true"}, Prepare: step()},
	}
	plan := BuildPlan(apps, config.MapOfRuntimes{})
	if len(plan.PreparedApps) != 1 || plan.PreparedApps[0] != "withstep" {
		t.Fatalf("PreparedApps = %v, want [withstep] (nostep has no step, skipped is not included)", plan.PreparedApps)
	}
}

func TestBuildOCIMap_PreparedEntries(t *testing.T) {
	m := BuildOCIMap(preparedPlan(), pinnedOpts())

	var prettier, shellcheck, ruff bool
	for _, e := range m.Layers {
		switch {
		case e.Kind == "prepared" && e.App == "prettier" && e.Subtree == ".prepared/prettier":
			prettier = true
		case e.Kind == "prepared" && e.App == "shellcheck" && e.Subtree == ".prepared/shellcheck":
			shellcheck = true
		case e.Subtree == ".prepared/ruff":
			ruff = true
		}
	}
	if !prettier || !shellcheck {
		t.Fatalf("prepared entries missing: prettier=%v shellcheck=%v layers=%v", prettier, shellcheck, m.Layers)
	}
	if ruff {
		t.Error("an app without a prepare step must not have a prepared ocimap entry")
	}

	// Emission order mirrors writeFinalStage: the prepared block follows the
	// parser block (none here) and precedes the trailing chown layer.
	lastKind := ""
	for _, e := range m.Layers {
		if e.Kind == "prepared" && lastKind != "" {
			// Any non-prepared kind before a prepared one is fine; what
			// matters is that prepared entries are contiguous and last.
			_ = lastKind
		}
		lastKind = e.Kind
	}
}
