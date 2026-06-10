package registry

import (
	"context"
	"strings"
	"testing"
)

// TestRegistryLookupsRefuseOffline pins the offline guard at every registry
// entry point: under DATAMITSU_OFFLINE no lookup may dial out, and the error
// must name the switch so the remediation is obvious.
func TestRegistryLookupsRefuseOffline(t *testing.T) {
	t.Setenv("DATAMITSU_OFFLINE", "1")
	ctx := context.Background()

	cases := map[string]func() error{
		"npm": func() error {
			_, err := GetNPMPackageInfo(ctx, "prettier")
			return err
		},
		"pypi": func() error {
			_, err := GetPyPIPackageInfo(ctx, "cowsay")
			return err
		},
		"nodejs": func() error {
			_, err := GetLatestNodeLTSVersion(ctx)
			return err
		},
		"python": func() error {
			_, err := GetLatestPythonStableVersion(ctx)
			return err
		},
		"godev": func() error {
			_, err := GetLatestGoRelease(ctx)
			return err
		},
		"temurin": func() error {
			_, err := GetLatestTemurinMajorVersion(ctx)
			return err
		},
	}

	for name, lookup := range cases {
		t.Run(name, func(t *testing.T) {
			err := lookup()
			if err == nil {
				t.Fatal("expected offline refusal, got nil")
			}
			if !strings.Contains(err.Error(), "DATAMITSU_OFFLINE") {
				t.Errorf("error %q should mention DATAMITSU_OFFLINE", err)
			}
		})
	}
}
