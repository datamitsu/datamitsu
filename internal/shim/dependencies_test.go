package shim

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/sourcefarm"
)

func TestDependencyDeletionRepairsRoot(t *testing.T) {
	for _, remove := range []bool{false, true} {
		name := "healthy"
		if remove {
			name = "removed"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			dir := t.TempDir()
			root := filepath.Join(dir, "proxy")
			dep := filepath.Join(dir, "private-upstream")
			for _, path := range []string{root, dep} {
				if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			entry := sourcefarm.Entry{Name: "proxy", Command: root, RequiredPaths: []string{dep}, Env: map[string]string{"UPSTREAM": dep}, Installed: true, Strategy: sourcefarm.StrategyShim}
			h.writeManifest(sourcefarm.Manifest{Entries: []sourcefarm.Entry{entry}})
			if remove {
				if err := os.Remove(dep); err != nil {
					t.Fatal(err)
				}
			}
			if got := h.d.entryHealthy(entry); got == remove {
				t.Fatalf("healthy = %v, removed = %v", got, remove)
			}
			h.spawnFunc = func(args []string) error {
				if !slices.Equal(args, []string{"install", "proxy"}) {
					t.Fatalf("spawn = %v", args)
				}
				return os.WriteFile(dep, []byte("repaired"), 0o755)
			}
			h.invokeThroughFarm("proxy")
			if code, handled := h.d.Dispatch(); !handled || code != 0 {
				t.Fatalf("dispatch = %d, %v: %s", code, handled, h.stderr.String())
			}
			wantSpawns := 0
			if remove {
				wantSpawns = 1
			}
			if len(h.spawns) != wantSpawns {
				t.Fatalf("spawns = %v", h.spawns)
			}
			if len(h.execs) != 1 || !slices.Contains(h.execs[0].environ, "UPSTREAM="+dep) {
				t.Fatalf("exec = %v", h.execs)
			}
		})
	}
}
