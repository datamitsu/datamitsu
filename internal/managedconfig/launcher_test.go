package managedconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

func TestLauncherMarkerRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := LauncherMarker{
		Command:     "pnpm datamitsu",
		BinaryPath:  "/opt/bin/datamitsu",
		CoreVersion: "0.5.3",
	}

	if err := WriteLauncherMarker(root, want, false); err != nil {
		t.Fatalf("WriteLauncherMarker() error = %v", err)
	}
	got, ok := ReadLauncherMarker(root)
	if !ok {
		t.Fatal("ReadLauncherMarker() = false, want true")
	}
	if got != want {
		t.Errorf("ReadLauncherMarker() = %+v, want %+v", got, want)
	}
	if path := filepath.Join(root, config.DatamitsuDirName, LauncherMarkerName); path == "" {
		t.Error("unreachable")
	}
}

func TestReadLauncherMarkerAbsentOrInvalid(t *testing.T) {
	root := t.TempDir()
	if _, ok := ReadLauncherMarker(root); ok {
		t.Error("absent marker reported as readable")
	}

	if err := os.MkdirAll(filepath.Join(root, config.DatamitsuDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, config.DatamitsuDirName, LauncherMarkerName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadLauncherMarker(root); ok {
		t.Error("invalid JSON reported as readable")
	}
}

// TestLauncherMarkerSurvivesRebuilds pins the exec lazy-refresh contract: a
// .datamitsu/ rebuild (links swap or type-definitions swap) must carry an
// existing launcher marker into the new tree, or facts().binaryCommand would
// lose its fallback after any lazy app install.
func TestLauncherMarkerSurvivesRebuilds(t *testing.T) {
	want := LauncherMarker{Command: "pnpm datamitsu", BinaryPath: "/opt/bin/datamitsu", CoreVersion: "0.5.3"}

	t.Run("links swap", func(t *testing.T) {
		root := t.TempDir()
		if err := WriteLauncherMarker(root, want, false); err != nil {
			t.Fatal(err)
		}
		installDir := filepath.Join(root, "install", "app", "hash")
		if err := os.MkdirAll(installDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(installDir, "config.js"), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
		apps := binmanager.MapOfApps{"theme-app": {Links: map[string]string{"theme": "config.js"}}}

		links, err := CreateDatamitsuLinks(root, apps, &mockResolver{paths: map[string]string{"theme-app": installDir}}, nil, nil, false)
		if err != nil {
			t.Fatalf("CreateDatamitsuLinks() error = %v", err)
		}
		if len(links) == 0 {
			t.Error("CreateDatamitsuLinks() created no links")
		}
		got, ok := ReadLauncherMarker(root)
		if !ok {
			t.Fatal("links swap dropped the launcher marker")
		}
		if got != want {
			t.Errorf("marker = %+v, want %+v", got, want)
		}
	})

	t.Run("type definitions swap", func(t *testing.T) {
		root := t.TempDir()
		if err := WriteLauncherMarker(root, want, false); err != nil {
			t.Fatal(err)
		}

		if err := CreateDatamitsuTypeDefinitions(root, false); err != nil {
			t.Fatalf("CreateDatamitsuTypeDefinitions() error = %v", err)
		}
		got, ok := ReadLauncherMarker(root)
		if !ok {
			t.Fatal("type definitions swap dropped the launcher marker")
		}
		if got != want {
			t.Errorf("marker = %+v, want %+v", got, want)
		}
	})
}

func TestWriteLauncherMarkerDryRunAndReservedName(t *testing.T) {
	root := t.TempDir()
	if err := WriteLauncherMarker(root, LauncherMarker{Command: "datamitsu"}, true); err != nil {
		t.Fatalf("dry-run write error = %v", err)
	}
	if _, ok := ReadLauncherMarker(root); ok {
		t.Error("dry run wrote the marker")
	}

	if !isReservedLinkName("launcher.json") || !isReservedLinkName("LAUNCHER.JSON") {
		t.Error("launcher.json is not reserved as a link name")
	}
	if isReservedLinkName("my-launcher.json") {
		t.Error("unrelated name wrongly reserved")
	}
}
