package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

func TestPullRuntimesCmd_HasMinAgeFlag(t *testing.T) {
	if pullRuntimesMinAge == nil {
		t.Fatal("pullRuntimesMinAge pointer was not wired up in init()")
	}
	if *pullRuntimesMinAge != minAgeFlagDefault {
		t.Errorf("default --min-age = %d, want sentinel %d", *pullRuntimesMinAge, minAgeFlagDefault)
	}
	if pullRuntimesCmd.Flags().Lookup("min-age") == nil {
		t.Fatal("--min-age flag was not registered on pull-runtimes")
	}
}

// TestPullNodeRuntime_MinAgePnpmNotOldEnough exercises the age-aware npm
// integration: when the only pnpm version is too fresh under the active cutoff,
// pullNodeRuntime must abort with the "no release old enough" hard error before
// reaching the nodejs.org archive fetch. This validates the
// GetNPMPackageInfoWithMinAge wiring and the nil-return error handling without
// touching the network beyond the mocked npm registry.
func TestPullNodeRuntime_MinAgePnpmNotOldEnough(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}

	origLTS := getLatestNodeLTSVersion
	defer func() { getLatestNodeLTSVersion = origLTS }()
	getLatestNodeLTSVersion = func(_ context.Context) (string, error) {
		return "24.14.0", nil
	}

	now := time.Now()
	// pnpm's only version is one hour old — too fresh for a one-day cutoff.
	times := map[string]string{
		"created": now.Add(-30 * 24 * time.Hour).Format(time.RFC3339),
		"9.0.0":   now.Add(-1 * time.Hour).Format(time.RFC3339),
	}
	srv := newNPMTestRegistry(t, "pnpm", "9.0.0", times)

	withNPMRegistry(t, srv, func() {
		data, binaries, err := pullNodeRuntime(context.Background(), 24*60)
		if err == nil {
			t.Fatal("expected pullNodeRuntime to error when no pnpm version is old enough")
		}
		if !strings.Contains(err.Error(), "pnpm") {
			t.Errorf("error = %v, want it to mention pnpm", err)
		}
		if !strings.Contains(err.Error(), "min-age 0") {
			t.Errorf("error = %v, want it to point at the --min-age 0 bypass", err)
		}
		if data != nil || binaries != nil {
			t.Error("expected nil data and binaries when no pnpm version is old enough")
		}
	})
}

// TestResolveMinAge_PullRuntimesSentinel confirms the shared resolver maps the
// pull-runtimes flag sentinel (-1) to the effective global minimum release age,
// the same contract the other pull-* commands rely on.
func TestResolveMinAge_PullRuntimesSentinel(t *testing.T) {
	eff := runtimeconfig.Effective{MinimumReleaseAgeMinutes: 4321}
	if got := resolveMinAge(minAgeFlagDefault, eff); got != 4321 {
		t.Errorf("resolveMinAge(sentinel) = %d, want 4321 (effective default)", got)
	}
	if got := resolveMinAge(0, eff); got != 0 {
		t.Errorf("resolveMinAge(0) = %d, want 0 (filtering disabled)", got)
	}
	if got := resolveMinAge(99, eff); got != 99 {
		t.Errorf("resolveMinAge(99) = %d, want 99 (custom)", got)
	}
}
