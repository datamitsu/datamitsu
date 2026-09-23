package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func ejectableEntry(placement config.ManagedConfigPlacement) config.ManagedConfig {
	return config.ManagedConfig{
		Tools:             []string{"gitleaks"},
		Scope:             config.ScopeGitRoot,
		Ejectable:         true,
		Placement:         placement,
		OtherFileNameList: []string{"gitleaks.toml"},
	}
}

func installerWithPristine(root string, configs config.MapOfManagedConfigs, pristine string) *Installer {
	layerMap := config.ManagedConfigLayerMap{
		".gitleaks.toml": {FileName: ".gitleaks.toml", PristineContent: &pristine},
	}
	return NewInstaller(root, root, nil, nil, configs, nil, &layerMap)
}

func writeRepoFile(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInternalEntryRemovesPristineRepositoryCopies(t *testing.T) {
	root := t.TempDir()
	main := writeRepoFile(t, root, ".gitleaks.toml", "pristine")
	alt := writeRepoFile(t, root, "gitleaks.toml", "pristine")
	installer := installerWithPristine(root, config.MapOfManagedConfigs{".gitleaks.toml": ejectableEntry(config.PlacementInternal)}, "pristine")

	if conflicts := installer.EjectConflicts(); len(conflicts) != 0 {
		t.Fatalf("EjectConflicts() = %v, want none", conflicts)
	}
	results, err := installer.InstallAll(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.Action != "internal" || r.Error != nil {
		t.Fatalf("result = %+v", r)
	}
	if want := filepath.Join(root, ".datamitsu", "configs", ".gitleaks.toml"); r.FilePath != want {
		t.Errorf("FilePath = %q, want %q", r.FilePath, want)
	}
	if !slices.Equal(r.DeletedFiles, []string{main, alt}) {
		t.Errorf("DeletedFiles = %v", r.DeletedFiles)
	}
	for _, p := range []string{main, alt} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived: %v", p, err)
		}
	}
}

func TestInternalEntryRefusesToDeleteChangedFiles(t *testing.T) {
	root := t.TempDir()
	main := writeRepoFile(t, root, ".gitleaks.toml", "pristine\n[allowlist]\n")
	installer := installerWithPristine(root, config.MapOfManagedConfigs{".gitleaks.toml": ejectableEntry(config.PlacementInternal)}, "pristine\n")

	conflicts := installer.EjectConflicts()
	if len(conflicts) != 1 || conflicts[0].Path != main || conflicts[0].Alternate {
		t.Fatalf("EjectConflicts() = %v", conflicts)
	}
	if msg := conflicts[0].Error(); !strings.HasPrefix(msg, ".gitleaks.toml differs") || !strings.Contains(msg, `adding "gitleaks" to ejectConfigs`) {
		t.Errorf("message does not say how to keep the file: %s", msg)
	}

	results, _ := installer.InstallAll(context.Background(), false)
	if results[0].Error == nil {
		t.Fatal("InstallAll deleted or accepted a changed file")
	}
	if got, _ := os.ReadFile(main); string(got) != "pristine\n[allowlist]\n" {
		t.Errorf("file changed: %q", got)
	}
}

func TestEjectedEntryKeepsChangedAlternate(t *testing.T) {
	root := t.TempDir()
	alt := writeRepoFile(t, root, "gitleaks.toml", "custom")
	installer := installerWithPristine(root, config.MapOfManagedConfigs{".gitleaks.toml": ejectableEntry(config.PlacementRepo)}, "pristine")

	conflicts := installer.EjectConflicts()
	if len(conflicts) != 1 || conflicts[0].Path != alt || !conflicts[0].Alternate {
		t.Fatalf("EjectConflicts() = %v", conflicts)
	}
}

func TestEjectConflictsHonourToolSelection(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, ".gitleaks.toml", "custom")
	pristine := "pristine"
	layerMap := config.ManagedConfigLayerMap{".gitleaks.toml": {PristineContent: &pristine}}
	installer := NewInstaller(root, root, nil, []string{"yamlfmt"}, config.MapOfManagedConfigs{".gitleaks.toml": ejectableEntry(config.PlacementInternal)}, nil, &layerMap)
	if conflicts := installer.EjectConflicts(); len(conflicts) != 0 {
		t.Fatalf("an unselected tool's file was checked: %v", conflicts)
	}
}

func TestFormattedButUnchangedFileIsNotAConflict(t *testing.T) {
	root := t.TempDir()
	disk := "title = \"x\"\n\n[extend]\npath = \".datamitsu/base.toml\"\n"
	writeRepoFile(t, root, ".gitleaks.toml", disk)
	pristine := "title = 'x'\n[extend]\npath = '.datamitsu/base.toml'\n"

	for _, tt := range []struct {
		name       string
		rerendered string
		want       int
		failed     bool
	}{
		{"re-render equals the pristine render", pristine, 0, false},
		{"re-render keeps something of the project's", pristine + "[allowlist]\n", 1, false},
		{"a layer threw while re-rendering the file", pristine, 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original, rerendered, pristineCopy := disk, tt.rerendered, pristine
			layerMap := config.ManagedConfigLayerMap{".gitleaks.toml": {
				OriginalContent: &original,
				Layers:          []config.ManagedConfigLayerEntry{{LayerName: "base", GeneratedContent: &rerendered}},
				PristineContent: &pristineCopy,
				RenderFailed:    tt.failed,
			}}
			installer := NewInstaller(root, root, nil, nil, config.MapOfManagedConfigs{".gitleaks.toml": ejectableEntry(config.PlacementInternal)}, nil, &layerMap)
			if got := len(installer.EjectConflicts()); got != tt.want {
				t.Fatalf("conflicts = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestInternalEntryIsRemovedWhateverTheProjectTypes(t *testing.T) {
	root := t.TempDir()
	main := writeRepoFile(t, root, ".gitleaks.toml", "pristine")
	entry := ejectableEntry(config.PlacementInternal)
	entry.ProjectTypes = []string{"docker-project"}
	installer := installerWithPristine(root, config.MapOfManagedConfigs{".gitleaks.toml": entry}, "pristine")

	results, err := installer.InstallAll(context.Background(), false)
	if err != nil || results[0].Action != "internal" {
		t.Fatalf("result = %+v, %v", results, err)
	}
	if _, err := os.Stat(main); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a stale copy of an inapplicable internal entry survived: %v", err)
	}
}

func TestEjectedGitRootEntryAppliesThroughTypesElsewhereInTheRepository(t *testing.T) {
	root := t.TempDir()
	entry := ejectableEntry(config.PlacementRepo)
	entry.ProjectTypes = []string{"docker-project"}
	configs := config.MapOfManagedConfigs{".gitleaks.toml": entry}

	rendered := "rendered\n"
	layerMap := config.ManagedConfigLayerMap{".gitleaks.toml": {
		Layers: []config.ManagedConfigLayerEntry{{LayerName: "base", GeneratedContent: &rendered}},
	}}

	withoutRepoTypes := NewInstaller(root, root, []string{"npm-package"}, nil, configs, nil, &layerMap)
	if results, _ := withoutRepoTypes.InstallAll(context.Background(), true); results[0].Action != "skipped" {
		t.Fatalf("root types alone: action = %q, want skipped", results[0].Action)
	}

	installer := NewInstaller(root, root, []string{"npm-package"}, nil, configs, nil, &layerMap)
	installer.SetRepoProjectTypes([]string{"npm-package", "docker-project"})
	results, err := installer.InstallAll(context.Background(), false)
	if err != nil || results[0].Error != nil || results[0].Action != "created" {
		t.Fatalf("result = %+v, %v", results, err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, ".gitleaks.toml")); string(got) != rendered {
		t.Errorf("written = %q", got)
	}
}
