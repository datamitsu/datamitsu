package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/spf13/cobra"
)

func TestRuntimeInventoryEntryFindsExactName(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rm := runtimemanager.New(config.MapOfRuntimes{
		"go-host": {
			Kind: config.RuntimeKindGo, Mode: config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: executable, SystemVersion: "fixture"},
			Go:     &config.RuntimeConfigGo{GoVersion: "fixture"},
		},
	})
	entry, found, err := runtimeInventoryEntry(rm, "go-host")
	if err != nil || !found || entry.Name != "go-host" {
		t.Fatalf("runtimeInventoryEntry(go-host) = %#v, %v, %v", entry, found, err)
	}
	if entry, found, err = runtimeInventoryEntry(rm, "GO-HOST"); err != nil || found {
		t.Fatalf("runtimeInventoryEntry(GO-HOST) = %#v, %v, %v; matching is exact", entry, found, err)
	}
}

func TestRuntimeRunRequiresSeparatorAndInterpreterArguments(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"go-host"},
		{"go-host", "-c", "print(1)"},
	} {
		err := runRuntimeRun(&cobra.Command{}, args)
		if err == nil || !strings.Contains(err.Error(), "usage: datamitsu runtime run") {
			t.Errorf("runRuntimeRun(%v) = %v, want usage error", args, err)
		}
	}
}
