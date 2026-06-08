package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

// fakeCommandInfoGetter records install requests and can fail on a chosen app.
type fakeCommandInfoGetter struct {
	installed []string
	failOn    string
}

func (f *fakeCommandInfoGetter) GetCommandInfo(_ context.Context, appName string) (*binmanager.CommandInfo, error) {
	if appName == f.failOn {
		return nil, errors.New("install failed")
	}
	f.installed = append(f.installed, appName)
	return &binmanager.CommandInfo{}, nil
}

func TestInstallCmd_NoArgsErrors(t *testing.T) {
	if err := runInstall(context.Background(), nil, nil, false); err == nil {
		t.Error("runInstall with no apps and no runtimes should error")
	}
}

func TestVersionCheckArgs(t *testing.T) {
	t.Run("default is --version", func(t *testing.T) {
		args, ok := versionCheckArgs(binmanager.App{Binary: &binmanager.AppConfigBinary{}})
		if !ok || len(args) != 1 || args[0] != "--version" {
			t.Errorf("args=%v ok=%v, want [--version] true", args, ok)
		}
	})
	t.Run("custom args honored", func(t *testing.T) {
		app := binmanager.App{
			Binary:       &binmanager.AppConfigBinary{},
			VersionCheck: &binmanager.AppVersionCheck{Args: []string{"version", "--short"}},
		}
		args, ok := versionCheckArgs(app)
		if !ok || len(args) != 2 || args[0] != "version" {
			t.Errorf("args=%v ok=%v, want [version --short] true", args, ok)
		}
	})
	t.Run("disabled is not verifiable", func(t *testing.T) {
		app := binmanager.App{
			Binary:       &binmanager.AppConfigBinary{},
			VersionCheck: &binmanager.AppVersionCheck{Disabled: true},
		}
		if _, ok := versionCheckArgs(app); ok {
			t.Error("disabled version check should not be verifiable")
		}
	})
	t.Run("shell is not verifiable", func(t *testing.T) {
		if _, ok := versionCheckArgs(binmanager.App{Shell: &binmanager.AppConfigShell{Name: "fmt"}}); ok {
			t.Error("shell app should not be verifiable")
		}
	})
}

func TestInstallSmartInitApps_InstallsEachApp(t *testing.T) {
	getter := &fakeCommandInfoGetter{}
	if err := installSmartInitApps(context.Background(), getter, []string{"shellcheck", "prettier"}); err != nil {
		t.Fatalf("installSmartInitApps: %v", err)
	}
	// installSmartInitApps sorts before installing.
	want := []string{"prettier", "shellcheck"}
	if len(getter.installed) != len(want) {
		t.Fatalf("installed = %v, want %v", getter.installed, want)
	}
	for i := range want {
		if getter.installed[i] != want[i] {
			t.Errorf("installed[%d] = %q, want %q", i, getter.installed[i], want[i])
		}
	}
}

func TestInstallSmartInitApps_PropagatesError(t *testing.T) {
	getter := &fakeCommandInfoGetter{failOn: "broken"}
	err := installSmartInitApps(context.Background(), getter, []string{"ok", "broken"})
	if err == nil {
		t.Fatal("expected error when an app fails to install")
	}
}
