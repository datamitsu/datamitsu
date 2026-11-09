package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"

	"github.com/dop251/goja"
)

func TestCreateMissingCreatesOnlyMissingFiles(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()
	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "managed content\n"
	})
	installer := NewInstaller(tmpDir, tmpDir, []string{}, nil, config.MapOfManagedConfigs{
		"managed.yml": {Content: vm.Get("contentFunc")},
	}, vm, nil)
	installer.SetCreateMissingOnly(true)

	ctx := context.Background()
	result, err := installer.InstallAll(ctx, false)
	if err != nil {
		t.Fatalf("InstallAll() error = %v", err)
	}
	if len(result) != 1 || result[0].Error != nil {
		t.Fatalf("InstallAll() = %+v, want one clean result", result)
	}
	if result[0].Action != "created" {
		t.Errorf("Action = %q, want %q", result[0].Action, "created")
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "managed.yml"))
	if err != nil || string(data) != "managed content\n" {
		t.Fatalf("created file = %q, err %v", data, err)
	}

	// A second run changes nothing and reports skipped-existing.
	results, err := installer.InstallAll(ctx, false)
	if err != nil {
		t.Fatalf("second InstallAll() error = %v", err)
	}
	if results[0].Action != "skipped-existing" {
		t.Errorf("second run Action = %q, want %q", results[0].Action, "skipped-existing")
	}
	data, _ = os.ReadFile(filepath.Join(tmpDir, "managed.yml"))
	if string(data) != "managed content\n" {
		t.Errorf("existing file rewritten: %q", data)
	}
}

func TestCreateMissingNeverTouchesExistingCustomContent(t *testing.T) {
	tmpDir := t.TempDir()
	custom := "my own rules\n"
	path := filepath.Join(tmpDir, "managed.yml")
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	vm := goja.New()
	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "managed content\n"
	})
	installer := NewInstaller(tmpDir, tmpDir, []string{}, nil, config.MapOfManagedConfigs{
		"managed.yml": {Content: vm.Get("contentFunc")},
	}, vm, nil)
	installer.SetCreateMissingOnly(true)

	result := installer.installConfig(context.Background(), "managed.yml", config.ManagedConfig{Content: vm.Get("contentFunc")}, false)
	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}
	if result.Action != "skipped-existing" {
		t.Errorf("Action = %q, want %q", result.Action, "skipped-existing")
	}
	data, _ := os.ReadFile(path)
	if string(data) != custom {
		t.Errorf("custom content rewritten: %q", data)
	}
}

func TestCreateMissingCreatesMainWhenOnlyVariantExists(t *testing.T) {
	tmpDir := t.TempDir()
	variant := filepath.Join(tmpDir, ".variant.old")
	if err := os.WriteFile(variant, []byte("user variant\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	vm := goja.New()
	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "managed content\n"
	})
	cfg := config.ManagedConfig{Content: vm.Get("contentFunc"), OtherFileNameList: []string{".variant.old"}}
	installer := NewInstaller(tmpDir, tmpDir, []string{}, nil, config.MapOfManagedConfigs{"renamed.yml": cfg}, vm, nil)
	installer.SetCreateMissingOnly(true)

	result := installer.installConfig(context.Background(), "renamed.yml", cfg, false)
	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}
	if result.Action != "created" {
		t.Errorf("Action = %q, want %q", result.Action, "created")
	}
	if len(result.DeletedFiles) != 0 {
		t.Errorf("DeletedFiles = %v, want none", result.DeletedFiles)
	}
	data, _ := os.ReadFile(variant)
	if string(data) != "user variant\n" {
		t.Errorf("variant file mutated: %q", data)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "renamed.yml"))
	if err != nil || string(data) != "managed content\n" {
		t.Errorf("main file = %q, err %v", data, err)
	}
}

func TestCreateMissingCollisionWithDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "managed.yml"), 0o755); err != nil {
		t.Fatal(err)
	}

	vm := goja.New()
	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "managed content\n"
	})
	cfg := config.ManagedConfig{Content: vm.Get("contentFunc")}
	installer := NewInstaller(tmpDir, tmpDir, []string{}, nil, config.MapOfManagedConfigs{"managed.yml": cfg}, vm, nil)
	installer.SetCreateMissingOnly(true)

	result := installer.installConfig(context.Background(), "managed.yml", cfg, false)
	if result.Error == nil {
		t.Fatal("installConfig() error = nil, want collision error")
	}
	if result.Action != "blocked" {
		t.Errorf("Action = %q, want %q", result.Action, "blocked")
	}
}

func TestCreateMissingSkipsDeleteOnlyAndInternal(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()
	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "content\n"
	})
	contentFunc := vm.Get("contentFunc")

	repoCopy := filepath.Join(tmpDir, "internal.yml")
	if err := os.WriteFile(repoCopy, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	configs := config.MapOfManagedConfigs{
		"delete.yml":   {Content: contentFunc, DeleteOnly: true},
		"internal.yml": {Content: contentFunc, Placement: config.PlacementInternal},
	}
	installer := NewInstaller(tmpDir, tmpDir, []string{}, nil, configs, vm, nil)
	installer.SetCreateMissingOnly(true)

	results, err := installer.InstallAll(context.Background(), false)
	if err != nil {
		t.Fatalf("InstallAll() error = %v", err)
	}
	actions := map[string]string{}
	for _, r := range results {
		actions[r.ConfigName] = r.Action
	}
	if actions["delete.yml"] != "skipped" {
		t.Errorf("deleteOnly Action = %q, want skipped", actions["delete.yml"])
	}
	if actions["internal.yml"] != "skipped" {
		t.Errorf("internal Action = %q, want skipped", actions["internal.yml"])
	}
	data, _ := os.ReadFile(repoCopy)
	if string(data) != "stale\n" {
		t.Errorf("internal entry's repository copy removed: %q", data)
	}
}

func TestCreateMissingSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "shared")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "real.yml"), []byte("shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.ManagedConfig{LinkTarget: "shared/real.yml"}
	installer := NewInstaller(tmpDir, tmpDir, []string{}, nil, config.MapOfManagedConfigs{"link.yml": cfg}, nil, nil)
	installer.SetCreateMissingOnly(true)

	result := installer.installConfig(context.Background(), "link.yml", cfg, false)
	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}
	if result.Action != "linked" {
		t.Errorf("Action = %q, want %q", result.Action, "linked")
	}

	// Existing link (to a regular file) is skipped, not recreated.
	result = installer.installConfig(context.Background(), "link.yml", cfg, false)
	if result.Error != nil || result.Action != "skipped-existing" {
		t.Fatalf("second run = (%q, %v), want skipped-existing", result.Action, result.Error)
	}

	// A nested managed link whose parent directories do not exist yet gets
	// them created — the exclusive path must not skip MkdirAll. The target is
	// relative to the link's own directory, so it climbs one level.
	nested := config.ManagedConfig{LinkTarget: "../shared/real.yml"}
	nestedInstaller := NewInstaller(tmpDir, tmpDir, []string{}, nil, config.MapOfManagedConfigs{".config/tool.yml": nested}, nil, nil)
	nestedInstaller.SetCreateMissingOnly(true)

	nestedResult := nestedInstaller.installConfig(context.Background(), ".config/tool.yml", nested, false)
	if nestedResult.Error != nil {
		t.Fatalf("nested installConfig() error = %v", nestedResult.Error)
	}
	if nestedResult.Action != "linked" {
		t.Errorf("nested Action = %q, want %q", nestedResult.Action, "linked")
	}
	info, err := os.Lstat(filepath.Join(tmpDir, ".config", "tool.yml"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("nested link = (%v, err %v); want a symlink", info, err)
	}
	if target, linkErr := os.Readlink(filepath.Join(tmpDir, ".config", "tool.yml")); linkErr != nil || target != "../shared/real.yml" {
		t.Errorf("nested link target = %q, err %v", target, linkErr)
	}
}

func TestCreateMissingDryRunWritesNothing(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()
	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "content\n"
	})
	cfg := config.ManagedConfig{Content: vm.Get("contentFunc")}
	installer := NewInstaller(tmpDir, tmpDir, []string{}, nil, config.MapOfManagedConfigs{"managed.yml": cfg}, vm, nil)
	installer.SetCreateMissingOnly(true)

	result := installer.installConfig(context.Background(), "managed.yml", cfg, true)
	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}
	if result.Action != "created" {
		t.Errorf("Action = %q, want %q", result.Action, "created")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "managed.yml")); !os.IsNotExist(err) {
		t.Error("dry run wrote the file")
	}
}
