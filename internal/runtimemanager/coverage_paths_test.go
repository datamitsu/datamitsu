package runtimemanager

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
)

// hostBinaries builds a managed-binary map keyed by the current host OS/arch
// with the given libc key, so tests that go through the runtime.GOOS/GOARCH
// resolution path (ComputeRuntimeStorePath, getRuntimePath) match the host.
func hostBinaries(t *testing.T, libc string, binaryPath *string) binmanager.MapOfBinaries {
	t.Helper()
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("GetOsTypeFromString(%q) error = %v", runtime.GOOS, err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("GetArchTypeFromString(%q) error = %v", runtime.GOARCH, err)
	}
	return binmanager.MapOfBinaries{
		osType: {
			archType: {libc: binmanager.BinaryOsArchInfo{
				URL:         "https://example.com/runtime.tar.gz",
				Hash:        "deadbeef",
				ContentType: binmanager.BinContentTypeTarGz,
				BinaryPath:  binaryPath,
			}},
		},
	}
}

func hostTargetWithLibc(libc target.LibcType) target.Target {
	return target.Target{OS: runtime.GOOS, Arch: runtime.GOARCH, Libc: libc}
}

func TestComputeRuntimeStorePath(t *testing.T) {
	t.Run("managed runtime returns store path", func(t *testing.T) {
		rc := config.RuntimeConfig{
			Kind:    config.RuntimeKindUV,
			Mode:    config.RuntimeModeManaged,
			Managed: &config.RuntimeConfigManaged{Binaries: hostBinaries(t, testLibc, nil)},
		}
		rm := newTestRMWithTarget(config.MapOfRuntimes{"uv": rc}, hostTargetWithLibc(target.LibcType(testLibc)))
		path, ok, err := rm.ComputeRuntimeStorePath("uv")
		if err != nil {
			t.Fatalf("ComputeRuntimeStorePath() error = %v", err)
		}
		if !ok {
			t.Fatal("ok = false, want true for managed runtime")
		}
		if path == "" {
			t.Error("path is empty")
		}
	})

	t.Run("system runtime returns ok=false", func(t *testing.T) {
		rc := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/uv"},
		}
		rm := New(config.MapOfRuntimes{"uv": rc})
		path, ok, err := rm.ComputeRuntimeStorePath("uv")
		if err != nil {
			t.Fatalf("ComputeRuntimeStorePath() error = %v", err)
		}
		if ok {
			t.Error("ok = true, want false for system runtime")
		}
		if path != "" {
			t.Errorf("path = %q, want empty", path)
		}
	})

	t.Run("unknown runtime errors", func(t *testing.T) {
		rm := New(config.MapOfRuntimes{})
		if _, _, err := rm.ComputeRuntimeStorePath("missing"); err == nil {
			t.Error("expected error for unknown runtime")
		}
	})

	t.Run("managed mode without managed config errors", func(t *testing.T) {
		rc := config.RuntimeConfig{
			Kind:    config.RuntimeKindUV,
			Mode:    config.RuntimeModeManaged,
			Managed: nil,
		}
		// Use a glibc host so musl auto-fallback does not rewrite the mode.
		rm := newTestRMWithTarget(config.MapOfRuntimes{"uv": rc}, hostTargetWithLibc(target.LibcGlibc))
		if _, _, err := rm.ComputeRuntimeStorePath("uv"); err == nil {
			t.Error("expected error for managed runtime without managed config")
		}
	})

	t.Run("unavailable arch errors", func(t *testing.T) {
		// Binaries only for a different OS than the host.
		otherOS := syslist.OsTypeDarwin
		if runtime.GOOS == "darwin" {
			otherOS = syslist.OsTypeLinux
		}
		rc := config.RuntimeConfig{
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeManaged,
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					otherOS: {
						syslist.ArchTypeAmd64: {testLibc: binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/x.tar.gz",
							Hash:        "abc",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
					},
				},
			},
		}
		rm := newTestRMWithTarget(config.MapOfRuntimes{"uv": rc}, hostTargetWithLibc(target.LibcType(testLibc)))
		if _, _, err := rm.ComputeRuntimeStorePath("uv"); err == nil {
			t.Error("expected error for runtime unavailable on host OS")
		}
	})

	t.Run("unavailable libc errors", func(t *testing.T) {
		// Only a "musl" entry, host requests a non-musl, non-glibc libc so the
		// glibc fallback in resolveLibcKey also misses.
		rc := config.RuntimeConfig{
			Kind:    config.RuntimeKindUV,
			Mode:    config.RuntimeModeManaged,
			Managed: &config.RuntimeConfigManaged{Binaries: hostBinaries(t, "musl", nil)},
		}
		rm := newTestRMWithTarget(config.MapOfRuntimes{"uv": rc}, hostTargetWithLibc(target.LibcType("nonexistent-libc")))
		if _, _, err := rm.ComputeRuntimeStorePath("uv"); err == nil {
			t.Error("expected error for runtime unavailable for requested libc")
		}
	})
}

func TestGetRuntimePath_ErrorBranches(t *testing.T) {
	t.Run("unknown runtime", func(t *testing.T) {
		rm := New(config.MapOfRuntimes{})
		if _, err := rm.GetRuntimePath("missing"); err == nil {
			t.Error("expected error for unknown runtime")
		}
	})

	t.Run("system mode without system config", func(t *testing.T) {
		rc := config.RuntimeConfig{Kind: config.RuntimeKindUV, Mode: config.RuntimeModeSystem, System: nil}
		rm := New(config.MapOfRuntimes{"uv": rc})
		if _, err := rm.GetRuntimePath("uv"); err == nil {
			t.Error("expected error for system mode without system config")
		}
	})

	t.Run("system mode returns command", func(t *testing.T) {
		rc := config.RuntimeConfig{
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/opt/uv"},
		}
		rm := New(config.MapOfRuntimes{"uv": rc})
		got, err := rm.GetRuntimePath("uv")
		if err != nil {
			t.Fatalf("GetRuntimePath() error = %v", err)
		}
		if got != "/opt/uv" {
			t.Errorf("GetRuntimePath() = %q, want /opt/uv", got)
		}
	})

	t.Run("managed mode without managed config", func(t *testing.T) {
		rc := config.RuntimeConfig{Kind: config.RuntimeKindUV, Mode: config.RuntimeModeManaged, Managed: nil}
		rm := newTestRMWithTarget(config.MapOfRuntimes{"uv": rc}, hostTargetWithLibc(target.LibcGlibc))
		if _, err := rm.GetRuntimePath("uv"); err == nil {
			t.Error("expected error for managed mode without managed config")
		}
	})

	t.Run("unavailable arch", func(t *testing.T) {
		otherOS := syslist.OsTypeDarwin
		if runtime.GOOS == "darwin" {
			otherOS = syslist.OsTypeLinux
		}
		rc := config.RuntimeConfig{
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeManaged,
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					otherOS: {syslist.ArchTypeAmd64: {testLibc: binmanager.BinaryOsArchInfo{
						URL: "https://example.com/x.tar.gz", Hash: "abc", ContentType: binmanager.BinContentTypeTarGz,
					}}},
				},
			},
		}
		rm := newTestRMWithTarget(config.MapOfRuntimes{"uv": rc}, hostTargetWithLibc(target.LibcType(testLibc)))
		if _, err := rm.GetRuntimePath("uv"); err == nil {
			t.Error("expected error for runtime unavailable on host OS")
		}
	})

	t.Run("unsafe binaryPath rejected", func(t *testing.T) {
		bad := "../escape"
		rc := config.RuntimeConfig{
			Kind:    config.RuntimeKindUV,
			Mode:    config.RuntimeModeManaged,
			Managed: &config.RuntimeConfigManaged{Binaries: hostBinaries(t, testLibc, &bad)},
		}
		rm := newTestRMWithTarget(config.MapOfRuntimes{"uv": rc}, hostTargetWithLibc(target.LibcType(testLibc)))
		if _, err := rm.GetRuntimePath("uv"); err == nil {
			t.Error("expected error for unsafe binaryPath")
		}
	})
}

func TestGetCommandInfo_NotRuntimeApp(t *testing.T) {
	rm := New(config.MapOfRuntimes{})
	app := binmanager.App{Required: true} // no Uv/Node/Jvm/Go
	if _, err := rm.GetCommandInfo(context.Background(), "x", app); err == nil {
		t.Error("expected error for non-runtime-managed app")
	}
}

func TestComputeAppPath_NotRuntimeApp(t *testing.T) {
	rm := New(config.MapOfRuntimes{})
	app := binmanager.App{Required: true}
	if _, err := rm.ComputeAppPath("x", app); err == nil {
		t.Error("expected error for non-runtime-managed app")
	}
}

func TestComputeAppPath_UnresolvableRuntime(t *testing.T) {
	rm := New(config.MapOfRuntimes{})
	tests := map[string]binmanager.App{
		"uv":   {Uv: &binmanager.AppConfigUV{Runtime: "missing"}},
		"node": {Node: &binmanager.AppConfigNode{Runtime: "missing"}},
		"jvm":  {Jvm: &binmanager.AppConfigJVM{Runtime: "missing"}},
		"go":   {Go: &binmanager.AppConfigGo{Runtime: "missing"}},
	}
	for name, app := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := rm.ComputeAppPath(name, app); err == nil {
				t.Errorf("expected error resolving runtime for %q app", name)
			}
		})
	}
}

// TestMoveRuntimeFiles_ErrorBranches covers the error paths not exercised by the
// happy-path TestMoveRuntimeFiles in file_ops_test.go.
func TestMoveRuntimeFiles_ErrorBranches(t *testing.T) {
	t.Run("empty directory errors", func(t *testing.T) {
		srcDir := t.TempDir()
		dst := filepath.Join(t.TempDir(), "store")
		if err := moveRuntimeFiles(srcDir, dst, nil); err == nil {
			t.Error("expected error for empty source directory")
		}
	})

	t.Run("missing source errors", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "store")
		if err := moveRuntimeFiles(filepath.Join(t.TempDir(), "nope"), dst, nil); err == nil {
			t.Error("expected error for missing source")
		}
	})

	t.Run("unsafe binaryPath errors", func(t *testing.T) {
		srcDir := t.TempDir()
		src := filepath.Join(srcDir, "binary")
		if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		bad := "../escape"
		if err := moveRuntimeFiles(src, filepath.Join(t.TempDir(), "store"), &bad); err == nil {
			t.Error("expected error for unsafe binaryPath")
		}
	})
}

func TestGetJVMCommandInfo(t *testing.T) {
	systemJVM := config.MapOfRuntimes{
		"jvm": {
			Kind:   config.RuntimeKindJVM,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "/usr/bin/java"},
		},
	}

	t.Run("jar mode (no main class)", func(t *testing.T) {
		rm := New(systemJVM)
		ci, err := rm.GetJVMCommandInfo(context.Background(), "app", &binmanager.AppConfigJVM{
			Version: "1.0.0",
			JarHash: "deadbeef",
		}, nil, nil)
		if err != nil {
			t.Fatalf("GetJVMCommandInfo() error = %v", err)
		}
		if ci.Command != "/usr/bin/java" {
			t.Errorf("Command = %q, want /usr/bin/java", ci.Command)
		}
		if len(ci.Args) != 2 || ci.Args[0] != "-jar" {
			t.Errorf("Args = %v, want [-jar <path>]", ci.Args)
		}
	})

	t.Run("main class mode", func(t *testing.T) {
		rm := New(systemJVM)
		ci, err := rm.GetJVMCommandInfo(context.Background(), "app", &binmanager.AppConfigJVM{
			Version:   "1.0.0",
			JarHash:   "deadbeef",
			MainClass: "com.example.Main",
		}, nil, nil)
		if err != nil {
			t.Fatalf("GetJVMCommandInfo() error = %v", err)
		}
		if len(ci.Args) != 3 || ci.Args[0] != "-cp" || ci.Args[2] != "com.example.Main" {
			t.Errorf("Args = %v, want [-cp <path> com.example.Main]", ci.Args)
		}
	})

	t.Run("system mode without system config defaults to java", func(t *testing.T) {
		rm := New(config.MapOfRuntimes{
			"jvm": {Kind: config.RuntimeKindJVM, Mode: config.RuntimeModeSystem, System: nil},
		})
		ci, err := rm.GetJVMCommandInfo(context.Background(), "app", &binmanager.AppConfigJVM{
			Version: "1.0.0", JarHash: "deadbeef",
		}, nil, nil)
		if err != nil {
			t.Fatalf("GetJVMCommandInfo() error = %v", err)
		}
		if ci.Command != "java" {
			t.Errorf("Command = %q, want java", ci.Command)
		}
	})

	t.Run("unresolvable runtime errors", func(t *testing.T) {
		rm := New(config.MapOfRuntimes{})
		if _, err := rm.GetJVMCommandInfo(context.Background(), "app", &binmanager.AppConfigJVM{
			Runtime: "missing", Version: "1.0.0", JarHash: "deadbeef",
		}, nil, nil); err == nil {
			t.Error("expected error for unresolvable JVM runtime")
		}
	})
}

func TestRemoveAll(t *testing.T) {
	t.Run("default seam removes path", func(t *testing.T) {
		dir := t.TempDir()
		child := filepath.Join(dir, "child")
		if err := os.MkdirAll(child, 0o755); err != nil {
			t.Fatal(err)
		}
		rm := New(config.MapOfRuntimes{})
		if err := rm.removeAll(child); err != nil {
			t.Fatalf("removeAll() error = %v", err)
		}
		if _, err := os.Stat(child); !os.IsNotExist(err) {
			t.Error("path still present after removeAll")
		}
	})

	t.Run("injected seam is used", func(t *testing.T) {
		called := ""
		rm := &RuntimeManager{removeAllFunc: func(p string) error { called = p; return nil }}
		if err := rm.removeAll("/some/path"); err != nil {
			t.Fatalf("removeAll() error = %v", err)
		}
		if called != "/some/path" {
			t.Errorf("injected seam not called with path, got %q", called)
		}
	})
}

func TestInstallRuntimes_SkipBranches(t *testing.T) {
	t.Run("unavailable arch skipped", func(t *testing.T) {
		otherOS := syslist.OsTypeDarwin
		if runtime.GOOS == "darwin" {
			otherOS = syslist.OsTypeLinux
		}
		runtimes := config.MapOfRuntimes{
			"r": {
				Kind: config.RuntimeKindUV,
				Mode: config.RuntimeModeManaged,
				Managed: &config.RuntimeConfigManaged{
					Binaries: binmanager.MapOfBinaries{
						otherOS: {syslist.ArchTypeAmd64: {testLibc: binmanager.BinaryOsArchInfo{
							URL: "https://example.com/x.tar.gz", Hash: "abc", ContentType: binmanager.BinContentTypeTarGz,
						}}},
					},
				},
			},
		}
		rm := newTestRMWithTarget(runtimes, hostTargetWithLibc(target.LibcType(testLibc)))
		stats, err := rm.InstallRuntimes(context.Background(), []string{"r"}, 2)
		if err != nil {
			t.Fatalf("InstallRuntimes() error = %v", err)
		}
		if len(stats.Skipped) != 1 {
			t.Errorf("expected 1 skipped, got %+v", stats)
		}
	})

	t.Run("unavailable libc skipped", func(t *testing.T) {
		runtimes := config.MapOfRuntimes{
			"r": {
				Kind:    config.RuntimeKindUV,
				Mode:    config.RuntimeModeManaged,
				Managed: &config.RuntimeConfigManaged{Binaries: hostBinaries(t, "musl", nil)},
			},
		}
		rm := newTestRMWithTarget(runtimes, hostTargetWithLibc(target.LibcType("nonexistent-libc")))
		stats, err := rm.InstallRuntimes(context.Background(), []string{"r"}, 2)
		if err != nil {
			t.Fatalf("InstallRuntimes() error = %v", err)
		}
		if len(stats.Skipped) != 1 {
			t.Errorf("expected 1 skipped, got %+v", stats)
		}
	})
}
